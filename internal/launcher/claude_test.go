package launcher

import (
	"reflect"
	"testing"
)

func TestClaudeInstallationUsesOneDaemonLocalVolume(t *testing.T) {
	t.Parallel()
	if ClaudeInstallationVolume != "codex-safe-claude" {
		t.Fatalf("ClaudeInstallationVolume = %q", ClaudeInstallationVolume)
	}
	if ClaudeInstallationRoot != "/opt/codex-safe/claude" {
		t.Fatalf("ClaudeInstallationRoot = %q", ClaudeInstallationRoot)
	}
	if ClaudeBinaryPath != "/opt/codex-safe/claude/home/.local/bin/claude" {
		t.Fatalf("ClaudeBinaryPath = %q", ClaudeBinaryPath)
	}
}

func TestClaudeCommandKeepsExplicitPermissionChoice(t *testing.T) {
	t.Parallel()
	cases := map[string][]string{
		"permission mode":        {"--permission-mode", "plan"},
		"permission mode equals": {"--permission-mode=default"},
		"bypass":                 {"--dangerously-skip-permissions"},
		"allow bypass":           {"--allow-dangerously-skip-permissions"},
		"flag after prompt":      {"fix this", "--permission-mode", "plan"},
	}
	for name, arguments := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := ClaudeCommand(claudeDefaultPermissionArgs, arguments)
			want := append([]string{ClaudeBinaryPath}, arguments...)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("ClaudeCommand(%#v) = %#v, want %#v", arguments, got, want)
			}
		})
	}
}

func TestClaudeCommandSuppressesOnlyDefaultPermissionArgument(t *testing.T) {
	t.Parallel()
	configured := []string{"--permission-mode", "auto", "--model", "opus"}
	got := ClaudeCommand(configured, []string{"--permission-mode", "plan"})
	want := []string{ClaudeBinaryPath, "--model", "opus", "--permission-mode", "plan"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ClaudeCommand() = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(configured, []string{"--permission-mode", "auto", "--model", "opus"}) {
		t.Fatal("ClaudeCommand() mutated configured arguments")
	}
}

func TestClaudeCommandTreatsDirectUpdateLikeAnyForwardedCommand(t *testing.T) {
	t.Parallel()
	got := ClaudeCommand(claudeDefaultPermissionArgs, []string{"update"})
	want := []string{ClaudeBinaryPath, "--permission-mode", "auto", "update"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ClaudeCommand() = %#v, want %#v", got, want)
	}
}
