package launcher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/dockercli"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
)

func TestStorageVolumeCreationFailureStopsContainerCreation(t *testing.T) {
	runner := &fakeCommandRunner{volumeError: errors.New("volume unavailable")}
	plan := simplePlan()
	plan.DockerStorage.ProjectRoot = "/primary/project"
	plan.DockerStorage.Branch = "feature/full-branch-name"
	attempt := &launchAttempt{
		docker:        testDocker(runner),
		cli:           dockercli.New("docker", runner),
		plan:          plan,
		image:         "image",
		containerName: "session",
	}
	err := attempt.ensureDockerStorage(context.Background())
	if err == nil || !strings.Contains(err.Error(), "volume unavailable") {
		t.Fatalf("error = %v", err)
	}
	if len(runner.combinedCalls) != 0 {
		t.Fatalf("container commands ran after volume failure: %v", runner.combinedCalls)
	}
	want := []string{
		"docker", "volume", "create",
		"--label", "agents-safe.managed=true",
		"--label", "agents-safe.docker-storage=branch",
		"--label", "agents-safe.project-path=/primary/project",
		"--label", "agents-safe.host-uid=1000",
		"--label", "agents-safe.git-branch=feature/full-branch-name",
		plan.DockerStorage.Volume,
	}
	if len(runner.volumeCalls) != 1 || !reflect.DeepEqual(runner.volumeCalls[0], want) {
		t.Fatalf("volume calls = %v, want %v", runner.volumeCalls, want)
	}
}

func TestStorageVolumeLabelsAttributeOnlyOwnedScopes(t *testing.T) {
	storage := launchplan.DockerStorage{ProjectRoot: "/primary/project", Branch: "main"}
	tests := map[projectenv.DockerStorageMode][]string{
		projectenv.DockerStorageBranch:  {managedLabel, dockerStorageLabel, projectPathLabel, hostUIDLabel, gitBranchLabel},
		projectenv.DockerStorageProject: {managedLabel, dockerStorageLabel, projectPathLabel, hostUIDLabel},
		projectenv.DockerStorageShared:  {managedLabel, dockerStorageLabel},
	}
	for mode, want := range tests {
		t.Run(string(mode), func(t *testing.T) {
			// Only the resolver records identities a scope owns, so an unowned scope carries none here.
			scoped := storage
			scoped.Mode = mode
			if mode == projectenv.DockerStorageShared {
				scoped.ProjectRoot, scoped.Branch = "", ""
			}
			if mode == projectenv.DockerStorageProject {
				scoped.Branch = ""
			}
			plan := simplePlan()
			plan.DockerStorage = scoped
			attempt := &launchAttempt{docker: testDocker(&fakeCommandRunner{}), plan: plan}
			var keys []string
			for _, label := range attempt.storageVolumeLabels() {
				keys = append(keys, label.Key)
			}
			if !reflect.DeepEqual(keys, want) {
				t.Fatalf("label keys = %v, want %v", keys, want)
			}
		})
	}
}

func TestStorageMismatchNamesExistingAndRequestedVolumes(t *testing.T) {
	var inspection dockercli.ContainerInspection
	mounts := `{"Mounts":[{"Type":"volume","Name":"old-volume","Destination":"/var/lib/docker"}]}`
	if err := json.Unmarshal([]byte(mounts), &inspection); err != nil {
		t.Fatal(err)
	}
	attempt := &launchAttempt{plan: launchplan.Plan{DockerStorage: launchplan.DockerStorage{
		Mode: projectenv.DockerStorageBranch, Volume: "new-volume",
	}}}
	message := (&launchConfigMismatchError{storageDifference: attempt.storageDifference(inspection)}).Error()
	for _, text := range []string{"old-volume", "new-volume", "branch", "--docker-storage=project"} {
		if !strings.Contains(message, text) {
			t.Fatalf("missing %q in %q", text, message)
		}
	}
	attempt.plan.DockerStorage.Volume = "old-volume"
	if got := attempt.storageDifference(inspection); got != "" {
		t.Fatalf("matching storage diagnosed: %s", got)
	}
}

// capturedDocker is testDocker with its stderr captured.
func capturedDocker(runner *fakeCommandRunner) (*DockerLauncher, *bytes.Buffer) {
	stderr := new(bytes.Buffer)
	docker := testDocker(runner)
	docker.Stderr = stderr
	return docker, stderr
}

// storageWarningAttempt is a launch attempt for the session container "session" whose stderr is captured.
func storageWarningAttempt(runner *fakeCommandRunner) (*launchAttempt, *bytes.Buffer) {
	docker, stderr := capturedDocker(runner)
	return &launchAttempt{docker: docker, cli: dockercli.New("docker", runner), containerName: "session"}, stderr
}

func TestConcurrentDockerStorageWarningNamesOtherRunningContainers(t *testing.T) {
	runner := &fakeCommandRunner{psOutput: "other-a\nsession\nother-b\n"}
	attempt, stderr := storageWarningAttempt(runner)
	if err := attempt.warnConcurrentDockerStorage(context.Background(), "agents-safe-docker-shared"); err != nil {
		t.Fatalf("warnConcurrentDockerStorage() error = %v", err)
	}
	want := [][]string{{"docker", "ps", "--filter", "volume=agents-safe-docker-shared", "--format", "{{.Names}}"}}
	if !reflect.DeepEqual(runner.psCalls, want) {
		t.Fatalf("ps calls = %v, want %v", runner.psCalls, want)
	}
	got := stderr.String()
	if !strings.Contains(got, "warning:") || !strings.Contains(got, `"agents-safe-docker-shared"`) ||
		!strings.Contains(got, `"other-a", "other-b"`) || strings.Contains(got, `"session"`) {
		t.Fatalf("stderr = %q, want a warning naming the volume and only the other containers", got)
	}
}

func TestConcurrentDockerStorageWarningSilentWithoutOtherContainers(t *testing.T) {
	for name, output := range map[string]string{"none": "", "own only": "session\n"} {
		t.Run(name, func(t *testing.T) {
			attempt, stderr := storageWarningAttempt(&fakeCommandRunner{psOutput: output})
			if err := attempt.warnConcurrentDockerStorage(context.Background(), "agents-safe-docker-test"); err != nil {
				t.Fatalf("warnConcurrentDockerStorage() error = %v", err)
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want no warning", stderr.String())
			}
		})
	}
}

func TestConcurrentDockerStorageListingFailureStopsLaunch(t *testing.T) {
	attempt, _ := storageWarningAttempt(&fakeCommandRunner{psError: errors.New("daemon unavailable")})
	err := attempt.warnConcurrentDockerStorage(context.Background(), "agents-safe-docker-test")
	if err == nil || !strings.Contains(err.Error(), "daemon unavailable") {
		t.Fatalf("error = %v", err)
	}
}

func TestDockerLaunchColdCreateWarnsAboutConcurrentDockerStorage(t *testing.T) {
	runner := &fakeCommandRunner{psOutput: "other-session\n", outputs: []commandResult{
		containerNotFound(),
		{output: []byte(`{"runc":{},"sysbox-runc":{}}`)},
		{output: []byte(`[]`)},
		{output: []byte(strings.Repeat("c", 64) + "\n")},
	}}
	plan := simplePlan()
	docker, stderr := capturedDocker(runner)
	if err := docker.Launch(context.Background(), plan, "image", []string{"true"}, launchplan.Options{}); err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	if len(runner.psCalls) != 1 || !containsSequence(runner.psCalls[0], "--filter", "volume="+plan.DockerStorage.Volume) {
		t.Fatalf("ps calls = %v, want one listing of %q", runner.psCalls, plan.DockerStorage.Volume)
	}
	if !strings.Contains(stderr.String(), `"other-session"`) {
		t.Fatalf("stderr = %q, want a concurrent-storage warning", stderr.String())
	}
}

func TestDockerLaunchRestartWarnsAboutConcurrentDockerStorageOfCreationMount(t *testing.T) {
	containerID := strings.Repeat("d", 64)
	plan := simplePlan()
	plan.DockerStorage.Volume = "agents-safe-docker-requested"
	stopped := stoppedInspection(containerID, false, matchingLabels(t, plan, 1000))
	mounts := `[{"Type":"volume","Name":"agents-safe-docker-shared","Destination":"` + launchplan.DockerDataRoot + `"}]`
	if err := json.Unmarshal([]byte(mounts), &stopped.Mounts); err != nil {
		t.Fatalf("unmarshal mounts: %v", err)
	}
	data := encodeInspection(t, stopped)
	runner := &fakeCommandRunner{psOutput: "other-session\n", outputs: []commandResult{
		{output: data}, {output: data}, {output: []byte("name\n")},
		{output: inspectionJSON(t, containerID, true, "running", stopped.Config.Labels)}, // post-wait force-exec warning
	}}
	docker, stderr := capturedDocker(runner)
	err := docker.Launch(context.Background(), plan, "image", []string{"true"}, launchplan.Options{ForceExec: true})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	if len(runner.psCalls) != 1 || !containsSequence(runner.psCalls[0], "--filter", "volume=agents-safe-docker-shared") {
		t.Fatalf("ps calls = %v, want one listing of the creation-time mount", runner.psCalls)
	}
	if !strings.Contains(stderr.String(), `"other-session"`) {
		t.Fatalf("stderr = %q, want a concurrent-storage warning", stderr.String())
	}
}

func TestDockerLaunchReusingRunningSessionDoesNotListStorageUsers(t *testing.T) {
	containerID := strings.Repeat("b", 64)
	plan := simplePlan()
	runner := &fakeCommandRunner{psOutput: "other-session\n", outputs: []commandResult{
		{output: inspectionJSON(t, containerID, true, "running", matchingLabels(t, plan, 1000))},
	}}
	if err := testDocker(runner).Launch(context.Background(), plan, "image", []string{"true"}, launchplan.Options{}); err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	if len(runner.psCalls) != 0 {
		t.Fatalf("ps calls = %v, want none for a running session", runner.psCalls)
	}
}
