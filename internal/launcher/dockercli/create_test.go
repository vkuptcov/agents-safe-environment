package dockercli

import (
	"reflect"
	"strings"
	"testing"
)

func TestBuildCreateArgsPreservesOrderedInputs(t *testing.T) {
	t.Parallel()
	request := CreateRequest{
		Image:      "image",
		Name:       "managed-container",
		Runtime:    "sysbox-runc",
		WorkingDir: "/project/nested",
		Labels: []KeyValue{
			{Key: "managed", Value: "true"},
			{Key: "project", Value: "/project"},
		},
		Environment: []KeyValue{
			{Key: "HOST_UID", Value: "1000"},
			{Key: "HOST_HOME", Value: "/home/developer"},
		},
		Mounts: []Mount{
			{Source: "/primary", Target: "/primary", ReadOnly: true},
			{Source: "/project", Target: "/project"},
		},
		Tmpfs: []TmpfsMount{{Target: "/project/.venv", Mode: "1777"}},
	}

	got, err := BuildCreateArgs(request)
	if err != nil {
		t.Fatalf("BuildCreateArgs() error = %v", err)
	}
	want := []string{
		"run", "--detach", "--rm",
		"--runtime=sysbox-runc",
		"--name", "managed-container",
		"--label", "managed=true",
		"--label", "project=/project",
		"--env", "HOST_UID=1000",
		"--env", "HOST_HOME=/home/developer",
		"--workdir", "/project/nested",
		"--mount", "type=bind,source=/primary,target=/primary,bind-propagation=rprivate,readonly",
		"--mount", "type=bind,source=/project,target=/project,bind-propagation=rprivate",
		"--tmpfs", "/project/.venv:mode=1777",
		"image",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildCreateArgs() = %#v, want %#v", got, want)
	}
	for _, forbidden := range []string{"--privileged", "--network=host", "/var/run/docker.sock"} {
		if strings.Contains(strings.Join(got, " "), forbidden) {
			t.Errorf("create args contain forbidden value %q: %#v", forbidden, got)
		}
	}
}
