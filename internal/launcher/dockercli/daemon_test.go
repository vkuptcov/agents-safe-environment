package dockercli

import (
	"context"
	"testing"
)

func TestInspectDaemonDecodesOSAndArchitecture(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{output: []byte(`{"Os":"linux","Arch":"arm64"}`)}
	inspection, err := New("docker", runner).InspectDaemon(context.Background())
	if err != nil {
		t.Fatalf("InspectDaemon() error = %v", err)
	}
	if inspection.OS != "linux" || inspection.Architecture != "arm64" {
		t.Fatalf("InspectDaemon() = %#v", inspection)
	}
}

// A non-Linux OS or unsupported architecture is a valid negative that InspectDaemon must still
// decode successfully; rejecting it is codexinstall.ParseTarget's job, not the transport's.
func TestInspectDaemonDecodesUnsupportedPlatformWithoutError(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{output: []byte(`{"Os":"windows","Arch":"amd64"}`)}
	inspection, err := New("docker", runner).InspectDaemon(context.Background())
	if err != nil {
		t.Fatalf("InspectDaemon() error = %v", err)
	}
	if inspection.OS != "windows" {
		t.Fatalf("InspectDaemon() = %#v, want a decoded (not rejected) windows OS", inspection)
	}
}

func TestInspectDaemonRejectsMalformedJSON(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{output: []byte("not json")}
	if _, err := New("docker", runner).InspectDaemon(context.Background()); err == nil {
		t.Fatal("InspectDaemon() must reject malformed JSON")
	}
}

func TestInspectDaemonRejectsEmptyFields(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{output: []byte(`{"Os":"","Arch":""}`)}
	if _, err := New("docker", runner).InspectDaemon(context.Background()); err == nil {
		t.Fatal("InspectDaemon() must reject an empty OS/architecture response")
	}
}

func TestInspectDaemonSurfacesTransportFailure(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{output: []byte("Cannot connect to the Docker daemon"), err: fakeExitError{code: 1}}
	if _, err := New("docker", runner).InspectDaemon(context.Background()); err == nil {
		t.Fatal("InspectDaemon() must surface a transport failure distinctly from a decoded negative")
	}
}
