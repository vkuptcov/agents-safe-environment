package smoke_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/require"
)

func initGitProject(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(path, 0o755), "project directory must be created")
	runInDir(t, path, "git", "init", "-q")
	runInDir(t, path, "git", "config", "user.name", "Codex Safe Smoke")
	runInDir(t, path, "git", "config", "user.email", "codex-safe@example.invalid")
	require.NoError(t, os.WriteFile(filepath.Join(path, "baseline.txt"), []byte("primary baseline\n"), 0o600), "baseline must be written")
	runInDir(t, path, "git", "add", "baseline.txt")
	runInDir(t, path, "git", "commit", "-qm", "baseline")
}

func requireMount(t *testing.T, inspection container.InspectResponse, source, destination string, writable bool) {
	t.Helper()
	for _, mount := range inspection.Mounts {
		if mount.Source == source && mount.Destination == destination {
			require.Equal(t, writable, mount.RW, "mount %q -> %q must have the expected read-write mode", source, destination)
			require.Equal(t, "rprivate", string(mount.Propagation), "mount %q -> %q must use private propagation", source, destination)
			return
		}
	}
	t.Fatalf("required mount %q -> %q was not found", source, destination)
}

func parseReport(t *testing.T, path string) map[string]string {
	t.Helper()
	values := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(readFile(t, path)), "\n") {
		key, value, found := strings.Cut(line, "=")
		require.True(t, found, "report line %q must have key=value form", line)
		values[key] = value
	}
	return values
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err, "file %q must be readable", path)
	return string(data)
}

func appendFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func requireHostOwnership(t *testing.T, paths ...string) {
	t.Helper()
	for _, root := range paths {
		err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok {
				return fmt.Errorf("read ownership of %s", path)
			}
			if int(stat.Uid) != os.Getuid() || int(stat.Gid) != os.Getgid() {
				return fmt.Errorf("%s has owner %d:%d, expected %d:%d", path, stat.Uid, stat.Gid, os.Getuid(), os.Getgid())
			}
			return nil
		})
		require.NoError(t, err, "Sysbox writes under %q must remain owned by the host user", root)
	}
}

func runInDir(t *testing.T, directory, name string, args ...string) {
	t.Helper()
	_ = runOutput(t, directory, name, args...)
}

func runOutput(t *testing.T, directory, name string, args ...string) string {
	t.Helper()
	command := exec.Command(name, args...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s %s must succeed: %s", name, strings.Join(args, " "), output)
	return string(output)
}
