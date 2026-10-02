package launcher

import (
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
