package launcher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/dockercli"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/hostmcp"
)

// attemptWith builds a launch attempt whose Docker calls are served by a scripted runner.
func attemptWith(runner *fakeCommandRunner) *launchAttempt {
	return &launchAttempt{
		docker:        testDocker(runner),
		cli:           dockercli.New("docker", runner),
		plan:          testPlan(),
		containerName: "codex-safe-session",
		projectKey:    "key",
	}
}

func runningInspection(t *testing.T) commandResult {
	return commandResult{output: inspectionJSON(t, strings.Repeat("a", 64), true, "running", map[string]string{})}
}

func createdInspection(t *testing.T) commandResult {
	return commandResult{output: inspectionJSON(t, strings.Repeat("b", 64), false, "created", map[string]string{})}
}

// F-004: a sidecar that becomes running while the launcher waits for its name is adopted, not
// retried into a false conflict.
func TestAwaitSidecarNameAdoptsARunningTransition(t *testing.T) {
	t.Parallel()
	runner := &fakeCommandRunner{outputs: []commandResult{
		createdInspection(t), // first inspect: still Created
		runningInspection(t), // second inspect: now Running
	}}
	attempt := attemptWith(runner)

	outcome, err := attempt.awaitSidecarName(context.Background(), "codex-safe-mcp-key-g", true)
	if err != nil {
		t.Fatalf("awaitSidecarName() error = %v", err)
	}
	if outcome != sidecarBecameRunning {
		t.Fatalf("a running transition must be reported as adoption, got %v", outcome)
	}
}

// After a Stop, a transient running observation must not be mistaken for adoption: the wait
// continues until the name is released.
func TestAwaitSidecarNameWaitsForReleaseWhenNotAdopting(t *testing.T) {
	t.Parallel()
	runner := &fakeCommandRunner{outputs: []commandResult{
		runningInspection(t), // still running (ignored)
		{output: []byte("Error: No such container"), err: fakeExitError{code: 1}}, // now gone
	}}
	attempt := attemptWith(runner)

	outcome, err := attempt.awaitSidecarName(context.Background(), "codex-safe-mcp-key-g", false)
	if err != nil {
		t.Fatalf("awaitSidecarName() error = %v", err)
	}
	if outcome != sidecarNameReleased {
		t.Fatalf("without adoption, only a released name may succeed, got %v", outcome)
	}
}

// F-001: on a post-create failure the session is stopped before its generation is removed, so a
// still-running session never has its bind-mounted channel unlinked from under it.
func TestCleanupCandidateStopsSessionBeforeRemovingGeneration(t *testing.T) {
	t.Parallel()
	generationDir := t.TempDir()
	runner := &fakeCommandRunner{outputs: []commandResult{
		{output: []byte("codex-safe-session\n")},                                  // stop session
		{output: []byte("codex-safe-mcp-key-g\n")},                                // stop sidecar
		{output: []byte("Error: No such container"), err: fakeExitError{code: 1}}, // await sidecar release
	}}
	attempt := attemptWith(runner)
	attempt.hostMCP = hostMCPPlan{
		set:            oneEndpointSet(t),
		channel:        hostmcp.Channel{Generation: generationDir, Name: "g"},
		candidate:      true,
		sidecarStarted: true,
		sessionID:      strings.Repeat("a", 64),
	}

	if err := attempt.cleanupCandidate(context.Background()); err != nil {
		t.Fatalf("cleanupCandidate() error = %v", err)
	}
	// The session stop is the first Docker call, before any removal.
	if len(runner.combinedCalls) == 0 || runner.combinedCalls[0][1] != "stop" ||
		runner.combinedCalls[0][len(runner.combinedCalls[0])-1] != "codex-safe-session" {
		t.Fatalf("the session must be stopped first, calls = %v", runner.combinedCalls)
	}
	if _, err := os.Stat(generationDir); !os.IsNotExist(err) {
		t.Fatalf("the generation must be removed after both stops succeed, stat err = %v", err)
	}
}

// If a stop fails, the generation is preserved: a container that could not be stopped keeps its
// channel rather than having it unlinked from under it.
func TestCleanupCandidatePreservesGenerationWhenAStopFails(t *testing.T) {
	t.Parallel()
	generationDir := t.TempDir()
	marker := filepath.Join(generationDir, "keep")
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatalf("marker: %v", err)
	}
	runner := &fakeCommandRunner{outputs: []commandResult{
		{output: []byte("boom: permission denied"), err: fakeExitError{code: 1}}, // stop session FAILS
	}}
	attempt := attemptWith(runner)
	attempt.hostMCP = hostMCPPlan{
		set:       oneEndpointSet(t),
		channel:   hostmcp.Channel{Generation: generationDir, Name: "g"},
		candidate: true,
		sessionID: strings.Repeat("a", 64),
	}

	if err := attempt.cleanupCandidate(context.Background()); err == nil {
		t.Fatal("a failed stop must surface an error")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the generation must be preserved when a stop fails, stat err = %v", err)
	}
}

// A candidate that was adopted (candidate == false) is never touched by cleanup: its resources
// belong to the running session it was adopted from.
func TestCleanupCandidateSkipsAnAdoptedChannel(t *testing.T) {
	t.Parallel()
	runner := &fakeCommandRunner{} // no Docker calls expected
	attempt := attemptWith(runner)
	attempt.hostMCP = hostMCPPlan{set: oneEndpointSet(t), candidate: false}

	if err := attempt.cleanupCandidate(context.Background()); err != nil {
		t.Fatalf("cleanupCandidate() on an adopted channel must be a no-op, got %v", err)
	}
	if len(runner.combinedCalls) != 0 {
		t.Fatalf("an adopted channel must trigger no Docker calls, got %v", runner.combinedCalls)
	}
}
