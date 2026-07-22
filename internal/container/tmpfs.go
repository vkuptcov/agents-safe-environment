package container

import (
	"context"
	"fmt"
	"os"
	"strings"
)

const tmpfsMountOptions = "nosuid,nodev,noexec"

// mountContainerTmpfs reapplies Docker's tmpfs plan from inside the final Sysbox mount namespace.
// Sysbox 0.7 can attach an idmapped project bind after Docker's tmpfs, leaving the tmpfs listed in
// mountinfo but covered. Running this during privileged bootstrap makes the intended mount the
// effective filesystem before the manager reports ready.
func mountContainerTmpfs(
	ctx context.Context,
	mounts []TmpfsMount,
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
	}
	return nil
}
