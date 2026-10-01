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
)

func TestStorageVolumeCreationFailureStopsContainerCreation(t *testing.T) {
	runner := &fakeCommandRunner{volumeError: errors.New("volume unavailable")}
	docker := testDocker(runner)
	plan := simplePlan()
	plan.DockerStorageProjectRoot = "/primary/project"
	plan.DockerStorageBranch = "feature/full-branch-name"
	attempt := &launchAttempt{docker: docker, cli: dockercli.New("docker", runner), plan: plan, image: "image", containerName: "session"}
	_, _, err := attempt.createContainer(context.Background())
	if err == nil || !strings.Contains(err.Error(), "volume unavailable") {
		t.Fatalf("error = %v", err)
	}
	if len(runner.combinedCalls) != 0 {
		t.Fatalf("container created after volume failure: %v", runner.combinedCalls)
	}
	want := []string{"docker", "volume", "create", "--label", "agents-safe.docker-storage=branch", "--label", "agents-safe.git-branch=feature/full-branch-name", "--label", "agents-safe.host-uid=1000", "--label", "agents-safe.managed=true", "--label", "agents-safe.project-path=/primary/project", plan.DockerStorageVolume}
	if len(runner.volumeCalls) != 1 || !reflect.DeepEqual(runner.volumeCalls[0], want) {
		t.Fatalf("volume calls = %v", runner.volumeCalls)
	}
	attempt.plan.DockerStorage = "shared"
	labels := attempt.storageVolumeLabels()
	if _, found := labels[projectPathLabel]; found {
		t.Fatal("host-wide volume attributed to a project")
	}
	if _, found := labels[hostUIDLabel]; found {
		t.Fatal("host-wide volume attributed to a user")
	}
}

func TestStorageMismatchNamesExistingAndRequestedVolumes(t *testing.T) {
	var inspection dockercli.ContainerInspection
	if err := json.Unmarshal([]byte(`{"Mounts":[{"Type":"volume","Name":"old-volume","Destination":"/var/lib/docker"}]}`), &inspection); err != nil {
		t.Fatal(err)
	}
	attempt := &launchAttempt{plan: launchplan.Plan{DockerStorage: "branch", DockerStorageVolume: "new-volume"}}
	message := (&launchConfigMismatchError{storageDifference: attempt.storageDifference(inspection)}).Error()
	for _, text := range []string{"old-volume", "new-volume", "branch", "--docker-storage=project"} {
		if !strings.Contains(message, text) {
			t.Fatalf("missing %q in %q", text, message)
		}
	}
	attempt.plan.DockerStorageVolume = "old-volume"
	if got := attempt.storageDifference(inspection); got != "" {
		t.Fatalf("matching storage diagnosed: %s", got)
	}
}
