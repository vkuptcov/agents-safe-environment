package dockercli

import (
	"reflect"
	"strings"
	"testing"
)

func TestBuildExecArgsPreservesCommandArguments(t *testing.T) {
	t.Parallel()
	containerID := strings.Repeat("a", 64)
	request := ExecRequest{
		ContainerID: containerID,
		User:        "1000:1001",
		WorkingDir:  "/project/nested",
		Environment: []KeyValue{
			{Key: "HOME", Value: "/home/developer"},
			{Key: "CODEX_HOME", Value: "/home/developer/.codex"},
		},
		Command:     []string{"codex-safe-session", "run", "--", "printf", "value with spaces", ""},
		Interactive: true,
		AllocateTTY: true,
	}

	got, err := BuildExecArgs(request)
	if err != nil {
		t.Fatalf("BuildExecArgs() error = %v", err)
	}
	want := []string{
		"exec", "--interactive", "--tty",
		"--user", "1000:1001",
		"--env", "HOME=/home/developer",
		"--env", "CODEX_HOME=/home/developer/.codex",
		"--workdir", "/project/nested",
		containerID,
		"codex-safe-session", "run", "--", "printf", "value with spaces", "",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildExecArgs() = %#v, want %#v", got, want)
	}
}
