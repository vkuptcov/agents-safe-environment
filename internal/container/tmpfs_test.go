package container

import (
	"context"
	"fmt"
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
		runner,
	); err != nil {
		t.Fatalf("mountContainerTmpfs() error = %v", err)
	}
	want := []string{
		commandKey("mount", "--types", "tmpfs", "--options", "mode=1777,"+tmpfsMountOptions, "tmpfs", target),
		commandKey("stat", "--file-system", "--format=%T", target),
	}
	if strings.Join(runner.calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("commands = %#v, want %#v", runner.calls, want)
	}
}

func TestMountContainerTmpfsFailsClosedForMissingTarget(t *testing.T) {
	target := filepath.Join(t.TempDir(), "missing")
	runner := &tmpfsCommandRunner{filesystem: "tmpfs\n"}
	err := mountContainerTmpfs(
		context.Background(),
		[]TmpfsMount{{Target: target, Mode: "1777"}},
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
