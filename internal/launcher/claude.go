package launcher

import "strings"

const (
	// ClaudeInstallationVolume is the daemon-local volume shared by every managed session.
	ClaudeInstallationVolume = "codex-safe-claude"
	// ClaudeInstallationRoot is the fixed path where sessions mount the volume read-only.
	ClaudeInstallationRoot = "/opt/codex-safe/claude"
	// ClaudeBinaryPath is the native-installer launcher used by claude-safe.
	ClaudeBinaryPath = ClaudeInstallationRoot + "/home/.local/bin/claude"
)

// claudeDefaultPermissionArgs let Claude Code operate freely inside the outer Sysbox boundary.
// Host access remains limited to the launcher's explicit mount plan.
var claudeDefaultPermissionArgs = []string{"--dangerously-skip-permissions"}

// ClaudeCommand combines project-configured and invocation arguments. An explicit invocation
// permission mode removes only the configured bypass default; every other configured argument
// remains in order.
func ClaudeCommand(configuredArgs, invocationArgs []string) []string {
	configured := append([]string(nil), configuredArgs...)
	if claudeArgsSelectPermissionMode(invocationArgs) {
		configured = withoutDefaultClaudePermission(configured)
	}
	command := make([]string, 0, len(configured)+len(invocationArgs)+1)
	command = append(command, ClaudeBinaryPath)
	command = append(command, configured...)
	command = append(command, invocationArgs...)
	return command
}

func withoutDefaultClaudePermission(arguments []string) []string {
	result := make([]string, 0, len(arguments))
	for _, argument := range arguments {
		if argument == claudeDefaultPermissionArgs[0] {
			continue
		}
		result = append(result, argument)
	}
	return result
}

func claudeArgsSelectPermissionMode(arguments []string) bool {
	for _, argument := range arguments {
		switch {
		case argument == "--permission-mode":
			return true
		case strings.HasPrefix(argument, "--permission-mode="):
			return true
		case argument == "--dangerously-skip-permissions":
			return true
		case argument == "--allow-dangerously-skip-permissions":
			return true
		}
	}
	return false
}
