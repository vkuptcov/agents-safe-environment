package container

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMountContainerTmpfsMountsAndVerifiesEffectiveFilesystem(t *testing.T) {
	target := t.TempDir()
	runner := &tmpfsCommandRunner{filesystem: "tmpfs\n"}
	if err := mountContainerTmpfs(
		context.Background(),
		[]TmpfsMount{{Target: target, Mode: "1777"}},
		0, 0,
		runner,
	); err != nil {
		t.Fatalf("mountContainerTmpfs() error = %v", err)
	}
	want := []string{
		commandKey("mount", "--types", "tmpfs", "--options", "mode=1777,"+scratchTmpfsOptions, "tmpfs", target),
		commandKey("stat", "--file-system", "--format=%T", target),
	}
	if strings.Join(runner.calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("commands = %#v, want %#v", runner.calls, want)
	}
}

func TestMountContainerTmpfsHandsOwnedMaskToHostUser(t *testing.T) {
	target := t.TempDir()
	if err := os.Chmod(target, 0o1777); err != nil {
		t.Fatal(err)
	}
	runner := &tmpfsCommandRunner{filesystem: "tmpfs\n"}
	// The command runner fakes the mount, so the temp directory keeps its real on-disk owner. Chowning
	// to the current identity is the permitted no-op that still exercises the ownership branch.
	if err := mountContainerTmpfs(
		context.Background(),
		[]TmpfsMount{{Target: target, Mode: "1777", Owned: true}},
		os.Getuid(), os.Getgid(),
		runner,
	); err != nil {
		t.Fatalf("mountContainerTmpfs() error = %v", err)
	}
	// A virtual environment exists to be executed: a noexec mask leaves every native extension module
	// intact on disk but unloadable, so the owned mount must opt out of that one flag.
	wantMount := commandKey(
		"mount", "--types", "tmpfs", "--options", "mode=1777,"+ownedTmpfsOptions, "tmpfs", target,
	)
	if runner.calls[0] != wantMount {
		t.Fatalf("owned tmpfs mount command = %q, want %q", runner.calls[0], wantMount)
	}
	if strings.Contains(ownedTmpfsOptions, "noexec") {
		t.Fatalf("owned tmpfs options must not be noexec: %q", ownedTmpfsOptions)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != ownedTmpfsMode {
		t.Fatalf("owned tmpfs mode = %o, want %o", perm, ownedTmpfsMode)
	}
	if info.Mode()&os.ModeSticky != 0 {
		t.Fatalf("owned tmpfs retained the sticky bit: %v", info.Mode())
	}
}

func TestMountContainerTmpfsFailsClosedForMissingTarget(t *testing.T) {
	target := filepath.Join(t.TempDir(), "missing")
	runner := &tmpfsCommandRunner{filesystem: "tmpfs\n"}
	err := mountContainerTmpfs(
		context.Background(),
		[]TmpfsMount{{Target: target, Mode: "1777"}},
		0, 0,
		runner,
	)
	if err == nil || !strings.Contains(err.Error(), "stat tmpfs target") {
		t.Fatalf("mountContainerTmpfs() error = %v, want missing-target diagnostic", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("commands = %#v, want no mount attempt", runner.calls)
	}
}

func TestMountContainerTmpfsRejectsIneffectiveMount(t *testing.T) {
	target := t.TempDir()
	runner := &tmpfsCommandRunner{filesystem: "ext2/ext3\n"}
	err := mountContainerTmpfs(
		context.Background(),
		[]TmpfsMount{{Target: target, Mode: "1777"}},
		0, 0,
		runner,
	)
	if err == nil || !strings.Contains(err.Error(), `effective filesystem "ext2/ext3"`) {
		t.Fatalf("mountContainerTmpfs() error = %v, want effective-filesystem diagnostic", err)
	}
}

type tmpfsCommandRunner struct {
	calls      []string
	filesystem string
}

func (runner *tmpfsCommandRunner) CombinedOutput(
	_ context.Context,
	name string,
	arguments ...string,
) ([]byte, error) {
	runner.calls = append(runner.calls, commandKey(name, arguments...))
	switch name {
	case "mount":
		return nil, nil
	case "stat":
		return []byte(runner.filesystem), nil
	default:
		return nil, fmt.Errorf("unexpected command %q", name)
	}
}
