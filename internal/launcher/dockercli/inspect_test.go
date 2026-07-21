package dockercli

import (
	"context"
	"errors"
	"io"
	"testing"
)

func TestInspectTranslatesContainerNotFound(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{
		output: []byte("Error: No such container: managed-container"),
		err:    fakeExitError{code: 1},
	}
	inspection, found, err := New("docker", runner).Inspect(context.Background(), "managed-container")
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if found {
		t.Fatalf("Inspect() found = true, inspection = %#v", inspection)
	}
}

func TestCreateTranslatesNameConflict(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{
		output: []byte("Conflict. The container name is already in use"),
		err:    fakeExitError{code: 125},
	}
	containerID, conflict, err := New("docker", runner).Create(context.Background(), CreateRequest{
		Image:      "image",
		Name:       "managed-container",
		Runtime:    "sysbox-runc",
		WorkingDir: "/project",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !conflict || containerID != "" {
		t.Fatalf("Create() = (%q, %t), want name conflict", containerID, conflict)
	}
}

func TestCreateDoesNotMistakeUnrelatedExit125ForNameConflict(t *testing.T) {
	t.Parallel()
	// An exit-125 failure whose message is not a container-name conflict (here an invalid image
	// reference) must surface as an error, not be misreported as conflict=true with no container.
	runner := &fakeRunner{
		output: []byte("docker: invalid reference format"),
		err:    fakeExitError{code: 125},
	}
	containerID, conflict, err := New("docker", runner).Create(context.Background(), CreateRequest{
		Image:      "image",
		Name:       "managed-container",
		Runtime:    "sysbox-runc",
		WorkingDir: "/project",
	})
	if err == nil {
		t.Fatal("Create() error = nil, want an unrelated exit-125 failure to be reported")
	}
	if conflict || containerID != "" {
		t.Fatalf("Create() = (%q, %t), want no conflict and no container ID", containerID, conflict)
	}
}

func TestProcessErrorContractCarriesExitAndStderr(t *testing.T) {
	t.Parallel()
	err := &commandError{
		err:    fakeExitError{code: 37},
		stderr: "container diagnostic",
	}
	if ExitCode(err) != 37 {
		t.Fatalf("ExitCode() = %d, want 37", ExitCode(err))
	}
	var diagnostic interface{ CommandStderr() string }
	if !errors.As(err, &diagnostic) || diagnostic.CommandStderr() != "container diagnostic" {
		t.Fatalf("CommandStderr() = %q, want diagnostic", diagnostic.CommandStderr())
	}
}

type fakeRunner struct {
	output []byte
	err    error
}

func (runner *fakeRunner) CombinedOutput(
	_ context.Context,
	_ string,
	_ ...string,
) ([]byte, error) {
	return runner.output, runner.err
}

func (runner *fakeRunner) Run(
	_ context.Context,
	_ string,
	_ []string,
	_ io.Reader,
	_ io.Writer,
	_ io.Writer,
) error {
	return runner.err
}

type fakeExitError struct {
	code int
}

func (err fakeExitError) Error() string {
	return "exit failure"
}

func (err fakeExitError) ExitCode() int {
	return err.code
}
