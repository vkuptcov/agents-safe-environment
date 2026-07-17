package dockercli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

const testImageID = "sha256:d22fbfd34f1d44af018da7923ebc5fab6c93648861630f6a5b507c3487babb00"

// scriptedRunner answers each docker invocation by its first two arguments, and records the order it
// was asked, so a test can prove a pull happened only when it should have.
type scriptedRunner struct {
	replies map[string]scriptedReply
	calls   []string
}

type scriptedReply struct {
	output []byte
	err    error
}

func (runner *scriptedRunner) CombinedOutput(_ context.Context, _ string, args ...string) ([]byte, error) {
	// Only Docker's two-word verbs take a subcommand; everything else keys on the verb alone.
	key := args[0]
	if len(args) > 1 && (args[0] == "image" || args[0] == "container") {
		key = args[0] + " " + args[1]
	}
	runner.calls = append(runner.calls, key)
	reply, found := runner.replies[key]
	if !found {
		return nil, errors.New("unexpected docker call: " + strings.Join(args, " "))
	}
	return reply.output, reply.err
}

func (runner *scriptedRunner) Run(context.Context, string, []string, io.Reader, io.Writer, io.Writer) error {
	return errors.New("not used")
}

func runtimesReply() scriptedReply {
	encoded, _ := json.Marshal(map[string]json.RawMessage{"sysbox-runc": json.RawMessage(`{}`)})
	return scriptedReply{output: encoded}
}

// A reference already in local storage must not be pulled: that is the common path on every launch.
func TestPreflightDoesNotPullAPresentImage(t *testing.T) {
	t.Parallel()
	runner := &scriptedRunner{replies: map[string]scriptedReply{
		"info":          runtimesReply(),
		"image inspect": {output: []byte("[{}]")},
	}}
	if err := New("docker", runner).Preflight(context.Background(), "sysbox-runc", "codex-safe-mvp:local"); err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	for _, call := range runner.calls {
		if call == "pull" {
			t.Fatalf("a present image must not be pulled: %#v", runner.calls)
		}
	}
}

// Before this, a reference absent from local storage failed preflight and never reached a pull, so
// resolving it to an immutable ID was impossible on a fresh host.
func TestPreflightPullsAnAbsentImageThenConfirmsIt(t *testing.T) {
	t.Parallel()
	inspects := 0
	runner := &scriptedRunner{replies: map[string]scriptedReply{
		"info": runtimesReply(),
		"pull": {output: []byte("Status: Downloaded newer image")},
	}}
	// The first inspect misses, the pull runs, and the second inspect confirms the image landed.
	runner.replies["image inspect"] = scriptedReply{}
	client := New("docker", &imageProbeRunner{inner: runner, inspects: &inspects})
	if err := client.Preflight(context.Background(), "sysbox-runc", "example.com/img:tag"); err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if inspects != 2 {
		t.Fatalf("an absent image must be inspected, pulled, then re-inspected; inspects = %d", inspects)
	}
	if !contains(runner.calls, "pull") {
		t.Fatalf("an absent image must be pulled: %#v", runner.calls)
	}
}

func TestPreflightSurfacesAPullFailure(t *testing.T) {
	t.Parallel()
	runner := &scriptedRunner{replies: map[string]scriptedReply{
		"info":          runtimesReply(),
		"image inspect": {output: []byte("No such image"), err: fakeExitError{code: 1}},
		"pull":          {output: []byte("manifest unknown"), err: fakeExitError{code: 1}},
	}}
	err := New("docker", runner).Preflight(context.Background(), "sysbox-runc", "example.com/img:missing")
	if err == nil {
		t.Fatal("a reference that cannot be pulled must fail preflight")
	}
	if !strings.Contains(err.Error(), "manifest unknown") {
		t.Fatalf("the diagnostic must name the pull failure, got %v", err)
	}
}

func TestResolveImageIDReturnsTheImmutableContentID(t *testing.T) {
	t.Parallel()
	runner := &scriptedRunner{replies: map[string]scriptedReply{
		"image inspect": {output: []byte(testImageID + "\n")},
	}}
	got, err := New("docker", runner).ResolveImageID(context.Background(), "codex-safe-mvp:local")
	if err != nil {
		t.Fatalf("ResolveImageID() error = %v", err)
	}
	if got != testImageID {
		t.Fatalf("ResolveImageID() = %q, want %q", got, testImageID)
	}
}

// A malformed value must never reach a create as if it pinned the image.
func TestResolveImageIDRejectsAMalformedID(t *testing.T) {
	t.Parallel()
	for _, malformed := range []string{"", "codex-safe-mvp:local", "sha256:short", "sha256:" + strings.Repeat("z", 64)} {
		runner := &scriptedRunner{replies: map[string]scriptedReply{
			"image inspect": {output: []byte(malformed)},
		}}
		if _, err := New("docker", runner).ResolveImageID(context.Background(), "reference"); err == nil {
			t.Errorf("ResolveImageID() accepted malformed ID %q", malformed)
		}
	}
}

// Recovery reads the running session's image rather than the tag it was launched with.
func TestInspectExposesTheContainerImageID(t *testing.T) {
	t.Parallel()
	body := `[{"Id":"aabbccddeeff001122334455","Image":"` + testImageID +
		`","Config":{"Labels":{}},"State":{"Running":true,"Status":"running"}}]`
	runner := &fakeRunner{output: []byte(body)}
	inspection, found, err := New("docker", runner).Inspect(context.Background(), "codex-safe-x")
	if err != nil || !found {
		t.Fatalf("Inspect() = (%t, %v)", found, err)
	}
	if inspection.Image != testImageID {
		t.Fatalf("Inspect().Image = %q, want %q", inspection.Image, testImageID)
	}
}

// imageProbeRunner makes the first `image inspect` miss and later ones hit, modelling a pull.
type imageProbeRunner struct {
	inner    *scriptedRunner
	inspects *int
}

func (runner *imageProbeRunner) CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	if len(args) >= 2 && args[0] == "image" && args[1] == "inspect" {
		*runner.inspects++
		runner.inner.calls = append(runner.inner.calls, "image inspect")
		if *runner.inspects == 1 {
			return []byte("Error: No such image"), fakeExitError{code: 1}
		}
		return []byte("[{}]"), nil
	}
	return runner.inner.CombinedOutput(ctx, name, args...)
}

func (runner *imageProbeRunner) Run(context.Context, string, []string, io.Reader, io.Writer, io.Writer) error {
	return errors.New("not used")
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
