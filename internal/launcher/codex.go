package launcher

import "strings"

const (
	// CodexInstallationVolume is the daemon-local volume shared by every managed session.
	CodexInstallationVolume = "agents-safe-codex"
	// CodexInstallationRoot is the fixed path where sessions mount the volume read-only.
	CodexInstallationRoot = "/opt/agents-safe/codex"
	// CodexBinaryPath is the installer-created executable used by the product launcher.
	CodexBinaryPath = CodexInstallationRoot + "/bin/codex"
)

// codexDefaultSandboxArgs disable Codex's own inner sandbox. The Sysbox container is already the
// isolation boundary, so Codex's bubblewrap-based sandbox is redundant, and the image ships no
// bubblewrap on PATH. Without this default Codex prints "could not find bubblewrap" and falls back
// to a bundled copy; selecting danger-full-access keeps Codex fully functional inside the container
// without weakening the outer boundary.
var codexDefaultSandboxArgs = []string{"--sandbox", "danger-full-access"}

// CodexCommand combines project-configured and invocation arguments. An explicit invocation sandbox selection removes
// only the configured default sandbox pair; every other configured argument remains in order.
func CodexCommand(configuredArgs, invocationArgs []string) []string {
	configured := append([]string(nil), configuredArgs...)
	if codexArgsSelectSandbox(invocationArgs) {
		configured = withoutDefaultSandbox(configured)
	}
	command := make([]string, 0, len(configured)+len(invocationArgs)+1)
	command = append(command, CodexBinaryPath)
	command = append(command, configured...)
	command = append(command, invocationArgs...)
	return command
}

func withoutDefaultSandbox(arguments []string) []string {
	result := make([]string, 0, len(arguments))
	for index := 0; index < len(arguments); index++ {
		if index+1 < len(arguments) && arguments[index] == codexDefaultSandboxArgs[0] &&
			arguments[index+1] == codexDefaultSandboxArgs[1] {
			index++
			continue
		}
		result = append(result, arguments[index])
	}
	return result
}

// codexArgsSelectSandbox reports whether the forwarded arguments already choose a Codex sandbox
// policy, either through the sandbox flag or through a flag that bypasses the sandbox entirely.
// When they do, the launcher forwards the user's choice untouched.
func codexArgsSelectSandbox(codexArgs []string) bool {
	for _, arg := range codexArgs {
		switch {
		case arg == "-s" || arg == "--sandbox":
			return true
		case strings.HasPrefix(arg, "-s=") || strings.HasPrefix(arg, "--sandbox="):
			return true
		case arg == "--full-auto":
			return true
		case arg == "--dangerously-bypass-approvals-and-sandbox":
			return true
		}
	}
	return false
}
