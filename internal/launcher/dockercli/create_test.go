package dockercli

import (
	"reflect"
	"strings"
	"testing"
)

// Docker's own `--tmpfs` defaults mark every such mount noexec, which makes native extension modules
// under a masked virtual environment unloadable. The launcher therefore spells the options out.
func TestTmpfsMountArgSpellsOutConfinementAndExecutability(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		mount TmpfsMount
		want  string
	}{
		{
			name:  "scratch mount stays non-executable",
			mount: TmpfsMount{Target: "/project/scratch", Mode: "1777"},
			want:  "/project/scratch:rw,nosuid,nodev,noexec,mode=1777",
		},
		{
			name:  "virtual environment mask is executable",
			mount: TmpfsMount{Target: "/project/.venv", Mode: "0755", Exec: true},
			want:  "/project/.venv:rw,nosuid,nodev,exec,mode=0755",
		},
		{
			name:  "mode is optional",
			mount: TmpfsMount{Target: "/project/.venv", Exec: true},
			want:  "/project/.venv:rw,nosuid,nodev,exec",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := tmpfsMountArg(test.mount); got != test.want {
				t.Fatalf("tmpfsMountArg() = %q, want %q", got, test.want)
			}
		})
	}
}

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
		"--tmpfs", "/project/.venv:rw,nosuid,nodev,noexec,mode=1777",
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

func TestBuildCreateArgsOmitsAutoRemoveForKeptContainer(t *testing.T) {
	t.Parallel()
	got, err := BuildCreateArgs(CreateRequest{
		Image: "image", Name: "managed-container", Runtime: "sysbox-runc", KeepContainer: true,
	})
	if err != nil {
		t.Fatalf("BuildCreateArgs() error = %v", err)
	}
	want := []string{"run", "--detach", "--runtime=sysbox-runc", "--name", "managed-container", "image"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildCreateArgs() = %q, want %q", got, want)
	}
}
