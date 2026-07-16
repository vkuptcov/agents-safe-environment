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

func TestDefaultCodexCommandPrependsBinaryPath(t *testing.T) {
	t.Parallel()
	got := DefaultCodexCommand([]string{"exec", "--model", "gpt-5"})
	want := []string{CodexBinaryPath, "exec", "--model", "gpt-5"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DefaultCodexCommand() = %#v, want %#v", got, want)
	}
}

func TestDefaultCodexCommandInteractiveWithoutArguments(t *testing.T) {
	t.Parallel()
	got := DefaultCodexCommand(nil)
	want := []string{CodexBinaryPath}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DefaultCodexCommand(nil) = %#v, want %#v", got, want)
	}
}
