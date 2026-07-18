package dockercli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestBuildArgsPreservesTypedArgumentOrder(t *testing.T) {
	t.Parallel()
	request := BuildRequest{
		Dockerfile: "/project/.agents-safe/Dockerfile",
		Tag:        "codex-safe-project-key:abc",
		BaseImage:  "base image:local",
		Labels:     []KeyValue{{Key: "one", Value: "first"}, {Key: "two", Value: "second"}},
		Context:    "/project/.agents-safe",
	}
	got, err := BuildArgs(request)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"build", "--file", request.Dockerfile, "--tag", request.Tag, "--build-arg", "AGENTS_SAFE_BASE=" + request.BaseImage, "--label", "one=first", "--label", "two=second", request.Context}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildArgs() = %#v, want %#v", got, want)
	}
}

func TestBuildRoutesOutputToDiagnosticStream(t *testing.T) {
	t.Parallel()
	runner := &streamRunner{stdout: "build stdout", stderr: "build stderr"}
	diagnostics := new(bytes.Buffer)
	err := New("docker", runner).Build(context.Background(), BuildRequest{Dockerfile: "Dockerfile", Tag: "tag", BaseImage: "base", Context: ".agents-safe"}, diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	if got := diagnostics.String(); got != "build stdoutbuild stderr" {
		t.Fatalf("diagnostics = %q", got)
	}
}

func TestBuildProbeArgsAreConfined(t *testing.T) {
	t.Parallel()
	imageID := "sha256:" + strings.Repeat("a", 64)
	got, err := BuildProbeArgs(ProbeRequest{ImageID: imageID, Entrypoint: "/bin/sh", Arguments: []string{"-c", "command -v codex"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"run", "--rm", "--network=none", "--read-only", "--cap-drop=ALL", "--entrypoint", "/bin/sh", imageID, "-c", "command -v codex"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildProbeArgs() = %#v, want %#v", got, want)
	}
}

func TestInspectImageDecodesConfig(t *testing.T) {
	t.Parallel()
	id := "sha256:" + strings.Repeat("a", 64)
	runner := &fakeRunner{output: []byte(`[{"Id":"` + id + `","Architecture":"amd64","Config":{"User":"root","Entrypoint":["/usr/bin/tini","--","/usr/local/bin/codex-safe-session"],"Cmd":["serve"],"Env":["DOCKER_HOST=unix:///var/run/docker.sock"],"Labels":{"key":"value"}}}]`)}
	inspection, found, err := New("docker", runner).InspectImage(context.Background(), "tag")
	if err != nil || !found {
		t.Fatalf("InspectImage() = (%#v, %t, %v)", inspection, found, err)
	}
	if inspection.Config.User != "root" || inspection.Config.Labels["key"] != "value" {
		t.Fatalf("inspection = %#v", inspection)
	}
}

type streamRunner struct{ stdout, stderr string }

func (runner *streamRunner) CombinedOutput(context.Context, string, ...string) ([]byte, error) {
	return nil, errors.New("not used")
}
func (runner *streamRunner) Run(_ context.Context, _ string, _ []string, _ io.Reader, stdout io.Writer, stderr io.Writer) error {
	_, _ = io.WriteString(stdout, runner.stdout)
	_, _ = io.WriteString(stderr, runner.stderr)
	return nil
}
