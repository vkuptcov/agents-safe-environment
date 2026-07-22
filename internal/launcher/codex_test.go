package launcher

import (
	"reflect"
	"testing"
)

func TestCodexBinaryPathIsImageOwnedAbsolute(t *testing.T) {
	t.Parallel()
	if CodexBinaryPath != "/usr/local/bin/codex" {
		t.Fatalf("CodexBinaryPath = %q, want the image-owned absolute path", CodexBinaryPath)
	}
}

func TestCodexInstallationUsesOneDaemonLocalVolume(t *testing.T) {
	t.Parallel()
	if CodexInstallationVolume != "codex-safe-codex" {
		t.Fatalf("CodexInstallationVolume = %q", CodexInstallationVolume)
	}
	if CodexInstallationRoot != "/opt/codex-safe/codex" {
		t.Fatalf("CodexInstallationRoot = %q", CodexInstallationRoot)
	}
}

func TestCodexCommandKeepsExplicitSandboxChoice(t *testing.T) {
	t.Parallel()
	cases := map[string][]string{
		"short flag":        {"-s", "read-only"},
		"long flag":         {"--sandbox", "workspace-write"},
		"short equals":      {"-s=read-only"},
		"long equals":       {"--sandbox=workspace-write"},
		"full auto":         {"--full-auto"},
		"bypass sandbox":    {"--dangerously-bypass-approvals-and-sandbox"},
		"flag after subcmd": {"exec", "--sandbox", "read-only"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := CodexCommand(codexDefaultSandboxArgs, args)
			want := append([]string{CodexBinaryPath}, args...)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("CodexCommand(%#v) = %#v, want %#v", args, got, want)
			}
		})
	}
}

func TestCodexCommandRetainsConfiguredArgumentsAndSuppressesOnlyDefaultSandbox(t *testing.T) {
	t.Parallel()
	configured := []string{"--sandbox", "danger-full-access", "--model", "gpt-5"}
	got := CodexCommand(configured, []string{"exec", "--sandbox", "read-only"})
	want := []string{CodexBinaryPath, "--model", "gpt-5", "exec", "--sandbox", "read-only"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CodexCommand() = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(configured, []string{"--sandbox", "danger-full-access", "--model", "gpt-5"}) {
		t.Fatal("CodexCommand() mutated configured arguments")
	}
}

func TestCodexCommandKeepsCustomConfiguredSandbox(t *testing.T) {
	t.Parallel()
	got := CodexCommand([]string{"--sandbox", "workspace-write"}, []string{"--sandbox", "read-only"})
	want := []string{CodexBinaryPath, "--sandbox", "workspace-write", "--sandbox", "read-only"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CodexCommand() = %#v, want %#v", got, want)
	}
}

func TestCodexCommandKeepsDirectUpdateLeadingForDispatcher(t *testing.T) {
	t.Parallel()
	got := CodexCommand([]string{"--sandbox", "danger-full-access", "--model", "gpt-5"}, []string{"update"})
	want := []string{CodexBinaryPath, "update"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CodexCommand() = %#v, want %#v", got, want)
	}
}
