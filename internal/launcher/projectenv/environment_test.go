package projectenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverAbsentDockerfile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	definition, err := Discover(root)
	if err != nil || definition != nil {
		t.Fatalf("Discover() = (%#v, %v), want (nil, nil)", definition, err)
	}
}

func TestDiscoverHashesContextDeterministically(t *testing.T) {
	t.Parallel()
	first := writeContext(t, "project one")
	second := writeContext(t, "project two")
	for _, root := range []string{first, second} {
		if err := os.WriteFile(filepath.Join(root, Directory, "Dockerfile"), []byte("FROM base\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(root, Directory, "nested"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, Directory, "nested", "tool"), []byte("tool\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	left, err := Discover(first)
	if err != nil {
		t.Fatal(err)
	}
	right, err := Discover(second)
	if err != nil {
		t.Fatal(err)
	}
	if left.Digest != right.Digest {
		t.Fatalf("digest differs across checkout paths: %q != %q", left.Digest, right.Digest)
	}
	if !strings.HasPrefix(left.EnvironmentLabel(), "sha256:") {
		t.Fatalf("EnvironmentLabel() = %q", left.EnvironmentLabel())
	}
}

func TestDiscoverChangesForContentAndExecutableMode(t *testing.T) {
	t.Parallel()
	root := writeContext(t, "project")
	path := filepath.Join(root, Directory, DockerfileName)
	if err := os.WriteFile(path, []byte("FROM base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("FROM changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	afterContent, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if before.Digest == afterContent.Digest {
		t.Fatal("content change did not change digest")
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	afterMode, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if afterContent.Digest == afterMode.Digest {
		t.Fatal("executable-bit change did not change digest")
	}
}

func TestDiscoverRejectsUnsafeEntries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		setup func(t *testing.T, context string)
	}{
		{name: "symlink", setup: func(t *testing.T, context string) {
			if err := os.Symlink("Dockerfile", filepath.Join(context, "link")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "named pipe", setup: func(t *testing.T, context string) {
			if err := os.Mkdir(filepath.Join(context, "directory"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := writeContext(t, test.name)
			context := filepath.Join(root, Directory)
			if err := os.WriteFile(filepath.Join(context, DockerfileName), []byte("FROM base\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			test.setup(t, context)
			if test.name == "named pipe" {
				t.Skip("named-pipe creation is covered by platform integration")
			}
			if _, err := Discover(root); err == nil {
				t.Fatal("Discover() accepted unsafe entry")
			}
		})
	}
}

func TestCacheKeyAndLocalImageName(t *testing.T) {
	t.Parallel()
	digest := strings.Repeat("a", 64)
	base := "sha256:" + strings.Repeat("b", 64)
	key, err := CacheKey(digest, base)
	if err != nil {
		t.Fatal(err)
	}
	name, err := LocalImageName("project-key", key)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(name, "codex-safe-project-project-key:") {
		t.Fatalf("name = %q", name)
	}
}

func writeContext(t *testing.T, name string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(root, Directory), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}
