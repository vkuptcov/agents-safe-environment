package smoke_test

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/require"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher"
)

const (
	cleanupTimeout     = 15 * time.Second
	idleRemovalTimeout = 20 * time.Second
)

type containerNames struct {
	outer    string
	sentinel string
	nested   string
	compose  string
}

type dockerHarness struct {
	t        *testing.T
	ctx      context.Context
	cancel   context.CancelFunc
	client   *client.Client
	project  string
	daemonID string
	names    containerNames
}

func newDockerHarness(t *testing.T, project projectLayout) *dockerHarness {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), smokeTimeout)
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err, "Moby client must initialize from the Docker environment")
	info, err := dockerClient.Info(ctx)
	require.NoError(t, err, "Moby client must inspect the host Docker daemon")
	outer := launcher.ProjectContainerName(os.Getuid(), project.worktree)
	projectKey := launcher.ProjectKey(os.Getuid(), project.worktree)
	return &dockerHarness{
		t:        t,
		ctx:      ctx,
		cancel:   cancel,
		client:   dockerClient,
		project:  project.worktree,
		daemonID: info.ID,
		names: containerNames{
			outer:    outer,
			sentinel: "codex-safe-host-sentinel-" + projectKey,
			nested:   "codex-safe-nested-" + projectKey,
			compose:  "codex-safe-compose-" + projectKey,
		},
	}
}

func (docker *dockerHarness) close() {
	docker.cancel()
	cleanupContext, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	_ = docker.client.ContainerRemove(cleanupContext, docker.names.outer, container.RemoveOptions{Force: true})
	_ = docker.client.ContainerRemove(cleanupContext, docker.names.sentinel, container.RemoveOptions{Force: true})
	_ = docker.client.Close()
}

func (docker *dockerHarness) startSentinel() {
	docker.t.Helper()
	created, err := docker.client.ContainerCreate(docker.ctx, &container.Config{
		Image:      goSmokeImage,
		Entrypoint: []string{"/bin/sleep"},
		Cmd:        []string{"300"},
		Labels:     map[string]string{"codex-safe.smoke": "go"},
	}, nil, nil, nil, docker.names.sentinel)
	require.NoError(docker.t, err, "host sentinel container must be created")
	require.NoError(docker.t, docker.client.ContainerStart(docker.ctx, created.ID, container.StartOptions{}), "host sentinel container must start")
}

func (docker *dockerHarness) inspectOuter() container.InspectResponse {
	docker.t.Helper()
	inspection, err := docker.client.ContainerInspect(docker.ctx, docker.names.outer)
	require.NoError(docker.t, err, "Moby client must inspect deterministic container %q", docker.names.outer)
	return inspection
}

func (docker *dockerHarness) managedContainers() []container.Summary {
	docker.t.Helper()
	items, err := docker.client.ContainerList(docker.ctx, container.ListOptions{Filters: filters.NewArgs(
		filters.Arg("label", "codex-safe.managed=true"),
		filters.Arg("label", "codex-safe.project-path="+docker.project),
		filters.Arg("label", "codex-safe.host-uid="+strconv.Itoa(os.Getuid())),
		filters.Arg("name", "^"+docker.names.outer+"$"),
	)})
	require.NoError(docker.t, err, "Moby client must list active managed containers")
	return items
}

func (docker *dockerHarness) containersNamed(name string) []container.Summary {
	docker.t.Helper()
	items, err := docker.client.ContainerList(docker.ctx, container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("name", "^"+name+"$")),
	})
	require.NoError(docker.t, err, "Moby client must list containers named %q", name)
	return items
}

func (docker *dockerHarness) waitForOuter() {
	docker.t.Helper()
	deadline := time.Now().Add(commandTimeout)
	for time.Now().Before(deadline) {
		if len(docker.managedContainers()) == 1 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	docker.t.Fatal("timed out waiting for the deterministic outer container")
}

func (docker *dockerHarness) waitForOuterRemoval() {
	docker.t.Helper()
	deadline := time.Now().Add(idleRemovalTimeout)
	for time.Now().Before(deadline) {
		_, err := docker.client.ContainerInspect(docker.ctx, docker.names.outer)
		if client.IsErrNotFound(err) {
			return
		}
		require.NoError(docker.t, err, "Moby client must inspect the session while waiting for idle removal")
		time.Sleep(250 * time.Millisecond)
	}
	docker.t.Fatal("deterministic outer container was not removed after idle timeout")
}
