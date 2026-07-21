package dockercli

import (
	"reflect"
	"testing"
)

func TestBuildCreateArgsRendersReadOnlyVolumeMount(t *testing.T) {
	t.Parallel()
	got, err := BuildCreateArgs(CreateRequest{
		Image: "image",
		Name:  "session",
		Mounts: []Mount{
			{Source: "/host/codex", Target: "/container/codex"},
			{
				Kind:     MountKindVolume,
				Source:   "codex-safe-codex-v1-1000-linux-amd64",
				Target:   "/container/codex/packages/standalone",
				ReadOnly: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("BuildCreateArgs() error = %v", err)
	}
	want := []string{
		"run", "--detach", "--rm",
		"--name", "session",
		"--mount", "type=bind,source=/host/codex,target=/container/codex,bind-propagation=rprivate",
		"--mount", "type=volume,source=codex-safe-codex-v1-1000-linux-amd64,target=/container/codex/packages/standalone,readonly",
		"image",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildCreateArgs() = %#v, want %#v", got, want)
	}
}

func TestBuildCreateArgsRejectsWritableVolumeMount(t *testing.T) {
	t.Parallel()
	_, err := BuildCreateArgs(CreateRequest{
		Image: "image",
		Name:  "session",
		Mounts: []Mount{
			{Kind: MountKindVolume, Source: "codex-store", Target: "/container/codex/packages/standalone"},
		},
	})
	if err == nil {
		t.Fatal("BuildCreateArgs() must reject a writable named-volume mount")
	}
}

func TestBuildRunAttachedArgsAllowsWritableVolumeMount(t *testing.T) {
	t.Parallel()
	got, err := BuildRunAttachedArgs(CreateRequest{
		Image: "base-image",
		Name:  "codex-safe-codex-update-v1-1000-linux-amd64",
		Mounts: []Mount{
			{Kind: MountKindVolume, Source: "codex-safe-codex-v1-1000-linux-amd64", Target: "/store"},
		},
		Command: []string{"codex-safe-session", "maintain"},
	})
	if err != nil {
		t.Fatalf("BuildRunAttachedArgs() error = %v", err)
	}
	want := []string{
		"run", "--rm",
		"--name", "codex-safe-codex-update-v1-1000-linux-amd64",
		"--mount", "type=volume,source=codex-safe-codex-v1-1000-linux-amd64,target=/store",
		"base-image",
		"codex-safe-session", "maintain",
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

func TestBuildRunAttachedArgsStillRequiresImageAndName(t *testing.T) {
	t.Parallel()
	if _, err := BuildRunAttachedArgs(CreateRequest{Name: "maintenance"}); err == nil {
		t.Error("an empty image must be rejected")
	}
	if _, err := BuildRunAttachedArgs(CreateRequest{Image: "image"}); err == nil {
		t.Error("an empty name must be rejected")
	}
}
