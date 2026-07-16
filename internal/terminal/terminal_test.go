package terminal

import (
	"bytes"
	"os"
	"testing"
)

func TestTerminalHelpersRejectNonTerminalStreams(t *testing.T) {
	t.Parallel()
	if IsTerminal(bytes.NewReader(nil)) {
		t.Fatal("IsTerminal() accepted a non-file reader")
	}
}

func TestTerminalHelpersRejectDevNull(t *testing.T) {
	t.Parallel()
	file, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	defer file.Close()

	if IsTerminal(file) {
		t.Fatal("IsTerminal() accepted a non-terminal character device")
	}
}
