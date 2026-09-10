package dockercli

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestInspectReportsAutoRemove(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{output: []byte(`[{"Id":"0123456789abcdef","Image":"sha256:abc",` +
		`"HostConfig":{"AutoRemove":false},"Config":{"Labels":{}},"State":{"Running":false,"Status":"exited"}}]`)}
	inspection, found, err := New("docker", runner).Inspect(context.Background(), "managed-container")
	if err != nil || !found {
		t.Fatalf("Inspect() = (found %t, err %v)", found, err)
	}
	if inspection.HostConfig.AutoRemove {
		t.Fatalf("Inspect() AutoRemove = true, want false")
	}
}

func TestStartRunsDockerStart(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{output: []byte("managed-container\n")}
	if err := New("docker", runner).Start(context.Background(), "managed-container"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	want := []string{"start", "managed-container"}
	if !reflect.DeepEqual(runner.args, want) {
		t.Fatalf("Start() argv = %q, want %q", runner.args, want)
	}
}

func TestStartReportsFailure(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{output: []byte("Error response from daemon: bind source path does not exist"), err: fakeExitError{code: 1}}
	err := New("docker", runner).Start(context.Background(), "managed-container")
	if err == nil {
		t.Fatal("Start() error = nil, want failure")
	}
	if got := err.Error(); !strings.Contains(got, "start container") || !strings.Contains(got, "bind source path does not exist") {
		t.Fatalf("Start() error = %q", got)
	}
}
