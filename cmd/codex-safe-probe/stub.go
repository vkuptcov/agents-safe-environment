//go:build !smoke

// Command codex-safe-probe is a test-only transport built only under the "smoke" tag. The untagged
// build is this stub: it carries no arbitrary-command execution surface, so a normal `go build ./...`
// and the product build never expose one. Build the real transport with `go build -tags smoke`.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "codex-safe-probe: built without the smoke build tag; rebuild with -tags smoke")
	os.Exit(2)
}
