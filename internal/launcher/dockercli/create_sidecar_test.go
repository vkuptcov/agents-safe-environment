package dockercli

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestBuildCreateArgsEncodesConfinedSidecar covers the relay sidecar's whole create contract. Every
// flag here is load-bearing: the sidecar gains the host network namespace, so losing any other
// confinement is a security regression rather than a cosmetic change.
func TestBuildCreateArgsEncodesConfinedSidecar(t *testing.T) {
	t.Parallel()
	request := CreateRequest{
		Image:          "sha256:abc",
		Name:           "codex-safe-mcp-key-generation",
		User:           "1000:1000",
		NetworkMode:    "host",
		ReadOnlyRootfs: true,
		CapDrop:        []string{"ALL"},
		SecurityOpt:    []string{"no-new-privileges"},
		Labels:         []KeyValue{{Key: "codex-safe.host-mcp-sidecar", Value: "true"}},
		Mounts:         []Mount{{Source: "/run/user/1000/codex-safe/key", Target: "/run/codex-safe-mcp"}},
		Command:        []string{"relay", "--generation", "/run/codex-safe-mcp/g-1"},
	}

	got, err := BuildCreateArgs(request)
	if err != nil {
		t.Fatalf("BuildCreateArgs() error = %v", err)
	}
	want := []string{
		"run", "--detach", "--rm",
		"--name", "codex-safe-mcp-key-generation",
		"--user", "1000:1000",
		"--network=host",
		"--read-only",
		"--cap-drop=ALL",
		"--security-opt=no-new-privileges",
		"--label", "codex-safe.host-mcp-sidecar=true",
		"--mount", "type=bind,source=/run/user/1000/codex-safe/key,target=/run/codex-safe-mcp,bind-propagation=rprivate",
		"sha256:abc",
		"relay", "--generation", "/run/codex-safe-mcp/g-1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildCreateArgs() = %#v, want %#v", got, want)
	}

	joined := strings.Join(got, " ")
	if strings.Contains(joined, "--runtime") {
		t.Errorf("the sidecar must take the Docker default runtime: %#v", got)
	}
	if strings.Contains(joined, "--workdir") {
		t.Errorf("the sidecar has no project directory to enter: %#v", got)
	}
	for _, forbidden := range []string{"--privileged", "docker.sock", "sysbox-runc"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("sidecar args contain forbidden value %q: %#v", forbidden, got)
		}
	}
}

// The command lands after the image, or Docker would read it as flags to run itself.
func TestBuildCreateArgsPlacesCommandAfterImageInOrder(t *testing.T) {
	t.Parallel()
	got, err := BuildCreateArgs(CreateRequest{
		Image:   "image",
		Name:    "container",
		Command: []string{"relay", "--endpoint", "127.0.0.1:64342", "--socket", "e0.sock"},
	})
	if err != nil {
		t.Fatalf("BuildCreateArgs() error = %v", err)
	}
	imageIndex := -1
	for index, argument := range got {
		if argument == "image" {
			imageIndex = index
			break
		}
	}
	if imageIndex == -1 {
		t.Fatalf("image is missing from %#v", got)
	}
	want := []string{"relay", "--endpoint", "127.0.0.1:64342", "--socket", "e0.sock"}
	if !reflect.DeepEqual(got[imageIndex+1:], want) {
		t.Fatalf("command after image = %#v, want %#v", got[imageIndex+1:], want)
	}
}

// An empty command preserves the image's default, which is how the session container keeps serve.
func TestBuildCreateArgsEmptyCommandPreservesImageDefault(t *testing.T) {
	t.Parallel()
	got, err := BuildCreateArgs(CreateRequest{
		Image:      "image",
		Name:       "container",
		Runtime:    "sysbox-runc",
		WorkingDir: "/project",
	})
	if err != nil {
		t.Fatalf("BuildCreateArgs() error = %v", err)
	}
	if got[len(got)-1] != "image" {
		t.Fatalf("an empty command must leave the image last: %#v", got)
	}
}

func TestBuildCreateArgsStillRequiresImageAndName(t *testing.T) {
	t.Parallel()
	if _, err := BuildCreateArgs(CreateRequest{Name: "container"}); err == nil {
		t.Error("an empty image must be rejected")
	}
	if _, err := BuildCreateArgs(CreateRequest{Image: "image"}); err == nil {
		t.Error("an empty name must be rejected")
	}
}

// recordingRunner captures argv and replays one canned outcome.
type recordingRunner struct {
	args   []string
	output []byte
	err    error
}

func (runner *recordingRunner) CombinedOutput(_ context.Context, _ string, args ...string) ([]byte, error) {
	runner.args = args
	return runner.output, runner.err
}

func (runner *recordingRunner) Run(context.Context, string, []string, io.Reader, io.Writer, io.Writer) error {
	return errors.New("not used")
}

func TestStopRequestsGracefulTermination(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{output: []byte("codex-safe-mcp-x\n")}
	client := New("docker", runner)
	if err := client.Stop(context.Background(), "codex-safe-mcp-x", 10*time.Second); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	want := []string{"stop", "--timeout", "10", "codex-safe-mcp-x"}
	if !reflect.DeepEqual(runner.args, want) {
		t.Fatalf("Stop() argv = %#v, want %#v", runner.args, want)
	}
}

// A sidecar Docker has already removed is stopped as far as the caller is concerned.
func TestStopTreatsMissingContainerAsStopped(t *testing.T) {
	t.Parallel()
	client := New("docker", &recordingRunner{
		output: []byte("Error response from daemon: No such container: gone"),
		err:    fakeExitError{code: 1},
	})
	if err := client.Stop(context.Background(), "gone", time.Second); err != nil {
		t.Fatalf("Stop() on a removed container must succeed, got %v", err)
	}
}

func TestStopSurfacesRealFailure(t *testing.T) {
	t.Parallel()
	client := New("docker", &recordingRunner{
		output: []byte("Error response from daemon: permission denied"),
		err:    fakeExitError{code: 1},
	})
	err := client.Stop(context.Background(), "sidecar", time.Second)
	if err == nil {
		t.Fatal("a real stop failure must surface")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("the diagnostic must carry Docker's output, got %v", err)
	}
}
