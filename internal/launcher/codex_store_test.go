package launcher

import (
	"context"
	"strings"
	"testing"

	"github.com/vkuptcov/agents-safe-environment/internal/codexinstall"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/dockercli"
)

func TestEnsureCodexInstallationVolumeCreatesWhenAbsent(t *testing.T) {
	t.Parallel()
	owned, err := codexinstall.NewCurrentIdentity(1000, codexinstall.Target{Architecture: codexinstall.ArchitectureAMD64})
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeCommandRunner{outputs: []commandResult{
		{output: []byte(`{"Os":"linux","Arch":"amd64"}`)},
		{output: []byte("Error response from daemon: get x: no such volume"), err: fakeExitError{code: 1}},
		{output: []byte("codex-safe-codex-v1-1000-linux-amd64\n")},
		// Post-create re-inspection: docker volume create is idempotent, so ownership is validated
		// against the resolved volume even on the create path.
		{output: volumeInspectionJSON(t, owned)},
	}}
	cli := dockercli.New("docker", runner)

	identity, err := EnsureCodexInstallationVolume(context.Background(), cli, 1000)
	if err != nil {
		t.Fatalf("EnsureCodexInstallationVolume() error = %v", err)
	}
	if identity.VolumeName() != "codex-safe-codex-v1-1000-linux-amd64" {
		t.Fatalf("identity = %#v", identity)
	}
	if len(runner.combinedCalls) != 4 {
		t.Fatalf(
			"combined calls = %#v, want daemon inspect, volume inspect, volume create, post-create inspect",
			runner.combinedCalls,
		)
	}
	createCall := runner.combinedCalls[2]
	if createCall[1] != "volume" || createCall[2] != "create" {
		t.Fatalf("create call = %#v", createCall)
	}
	for _, label := range identity.Labels() {
		assertLabel(t, createCall, label.Key, label.Value)
	}
	postCreateInspect := runner.combinedCalls[3]
	if postCreateInspect[1] != "volume" || postCreateInspect[2] != "inspect" {
		t.Fatalf("post-create call = %#v, want a re-inspection for ownership validation", postCreateInspect)
	}
}

func TestEnsureCodexInstallationVolumeFailsClosedOnForeignVolumeAdoptedInCreateRace(t *testing.T) {
	t.Parallel()
	// InspectVolume reports the name absent, but a foreign volume is created at the same name before
	// our create runs. docker volume create adopts it (idempotent success) without applying our
	// labels, so the post-create re-inspection must reject the mismatched store.
	runner := &fakeCommandRunner{outputs: []commandResult{
		{output: []byte(`{"Os":"linux","Arch":"amd64"}`)},
		{output: []byte("Error response from daemon: get x: no such volume"), err: fakeExitError{code: 1}},
		{output: []byte("codex-safe-codex-v1-1000-linux-amd64\n")},
		{output: []byte(`[{"Name":"codex-safe-codex-v1-1000-linux-amd64","Labels":{"foreign":"true"}}]`)},
	}}
	cli := dockercli.New("docker", runner)

	if _, err := EnsureCodexInstallationVolume(context.Background(), cli, 1000); err == nil {
		t.Fatal("EnsureCodexInstallationVolume() must fail closed when create adopts a foreign volume")
	}
	if len(runner.combinedCalls) != 4 {
		t.Fatalf("combined calls = %#v, want a post-create re-inspection before rejection", runner.combinedCalls)
	}
}

func TestEnsureCodexInstallationVolumeValidatesMatchingOwnership(t *testing.T) {
	t.Parallel()
	identity, err := codexinstall.NewCurrentIdentity(1000, codexinstall.Target{Architecture: codexinstall.ArchitectureAMD64})
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeCommandRunner{outputs: []commandResult{
		{output: []byte(`{"Os":"linux","Arch":"amd64"}`)},
		{output: volumeInspectionJSON(t, identity)},
	}}
	cli := dockercli.New("docker", runner)

	got, err := EnsureCodexInstallationVolume(context.Background(), cli, 1000)
	if err != nil {
		t.Fatalf("EnsureCodexInstallationVolume() error = %v", err)
	}
	if got != identity {
		t.Fatalf("identity = %#v, want %#v", got, identity)
	}
	if len(runner.combinedCalls) != 2 {
		t.Fatalf("combined calls = %#v, want no create call for an already-owned volume", runner.combinedCalls)
	}
}

func TestEnsureCodexInstallationVolumeFailsClosedOnMissingLabel(t *testing.T) {
	t.Parallel()
	runner := &fakeCommandRunner{outputs: []commandResult{
		{output: []byte(`{"Os":"linux","Arch":"amd64"}`)},
		{output: []byte(
			`[{"Name":"codex-safe-codex-v1-1000-linux-amd64","Labels":{"codex-safe.managed":"true"}}]`,
		)},
	}}
	cli := dockercli.New("docker", runner)

	if _, err := EnsureCodexInstallationVolume(context.Background(), cli, 1000); err == nil {
		t.Fatal("EnsureCodexInstallationVolume() must fail closed on a volume missing ownership labels")
	}
	if len(runner.combinedCalls) != 2 {
		t.Fatalf(
			"combined calls = %#v, a mismatched volume must never be adopted, relabeled, or recreated",
			runner.combinedCalls,
		)
	}
}

func TestEnsureCodexInstallationVolumeFailsClosedOnConflictingLabel(t *testing.T) {
	t.Parallel()
	runner := &fakeCommandRunner{outputs: []commandResult{
		{output: []byte(`{"Os":"linux","Arch":"amd64"}`)},
		{output: []byte(`[{"Name":"codex-safe-codex-v1-1000-linux-amd64","Labels":{
			"codex-safe.managed":"true",
			"codex-safe.resource":"codex-installation",
			"codex-safe.host-uid":"1000",
			"codex-safe.codex-target":"linux-amd64",
			"codex-safe.codex-store-protocol":"999"
		}}]`)},
	}}
	cli := dockercli.New("docker", runner)

	if _, gotErr := EnsureCodexInstallationVolume(context.Background(), cli, 1000); gotErr == nil {
		t.Fatal("EnsureCodexInstallationVolume() must fail closed on a conflicting store-protocol label")
	} else if !strings.Contains(gotErr.Error(), "codex-safe.codex-store-protocol") {
		t.Fatalf("error = %v, want it to name the conflicting label", gotErr)
	}
}

func TestEnsureCodexInstallationVolumeRejectsNonLinuxDaemonBeforeTouchingVolume(t *testing.T) {
	t.Parallel()
	runner := &fakeCommandRunner{outputs: []commandResult{
		{output: []byte(`{"Os":"darwin","Arch":"arm64"}`)},
	}}
	cli := dockercli.New("docker", runner)

	if _, err := EnsureCodexInstallationVolume(context.Background(), cli, 1000); err == nil {
		t.Fatal("EnsureCodexInstallationVolume() must reject a non-Linux daemon")
	}
	if len(runner.combinedCalls) != 1 {
		t.Fatalf("combined calls = %#v, want only the daemon inspect before rejection", runner.combinedCalls)
	}
}

func TestResolveCodexTargetRejectsUnsupportedArchitecture(t *testing.T) {
	t.Parallel()
	runner := &fakeCommandRunner{outputs: []commandResult{
		{output: []byte(`{"Os":"linux","Arch":"386"}`)},
	}}
	cli := dockercli.New("docker", runner)

	if _, err := ResolveCodexTarget(context.Background(), cli); err == nil {
		t.Fatal("ResolveCodexTarget() must reject an unsupported architecture")
	}
}

func volumeInspectionJSON(t *testing.T, identity codexinstall.Identity) []byte {
	t.Helper()
	labels := "{"
	for index, label := range identity.Labels() {
		if index > 0 {
			labels += ","
		}
		labels += `"` + label.Key + `":"` + label.Value + `"`
	}
	labels += "}"
	return []byte(`[{"Name":"` + identity.VolumeName() + `","Labels":` + labels + `}]`)
}
