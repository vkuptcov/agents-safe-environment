package launcher

// CodexBinaryPath is the absolute image-owned path of the Codex CLI. The product always invokes
// Codex from this path so a mounted Codex home cannot shadow it through PATH, and host-side Codex
// binaries under the mounted state are treated as data, never as the launcher's executable.
const CodexBinaryPath = "/usr/local/bin/codex"

// DefaultCodexCommand returns the command that runs interactive Codex with the forwarded arguments.
// The image-owned Codex binary is always the executable; forwarded arguments are Codex arguments,
// never a standalone command.
func DefaultCodexCommand(codexArgs []string) []string {
	command := make([]string, 0, len(codexArgs)+1)
	command = append(command, CodexBinaryPath)
	command = append(command, codexArgs...)
	return command
}
