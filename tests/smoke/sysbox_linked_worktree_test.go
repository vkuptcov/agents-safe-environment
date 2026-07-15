package smoke_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/require"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher"
)

const (
	goSmokeEnv   = "CODEX_SAFE_RUN_SYSBOX_SMOKE"
	goSmokeImage = "codex-safe-mvp:local"
)

// TestSysboxLinkedWorktreeGo compares the essential shared-session lifecycle
// with the Bash harness while using the Moby client for host Docker queries.
// It is opt-in because it requires a real Sysbox runtime and an already-built image.
func TestSysboxLinkedWorktreeGo(t *testing.T) {
	if os.Getenv(goSmokeEnv) != "1" {
		t.Skipf("set %s=1 to run the real Sysbox smoke test", goSmokeEnv)
	}
	workingDirectory, err := os.Getwd()
	require.NoError(t, err, "smoke working directory must be available")
	binaryPath := filepath.Join(workingDirectory, "..", "..", "bin", "codex-safe")
	if _, err := os.Stat(binaryPath); err != nil {
		t.Skip("bin/codex-safe is missing; run make build first")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err, "Moby client must initialize from the Docker environment")
	defer dockerClient.Close()

	project := filepath.Join(t.TempDir(), "linked worktree")
	initGitProject(t, project)
	containerName, err := launcher.ProjectContainerName(os.Getuid(), project)
	require.NoError(t, err, "deterministic project container name must be derivable")

	ready := filepath.Join(project, "first.ready")
	releaseFirst := filepath.Join(project, "first.release")
	first := startLauncherCommand(t, binaryPath, project, "bash", "-c", "printf ready >\"$1\"; while [[ ! -e \"$2\" ]]; do sleep 1; done", "bash", ready, releaseFirst)
	waitForPath(t, ready)

	firstInspection := inspectContainer(t, ctx, dockerClient, containerName)
	managed := listManagedContainers(t, ctx, dockerClient, containerName)
	require.Len(t, managed, 1, "Moby client must find exactly one managed outer container")
	require.True(t, firstInspection.State.Running, "outer container must run while the first command is active")
	require.Equal(t, "true", firstInspection.Config.Labels["codex-safe.managed"], "managed label must identify the session")
	require.Equal(t, project, firstInspection.Config.Labels["codex-safe.project-path"], "project label must match the root")

	secondReady := filepath.Join(project, "second.ready")
	releaseSecond := filepath.Join(project, "second.release")
	second := startLauncherCommand(t, binaryPath, project, "bash", "-c", "printf ready >\"$1\"; while [[ ! -e \"$2\" ]]; do sleep 1; done", "bash", secondReady, releaseSecond)
	waitForPath(t, secondReady)

	require.NoError(t, os.WriteFile(releaseFirst, nil, 0o600), "first command release marker must be writable")
	require.NoError(t, first.Wait(), "first wrapped command must exit cleanly")
	require.Nil(t, second.ProcessState, "second wrapped command must survive first command exit")
	require.True(t, inspectContainer(t, ctx, dockerClient, containerName).State.Running, "outer session must remain running")

	require.NoError(t, os.WriteFile(releaseSecond, nil, 0o600), "second command release marker must be writable")
	require.NoError(t, second.Wait(), "second wrapped command must exit cleanly")
	require.True(t, inspectContainer(t, ctx, dockerClient, containerName).State.Running, "idle grace period must retain the container")
	waitForContainerRemoval(t, ctx, dockerClient, containerName)
}

func initGitProject(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(path, 0o755), "project directory must be created")
	runInDir(t, path, "git", "init", "-q")
	runInDir(t, path, "git", "config", "user.name", "Codex Safe Smoke")
	runInDir(t, path, "git", "config", "user.email", "codex-safe@example.invalid")
	require.NoError(t, os.WriteFile(filepath.Join(path, "baseline.txt"), []byte("baseline\n"), 0o600), "baseline must be written")
	runInDir(t, path, "git", "add", "baseline.txt")
	runInDir(t, path, "git", "commit", "-qm", "baseline")
}

func startLauncherCommand(t *testing.T, binaryPath, project string, command ...string) *exec.Cmd {
	t.Helper()
	args := append([]string{"--project", project, "--image", goSmokeImage, "--"}, command...)
	process := exec.Command(binaryPath, args...)
	process.Stdout = new(bytes.Buffer)
	process.Stderr = new(bytes.Buffer)
	require.NoError(t, process.Start(), "codex-safe command must start")
	t.Cleanup(func() {
		if process.Process != nil && processAlive(process) {
			_ = process.Process.Kill()
		}
	})
	return process
}

func inspectContainer(t *testing.T, ctx context.Context, dockerClient *client.Client, name string) container.InspectResponse {
	t.Helper()
	inspection, err := dockerClient.ContainerInspect(ctx, name)
	require.NoError(t, err, "Moby client must inspect deterministic container %q", name)
	return inspection
}

func listManagedContainers(t *testing.T, ctx context.Context, dockerClient *client.Client, name string) []container.Summary {
	t.Helper()
	items, err := dockerClient.ContainerList(ctx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", "codex-safe.managed=true"),
			filters.Arg("name", "^"+name+"$"),
		),
	})
	require.NoError(t, err, "Moby client must list managed containers")
	return items
}

func waitForContainerRemoval(t *testing.T, ctx context.Context, dockerClient *client.Client, name string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		_, err := dockerClient.ContainerInspect(ctx, name)
		if client.IsErrNotFound(err) {
			return
		}
		require.NoError(t, err, "Moby client must inspect the session while waiting for idle removal")
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatal("deterministic outer container was not removed after idle timeout")
}

func waitForPath(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func processAlive(process *exec.Cmd) bool {
	return process != nil && process.Process != nil && process.ProcessState == nil
}

func runInDir(t *testing.T, directory, name string, args ...string) {
	t.Helper()
	command := exec.Command(name, args...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s %s must succeed: %s", name, strings.Join(args, " "), output)
}
