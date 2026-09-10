package launcher

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/dockercli"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
)

// stoppedInspectionJSON encodes an exited container whose removal policy is autoRemove.
func stoppedInspectionJSON(t *testing.T, containerID string, autoRemove bool, labels map[string]string) []byte {
	t.Helper()
	inspection := dockercli.ContainerInspection{ID: containerID, Image: "sha256:" + strings.Repeat("1", 64)}
	inspection.Config.Labels = labels
	inspection.HostConfig.AutoRemove = autoRemove
	inspection.State.Status = "exited"
	data, err := json.Marshal([]dockercli.ContainerInspection{inspection})
	if err != nil {
		t.Fatalf("marshal inspection: %v", err)
	}
	return data
}

func TestDockerLaunchRestartsStoppedPersistentContainer(t *testing.T) {
	containerID := strings.Repeat("d", 64)
	plan := simplePlan()
	runner := &fakeCommandRunner{outputs: []commandResult{
		{output: stoppedInspectionJSON(t, containerID, false, matchingLabels(t, plan, 1000))},
		{output: []byte("name\n")}, // docker start
	}}
	docker := testDocker(runner)
	stderr := new(bytes.Buffer)
	docker.Stderr = stderr
	if err := docker.Launch(context.Background(), plan, "image", []string{"make", "test"}, launchplan.Options{}); err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	containerName := mustContainerName(t, docker.HostUID, plan.ProjectRoot)
	wantCalls := [][]string{
		{"docker", "container", "inspect", containerName},
		{"docker", "start", containerName},
	}
	if !reflect.DeepEqual(runner.combinedCalls, wantCalls) {
		t.Fatalf("CombinedOutput calls = %#v, want %#v", runner.combinedCalls, wantCalls)
	}
	assertSessionReadyThenWrappedRun(t, runner.runCalls, containerID, []string{"make", "test"})
	if !strings.Contains(stderr.String(), "restarting persistent session container") ||
		!strings.Contains(stderr.String(), "docker rm "+containerName) {
		t.Fatalf("stderr = %q, want a restart notice naming the removal command", stderr.String())
	}
}

func TestDockerLaunchRejectsStoppedPersistentContainerOnFingerprintMismatch(t *testing.T) {
	plan := simplePlan()
	labels := matchingLabels(t, plan, 1000)
	labels[launchConfigLabel] = "stale"
	runner := &fakeCommandRunner{outputs: []commandResult{
		{output: stoppedInspectionJSON(t, strings.Repeat("e", 64), false, labels)},
	}}
	err := testDocker(runner).Launch(context.Background(), plan, "image", []string{"true"}, launchplan.Options{})
	if err == nil || !strings.Contains(err.Error(), "creation fingerprint") || !strings.Contains(err.Error(), "docker rm") {
		t.Fatalf("Launch() error = %v, want fingerprint mismatch with a removal hint", err)
	}
	if len(runner.combinedCalls) != 1 {
		t.Fatalf("CombinedOutput calls = %#v, want inspect only, never start", runner.combinedCalls)
	}
}

func TestDockerLaunchForceExecRestartsStoppedPersistentContainerDespiteMismatch(t *testing.T) {
	containerID := strings.Repeat("f", 64)
	plan := simplePlan()
	labels := matchingLabels(t, plan, 1000)
	labels[launchConfigLabel] = "stale"
	runner := &fakeCommandRunner{outputs: []commandResult{
		{output: stoppedInspectionJSON(t, containerID, false, labels)},
		{output: []byte("name\n")},
		{output: inspectionJSON(t, containerID, true, "running", labels)}, // post-wait force-exec warning
	}}
	err := testDocker(runner).Launch(context.Background(), plan, "image", []string{"true"}, launchplan.Options{ForceExec: true})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	assertSessionReadyThenWrappedRun(t, runner.runCalls, containerID, []string{"true"})
}

func TestDockerLaunchStillAwaitsRemovalOfStoppedAutoRemoveContainer(t *testing.T) {
	containerID := strings.Repeat("a", 64)
	plan := simplePlan()
	runner := &fakeCommandRunner{outputs: []commandResult{
		{output: stoppedInspectionJSON(t, strings.Repeat("9", 64), true, matchingLabels(t, plan, 1000))},
		containerNotFound(),
		{output: []byte(`{"runc":{},"sysbox-runc":{}}`)},
		{output: []byte(`[]`)},
		{output: []byte(containerID + "\n")},
	}}
	if err := testDocker(runner).Launch(context.Background(), plan, "image", []string{"true"}, launchplan.Options{}); err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	for _, call := range runner.combinedCalls {
		if call[1] == "start" {
			t.Fatalf("an auto-remove container must never be started, calls = %#v", runner.combinedCalls)
		}
	}
	assertColdSessionReadyThenWrappedRun(t, runner.runCalls, containerID, []string{"true"})
}

func TestDockerLaunchCreatesWithoutAutoRemoveWhenKeepContainerIsSet(t *testing.T) {
	containerID := strings.Repeat("c", 64)
	runner := &fakeCommandRunner{outputs: []commandResult{
		containerNotFound(),
		{output: []byte(`{"runc":{},"sysbox-runc":{}}`)},
		{output: []byte(`[]`)},
		{output: []byte(containerID + "\n")},
	}}
	err := testDocker(runner).Launch(
		context.Background(), simplePlan(), "image", []string{"true"}, launchplan.Options{KeepContainer: true},
	)
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	create := runner.combinedCalls[len(runner.combinedCalls)-1]
	if create[1] != "run" || containsSequence(create, "--rm") {
		t.Fatalf("create argv = %#v, want docker run without --rm", create)
	}
}

// A stopped persistent session still records its generation directory, but the departed sidecar
// removed it. Restart must recreate that directory and the sidecar before the session starts, or the
// session's bind mount has no source.
func TestPrepareHostMCPRestartRecreatesGenerationAndSidecar(t *testing.T) {
	runtimeDir, err := os.MkdirTemp("", "cs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	generation := filepath.Join(runtimeDir, "agents-safe", "key", "g-abc123")
	sessionImage := "sha256:" + strings.Repeat("2", 64)

	runner := &fakeCommandRunner{outputs: []commandResult{
		{output: []byte(strings.Repeat("b", 64) + "\n")}, // sidecar create
	}}
	attempt := attemptWith(runner)
	attempt.docker.LookupEnv = func(name string) (string, bool) {
		if name == "XDG_RUNTIME_DIR" {
			return runtimeDir, true
		}
		return "", false
	}
	attempt.hostMCP = hostMCPPlan{set: oneEndpointSet(t)}
	inspection := dockercli.ContainerInspection{ID: strings.Repeat("a", 64), Image: sessionImage}
	inspection.Config.Labels = map[string]string{hostMCPChannelLabel: generation}

	if err := attempt.prepareHostMCPRestart(context.Background(), inspection); err != nil {
		t.Fatalf("prepareHostMCPRestart() error = %v", err)
	}
	info, err := os.Stat(generation)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("generation directory = %v, %v; want a private directory recreated", info, err)
	}
	if len(runner.combinedCalls) != 1 || runner.combinedCalls[0][1] != "run" ||
		!containsSequence(runner.combinedCalls[0], "--name", "agents-safe-mcp-key-g-abc123") ||
		runner.combinedCalls[0][len(runner.combinedCalls[0])-1] != "--endpoint" && !containsSequence(runner.combinedCalls[0], sessionImage) {
		t.Fatalf("calls = %#v, want one sidecar create pinned to the session image", runner.combinedCalls)
	}
	if attempt.hostMCP.channel.Generation != generation || attempt.hostMCP.candidate {
		t.Fatalf("hostMCP = %#v, want the recorded channel adopted, not a candidate", attempt.hostMCP)
	}
}

// sessionGoneError is the exec failure Docker reports once the session shut down under the command.
type sessionGoneError struct{}

func (sessionGoneError) Error() string { return "exit failure" }
func (sessionGoneError) ExitCode() int { return 1 }
func (sessionGoneError) CommandStderr() string {
	return "Error response from daemon: container is not running"
}

// A persistent session that idles out between inspection and exec is restarted and the command
// retried, just as an auto-remove session is replaced.
func TestDockerLaunchRestartsPersistentContainerWhenExecFindsItStopped(t *testing.T) {
	containerID := strings.Repeat("a", 64)
	plan := simplePlan()
	labels := matchingLabels(t, plan, 1000)
	runner := &fakeCommandRunner{
		outputs: []commandResult{
			{output: inspectionJSON(t, containerID, true, "running", labels)},
			{output: stoppedInspectionJSON(t, containerID, false, labels)}, // after the failed exec
			{output: stoppedInspectionJSON(t, containerID, false, labels)}, // replacement acquisition
			{output: []byte("name\n")},                                     // docker start
		},
		runErrors: []error{nil, sessionGoneError{}},
	}
	if err := testDocker(runner).Launch(context.Background(), plan, "image", []string{"true"}, launchplan.Options{}); err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	if len(runner.runCalls) != 4 {
		t.Fatalf("Run calls = %#v, want readiness, failed exec, readiness, retried exec", runner.runCalls)
	}
	last := runner.combinedCalls[len(runner.combinedCalls)-1]
	if !reflect.DeepEqual(last, []string{"docker", "start", mustContainerName(t, 1000, plan.ProjectRoot)}) {
		t.Fatalf("last call = %#v, want docker start", last)
	}
}
