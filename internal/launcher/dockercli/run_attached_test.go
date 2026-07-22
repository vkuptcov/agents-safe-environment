package dockercli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"testing"
)

type attachedRunner struct {
	args           []string
	stdout, stderr string
	err            error
}

func (runner *attachedRunner) CombinedOutput(context.Context, string, ...string) ([]byte, error) {
	return nil, errors.New("not used")
}

func (runner *attachedRunner) Run(
	_ context.Context,
	_ string,
	args []string,
	_ io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) error {
	runner.args = append([]string(nil), args...)
	_, _ = io.WriteString(stdout, runner.stdout)
	_, _ = io.WriteString(stderr, runner.stderr)
	return runner.err
}

func TestRunAttachedStreamsOutput(t *testing.T) {
	t.Parallel()
	runner := &attachedRunner{stdout: "out", stderr: "err"}
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	request := CreateRequest{Image: "image", Name: "maintenance"}
	if err := New("docker", runner).RunAttached(context.Background(), request, nil, stdout, stderr); err != nil {
		t.Fatalf("RunAttached() error = %v", err)
	}
	if stdout.String() != "out" || stderr.String() != "err" {
		t.Fatalf("streams = (%q, %q)", stdout.String(), stderr.String())
	}
	want := []string{"run", "--rm", "--name", "maintenance", "image"}
	if !reflect.DeepEqual(runner.args, want) {
		t.Fatalf("argv = %#v, want %#v", runner.args, want)
	}
}

func TestRunAttachedPreservesExitCode(t *testing.T) {
	t.Parallel()
	runner := &attachedRunner{err: fakeExitError{code: 23}}
	err := New("docker", runner).RunAttached(
		context.Background(), CreateRequest{Image: "image", Name: "maintenance"}, nil, io.Discard, io.Discard,
	)
	if err == nil || ExitCode(err) != 23 {
		t.Fatalf("RunAttached() error = %v, exit = %d", err, ExitCode(err))
	}
}
