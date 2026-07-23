package container

import (
	"context"
	"fmt"
	"os"
	"strings"
)

const (
	tmpfsMountOptions = "nosuid,nodev,noexec"
	// ownedTmpfsMode is the mode applied to a host-user-owned tmpfs mask after chown. A fresh tmpfs is
	// always root-owned, so a Python virtual-environment mask needs an explicit non-sticky user mode
	// rather than the /tmp-style default that generic scratch mounts keep.
	ownedTmpfsMode = 0o755
)

// mountContainerTmpfs reapplies Docker's tmpfs plan from inside the final Sysbox mount namespace.
// Sysbox 0.7 can attach an idmapped project bind after Docker's tmpfs, leaving the tmpfs listed in
// mountinfo but covered. Running this during privileged bootstrap makes the intended mount the
// effective filesystem before the manager reports ready. Owned masks are then handed to the host user,
// because a fresh tmpfs is root-owned and a virtual environment the user populates must be user-owned.
func mountContainerTmpfs(
	ctx context.Context,
	mounts []TmpfsMount,
	hostUID int,
	hostGID int,
	commands systemCommandRunner,
) error {
	for _, mount := range mounts {
		info, err := os.Stat(mount.Target)
		if err != nil {
			return fmt.Errorf("stat tmpfs target %q: %w", mount.Target, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("tmpfs target %q is not a directory", mount.Target)
		}

		options := "mode=" + mount.Mode + "," + tmpfsMountOptions
		arguments := []string{"--types", "tmpfs", "--options", options, "tmpfs", mount.Target}
		output, err := commands.CombinedOutput(ctx, "mount", arguments...)
		if err != nil {
			return commandOutputError("mount", arguments, output, err)
		}

		arguments = []string{"--file-system", "--format=%T", mount.Target}
		output, err = commands.CombinedOutput(ctx, "stat", arguments...)
		if err != nil {
			return commandOutputError("stat", arguments, output, err)
		}
		if filesystem := strings.TrimSpace(string(output)); filesystem != "tmpfs" {
			return fmt.Errorf("tmpfs target %q has effective filesystem %q", mount.Target, filesystem)
		}

		if !mount.Owned {
			continue
		}
		if err := os.Chown(mount.Target, hostUID, hostGID); err != nil {
			return fmt.Errorf("set owner of tmpfs target %q: %w", mount.Target, err)
		}
		if err := os.Chmod(mount.Target, ownedTmpfsMode); err != nil {
			return fmt.Errorf("set mode of tmpfs target %q: %w", mount.Target, err)
		}
	}
	return nil
}
