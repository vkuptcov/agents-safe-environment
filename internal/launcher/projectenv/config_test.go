package projectenv

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadMounts(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	first := t.TempDir()
	secondTarget := t.TempDir()
	secondLink := filepath.Join(t.TempDir(), "mount-link")
	if err := os.Symlink(secondTarget, secondLink); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, root, fmt.Sprintf("mounts = [%q, %q, %q]\n", first, secondLink, first))

	mounts, err := LoadMounts(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{first, secondTarget}
	if !reflect.DeepEqual(mounts, want) {
		t.Fatalf("LoadMounts() = %#v, want %#v", mounts, want)
	}
}

func TestLoadMountsAbsentConfig(t *testing.T) {
	t.Parallel()
	for _, root := range []string{t.TempDir(), writeContext(t)} {
		mounts, err := LoadMounts(root)
		if err != nil || mounts != nil {
			t.Fatalf("LoadMounts(%q) = (%#v, %v), want nil, nil", root, mounts, err)
		}
	}
}

func TestLoadMountsRejectsInvalidConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "malformed", content: "mounts = [", want: "parse project environment config"},
		{name: "unknown", content: "unknown = true\n", want: "unknown setting"},
		{name: "relative", content: "mounts = [\"relative\"]\n", want: "literal absolute path"},
		{name: "root", content: "mounts = [\"/\"]\n", want: "filesystem root"},
		{name: "missing", content: "mounts = [\"/path/that/does/not/exist\"]\n", want: "resolve configured mount"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeConfig(t, root, test.content)
			_, err := LoadMounts(root)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LoadMounts() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadMountsRejectsFileAndSymlinkConfig(t *testing.T) {
	t.Parallel()
	t.Run("mount source file", func(t *testing.T) {
		root := t.TempDir()
		file := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		writeConfig(t, root, fmt.Sprintf("mounts = [%q]\n", file))
		_, err := LoadMounts(root)
		if err == nil || !strings.Contains(err.Error(), "is not a directory") {
			t.Fatalf("LoadMounts() error = %v, want directory rejection", err)
		}
	})
	t.Run("config symlink", func(t *testing.T) {
		root := t.TempDir()
		contextPath := filepath.Join(root, Directory)
		if err := os.Mkdir(contextPath, 0o755); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(target, []byte("mounts = []\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(contextPath, ConfigName)); err != nil {
			t.Fatal(err)
		}
		_, err := LoadMounts(root)
		if err == nil || !strings.Contains(err.Error(), "is not a regular file") {
			t.Fatalf("LoadMounts() error = %v, want symlink rejection", err)
		}
	})
}

func writeConfig(t *testing.T, root string, content string) {
	t.Helper()
	contextPath := filepath.Join(root, Directory)
	if err := os.MkdirAll(contextPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contextPath, ConfigName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
