package container

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

const (
	homeMode    = 0o700
	bashRCMode  = 0o644
	sudoersMode = 0o440
)

// prepareContainerUserFilesystem creates the invoking user's home and shell/sudo configuration inside the container.
func prepareContainerUserFilesystem(
	ctx context.Context,
	config Config,
	paths containerPaths,
	commands systemCommandRunner,
) error {
	if err := os.MkdirAll(config.HostHome, homeMode); err != nil {
		return fmt.Errorf("create container-local home: %w", err)
	}
	if err := os.Chown(config.HostHome, config.HostUID, config.HostGID); err != nil {
		return fmt.Errorf("set container-local home owner: %w", err)
	}
	if err := os.Chmod(config.HostHome, homeMode); err != nil {
		return fmt.Errorf("set container-local home mode: %w", err)
	}

	bashRC, err := os.ReadFile(paths.bashRCSource)
	if err != nil {
		return fmt.Errorf("read image Bash configuration: %w", err)
	}
	if err := writeAtomicOwned(
		filepath.Join(config.HostHome, ".bashrc"),
		bashRC,
		bashRCMode,
		config.HostUID,
		config.HostGID,
	); err != nil {
		return fmt.Errorf("install user Bash configuration: %w", err)
	}

	sudoers := []byte(fmt.Sprintf("%s ALL=(ALL:ALL) NOPASSWD: ALL\n", config.HostUser))
	if err := writeValidatedSudoers(ctx, paths.sudoersFile, sudoers, commands); err != nil {
		return err
	}
	return nil
}

func writeValidatedSudoers(
	ctx context.Context,
	target string,
	contents []byte,
	commands systemCommandRunner,
) error {
	directory := filepath.Dir(target)
	temporary, err := os.CreateTemp(directory, ".codex-safe-sudoers-*")
	if err != nil {
		return fmt.Errorf("create temporary sudoers policy: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary sudoers policy: %w", err)
	}
	if err := temporary.Chmod(sudoersMode); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set temporary sudoers mode: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary sudoers policy: %w", err)
	}

	output, err := commands.CombinedOutput(ctx, "visudo", "--check", "--file="+temporaryPath)
	if err != nil {
		return commandOutputError("visudo", []string{"--check", "--file=" + temporaryPath}, output, err)
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		return fmt.Errorf("install sudoers policy: %w", err)
	}
	return nil
}

func writeAtomicOwned(path string, contents []byte, mode os.FileMode, uid int, gid int) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".codex-safe-file-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Chown(uid, gid); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
