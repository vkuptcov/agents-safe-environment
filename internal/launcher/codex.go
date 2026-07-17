package launcher

import "strings"

// CodexBinaryPath is the absolute image-owned path of the Codex CLI. The product always invokes
// Codex from this path so a mounted Codex home cannot shadow it through PATH, and host-side Codex
// binaries under the mounted state are treated as data, never as the launcher's executable.
const CodexBinaryPath = "/usr/local/bin/codex"

// codexDefaultSandboxArgs disable Codex's own inner sandbox. The Sysbox container is already the
// isolation boundary, so Codex's bubblewrap-based sandbox is redundant, and the image ships no
// bubblewrap on PATH. Without this default Codex prints "could not find bubblewrap" and falls back
// to a bundled copy; selecting danger-full-access keeps Codex fully functional inside the container
// without weakening the outer boundary.
var codexDefaultSandboxArgs = []string{"--sandbox", "danger-full-access"}

// DefaultCodexCommand returns the command that runs interactive Codex with the forwarded arguments.
// The image-owned Codex binary is always the executable; forwarded arguments are Codex arguments,
// never a standalone command. A default sandbox policy is applied unless the forwarded arguments
// already select one, so an explicit user choice is never overridden.
func DefaultCodexCommand(codexArgs []string) []string {
	command := make([]string, 0, len(codexArgs)+len(codexDefaultSandboxArgs)+1)
	command = append(command, CodexBinaryPath)
	if !codexArgsSelectSandbox(codexArgs) {
		command = append(command, codexDefaultSandboxArgs...)
	}
	command = append(command, codexArgs...)
	return command
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
