package dockercli

import (
	"reflect"
	"testing"
)

func TestBuildCreateArgsRendersReadOnlyVolumeMount(t *testing.T) {
	t.Parallel()
	got, err := BuildCreateArgs(CreateRequest{
		Image:  "image",
		Name:   "session",
		Mounts: []Mount{{Source: "/host/codex", Target: "/container/codex"}},
		Volumes: []VolumeMount{{
			Source:   "codex-safe-codex",
			Target:   "/opt/codex-safe/codex",
			ReadOnly: true,
		}},
	})
	if err != nil {
		t.Fatalf("BuildCreateArgs() error = %v", err)
	}
	want := []string{
		"run", "--detach", "--rm",
		"--name", "session",
		"--mount", "type=bind,source=/host/codex,target=/container/codex,bind-propagation=rprivate",
		"--mount", "type=volume,source=codex-safe-codex,target=/opt/codex-safe/codex,readonly",
		"image",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildCreateArgs() = %#v, want %#v", got, want)
	}
}

func TestBuildRunAttachedArgsAllowsWritableVolumeMount(t *testing.T) {
	t.Parallel()
	got, err := BuildRunAttachedArgs(CreateRequest{
		Image:      "base-image",
		Entrypoint: "/usr/local/bin/codex-safe-update",
		Volumes:    []VolumeMount{{Source: "codex-safe-codex", Target: "/opt/codex-safe/codex"}},
	})
	if err != nil {
		t.Fatalf("BuildRunAttachedArgs() error = %v", err)
	}
	want := []string{
		"run", "--rm",
		"--mount", "type=volume,source=codex-safe-codex,target=/opt/codex-safe/codex",
		"--entrypoint", "/usr/local/bin/codex-safe-update",
		"base-image",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildRunAttachedArgs() = %#v, want %#v", got, want)
	}
}

func TestBuildRunAttachedArgsOmitsDetach(t *testing.T) {
	t.Parallel()
	got, err := BuildRunAttachedArgs(CreateRequest{Image: "image", Name: "maintenance"})
	if err != nil {
		t.Fatalf("BuildRunAttachedArgs() error = %v", err)
	}
	for _, argument := range got {
		if argument == "--detach" {
			t.Fatalf("an attached run must never carry --detach: %#v", got)
		}
	}
}

func TestBuildRunAttachedArgsRequiresOnlyImage(t *testing.T) {
	t.Parallel()
	if _, err := BuildRunAttachedArgs(CreateRequest{Name: "maintenance"}); err == nil {
		t.Error("an empty image must be rejected")
	}
	if got, err := BuildRunAttachedArgs(CreateRequest{Image: "image"}); err != nil {
		t.Fatalf("an anonymous attached run must be accepted: %v", err)
	} else if want := []string{"run", "--rm", "image"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildRunAttachedArgs() = %#v, want %#v", got, want)
	}
}
