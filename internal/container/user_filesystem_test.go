package container

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestPrepareUserFilesystem(t *testing.T) {
	root := t.TempDir()
	config := testConfig(t)
	config.HostUID = os.Getuid()
	config.HostGID = os.Getgid()
	config.HostHome = filepath.Join(root, "home", "alex")
	if err := os.MkdirAll(filepath.Dir(config.HostHome), 0o755); err != nil {
		t.Fatalf("create home parent: %v", err)
	}
	bashRCSource := filepath.Join(root, "bashrc")
	if err := os.WriteFile(bashRCSource, []byte("complete -r\n"), 0o644); err != nil {
		t.Fatalf("write Bash source: %v", err)
	}
	sudoersDirectory := filepath.Join(root, "sudoers.d")
	if err := os.Mkdir(sudoersDirectory, 0o755); err != nil {
		t.Fatalf("create sudoers directory: %v", err)
	}
	paths := defaultContainerPaths()
	paths.bashRCSource = bashRCSource
	paths.sudoersFile = filepath.Join(sudoersDirectory, "codex-safe-host")
	commands := &recordingSuccessRunner{}

	if err := prepareContainerUserFilesystem(context.Background(), config, paths, commands); err != nil {
		t.Fatalf("prepareContainerUserFilesystem() error = %v", err)
	}
	assertFile(t, config.HostHome, homeMode, config.HostUID, config.HostGID, "")
	assertFile(
		t,
		filepath.Join(config.HostHome, ".bashrc"),
		bashRCMode,
		config.HostUID,
		config.HostGID,
		"complete -r\n",
	)
	assertFile(t, paths.sudoersFile, sudoersMode, os.Getuid(), os.Getgid(), "alex ALL=(ALL:ALL) NOPASSWD: ALL\n")
	if len(commands.calls) != 1 || !strings.HasPrefix(commands.calls[0], "visudo|") {
		t.Fatalf("validation calls = %#v", commands.calls)
	}
}

func TestPrepareUserFilesystemDoesNotInstallInvalidSudoers(t *testing.T) {
	root := t.TempDir()
	config := testConfig(t)
	config.HostUID = os.Getuid()
	config.HostGID = os.Getgid()
	config.HostHome = filepath.Join(root, "home")
	bashRCSource := filepath.Join(root, "bashrc")
	if err := os.WriteFile(bashRCSource, nil, 0o644); err != nil {
		t.Fatalf("write Bash source: %v", err)
	}
	paths := defaultContainerPaths()
	paths.bashRCSource = bashRCSource
	paths.sudoersFile = filepath.Join(root, "sudoers")

	err := prepareContainerUserFilesystem(context.Background(), config, paths, failingCommandRunner{})
	if err == nil {
		t.Fatal("prepareContainerUserFilesystem() accepted failed visudo validation")
	}
	if _, statErr := os.Stat(paths.sudoersFile); !os.IsNotExist(statErr) {
		t.Fatalf("sudoers target exists after validation failure: %v", statErr)
	}
}

type recordingSuccessRunner struct {
	calls []string
}

func (runner *recordingSuccessRunner) CombinedOutput(
	_ context.Context,
	name string,
	arguments ...string,
) ([]byte, error) {
	runner.calls = append(runner.calls, commandKey(name, arguments...))
	return nil, nil
}

type failingCommandRunner struct{}

func (failingCommandRunner) CombinedOutput(
	context.Context,
	string,
	...string,
) ([]byte, error) {
	return []byte("invalid policy"), fakeCommandError{code: 1}
}

func assertFile(t *testing.T, path string, mode os.FileMode, uid int, gid int, contents string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	if got := info.Mode().Perm(); got != mode {
		t.Fatalf("%q mode = %o, want %o", path, got, mode)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("%q stat type = %T", path, info.Sys())
	}
	if int(stat.Uid) != uid || int(stat.Gid) != gid {
		t.Fatalf("%q owner = %d:%d, want %d:%d", path, stat.Uid, stat.Gid, uid, gid)
	}
	if info.IsDir() {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	if string(data) != contents {
		t.Fatalf("%q contents = %q, want %q", path, data, contents)
	}
}
