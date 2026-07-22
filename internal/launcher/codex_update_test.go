package launcher

import (
	"context"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/dockercli"
)

func TestUpdateCodexRunsIsolatedMaintenanceContainer(t *testing.T) {
	t.Parallel()
	runner := &fakeCommandRunner{}
	err := updateCodex(
		context.Background(), dockercli.New("docker", runner), "codex-safe-mvp:local", io.Discard, io.Discard,
	)
	if err != nil {
		t.Fatalf("updateCodex() error = %v", err)
	}
	want := []string{
		"docker", "run", "--rm",
		"--name", codexUpdateContainerName,
		"--mount", "type=volume,source=codex-safe-codex,target=/opt/codex-safe/codex",
		"--entrypoint", codexUpdateEntrypoint,
		"codex-safe-mvp:local",
	}
	if !reflect.DeepEqual(runner.runCalls, [][]string{want}) {
		t.Fatalf("run calls = %#v, want %#v", runner.runCalls, [][]string{want})
	}
	joined := strings.Join(want, " ")
	for _, forbidden := range []string{"sysbox-runc", "docker.sock", "--workdir", "--env", "type=bind"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("maintenance argv contains %q: %#v", forbidden, want)
		}
	}
}

func TestUpdateCodexPreservesContainerExitCode(t *testing.T) {
	t.Parallel()
	runner := &fakeCommandRunner{runErrors: []error{fakeExitError{code: 23}}}
	err := updateCodex(
		context.Background(), dockercli.New("docker", runner), "codex-safe-mvp:local", io.Discard, io.Discard,
	)
	if err == nil || dockercli.ExitCode(err) != 23 {
		t.Fatalf("updateCodex() error = %v, exit = %d", err, dockercli.ExitCode(err))
	}
}
