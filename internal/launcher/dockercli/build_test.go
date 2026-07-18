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
		Tag:       "codex-safe-project-key:abc",
		BaseImage: "base image:local",
		Context:   "/project/.agents-safe",
	}
	got, err := BuildArgs(request)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"build", "--tag", request.Tag, "--build-arg", "AGENTS_SAFE_BASE=" + request.BaseImage, request.Context}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildArgs() = %#v, want %#v", got, want)
	}
}

func TestBuildRoutesOutputToDiagnosticStream(t *testing.T) {
	t.Parallel()
	runner := &streamRunner{stdout: "build stdout", stderr: "build stderr"}
	diagnostics := new(bytes.Buffer)
	err := New("docker", runner).Build(context.Background(), BuildRequest{Tag: "tag", BaseImage: "base", Context: ".agents-safe"}, diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	if got := diagnostics.String(); got != "build stdoutbuild stderr" {
		t.Fatalf("diagnostics = %q", got)
	}
}

func TestInspectImageDecodesConfig(t *testing.T) {
	t.Parallel()
	id := "sha256:" + strings.Repeat("a", 64)
	runner := &fakeRunner{output: []byte(`[{"Id":"` + id + `","Architecture":"amd64","Config":{"User":"root","Entrypoint":["/usr/bin/tini","--","/usr/local/bin/codex-safe-session"],"Cmd":["serve"],"Env":["DOCKER_HOST=unix:///var/run/docker.sock"]}}]`)}
	inspection, err := New("docker", runner).InspectImage(context.Background(), "tag")
	if err != nil {
		t.Fatalf("InspectImage() = (%#v, %v)", inspection, err)
	}
	if inspection.Config.User != "root" {
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
