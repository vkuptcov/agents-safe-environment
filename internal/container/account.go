package container

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type commandRunner interface {
	CombinedOutput(context.Context, string, ...string) ([]byte, error)
}

type execCommandRunner struct{}

func (execCommandRunner) CombinedOutput(ctx context.Context, name string, arguments ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, arguments...).CombinedOutput()
}

func configureHostAccount(ctx context.Context, config Config, commands commandRunner) error {
	if err := configureHostGroup(ctx, config, commands); err != nil {
		return err
	}
	if err := configureHostUser(ctx, config, commands); err != nil {
		return err
	}
	return nil
}

func configureHostGroup(ctx context.Context, config Config, commands commandRunner) error {
	groupByID, err := lookupDatabaseEntry(ctx, commands, "group", strconv.Itoa(config.HostGID))
	if err != nil {
		return fmt.Errorf("look up container group by GID: %w", err)
	}
	groupByName, err := lookupDatabaseEntry(ctx, commands, "group", config.HostGroup)
	if err != nil {
		return fmt.Errorf("look up container group by name: %w", err)
	}
	if groupByName != "" {
		name, gid, err := parseGroupEntry(groupByName)
		if err != nil {
			return err
		}
		if name != config.HostGroup || gid != config.HostGID {
			return fmt.Errorf("container group %q already uses GID %d", config.HostGroup, gid)
		}
	}

	if groupByID == "" {
		return runAccountCommand(ctx, commands, "groupadd", "--gid", strconv.Itoa(config.HostGID), config.HostGroup)
	}
	idName, idGID, err := parseGroupEntry(groupByID)
	if err != nil {
		return err
	}
	if idGID != config.HostGID {
		return fmt.Errorf("group lookup for GID %d returned GID %d", config.HostGID, idGID)
	}
	if idName == config.HostGroup {
		return nil
	}
	if groupByName != "" {
		return fmt.Errorf("GID %d has conflicting container group names", config.HostGID)
	}
	return runAccountCommand(ctx, commands, "groupmod", "--new-name", config.HostGroup, idName)
}

func configureHostUser(ctx context.Context, config Config, commands commandRunner) error {
	userByID, err := lookupDatabaseEntry(ctx, commands, "passwd", strconv.Itoa(config.HostUID))
	if err != nil {
		return fmt.Errorf("look up container user by UID: %w", err)
	}
	userByName, err := lookupDatabaseEntry(ctx, commands, "passwd", config.HostUser)
	if err != nil {
		return fmt.Errorf("look up container user by name: %w", err)
	}
	if userByName != "" {
		name, uid, err := parsePasswdEntry(userByName)
		if err != nil {
			return err
		}
		if name != config.HostUser || uid != config.HostUID {
			return fmt.Errorf("container user %q already uses UID %d", config.HostUser, uid)
		}
	}

	if userByID == "" {
		return runAccountCommand(
			ctx,
			commands,
			"useradd",
			"--uid", strconv.Itoa(config.HostUID),
			"--gid", strconv.Itoa(config.HostGID),
			"--home-dir", config.HostHome,
			"--no-create-home",
			"--shell", "/bin/bash",
			config.HostUser,
		)
	}

	idName, idUID, err := parsePasswdEntry(userByID)
	if err != nil {
		return err
	}
	if idUID != config.HostUID {
		return fmt.Errorf("user lookup for UID %d returned UID %d", config.HostUID, idUID)
	}
	if idName != config.HostUser {
		if userByName != "" {
			return fmt.Errorf("UID %d has conflicting container user names", config.HostUID)
		}
		if err := runAccountCommand(ctx, commands, "usermod", "--login", config.HostUser, idName); err != nil {
			return err
		}
	}
	return runAccountCommand(
		ctx,
		commands,
		"usermod",
		"--gid", strconv.Itoa(config.HostGID),
		"--home", config.HostHome,
		config.HostUser,
	)
}

func lookupDatabaseEntry(
	ctx context.Context,
	commands commandRunner,
	database string,
	key string,
) (string, error) {
	output, err := commands.CombinedOutput(ctx, "getent", database, key)
	if err != nil {
		var exitError interface{ ExitCode() int }
		if errors.As(err, &exitError) && exitError.ExitCode() == 2 {
			return "", nil
		}
		return "", commandOutputError("getent", []string{database, key}, output, err)
	}
	entry := strings.TrimSpace(string(output))
	if strings.Contains(entry, "\n") {
		return "", fmt.Errorf("getent %s %q returned multiple entries", database, key)
	}
	return entry, nil
}

func parseGroupEntry(entry string) (string, int, error) {
	fields := strings.Split(entry, ":")
	if len(fields) < 3 || fields[0] == "" {
		return "", 0, fmt.Errorf("parse container group entry %q", entry)
	}
	gid, err := strconv.Atoi(fields[2])
	if err != nil || gid < 0 {
		return "", 0, fmt.Errorf("parse GID in container group entry %q", entry)
	}
	return fields[0], gid, nil
}

func parsePasswdEntry(entry string) (string, int, error) {
	fields := strings.Split(entry, ":")
	if len(fields) < 7 || fields[0] == "" {
		return "", 0, fmt.Errorf("parse container passwd entry %q", entry)
	}
	uid, err := strconv.Atoi(fields[2])
	if err != nil || uid < 0 {
		return "", 0, fmt.Errorf("parse UID in container passwd entry %q", entry)
	}
	return fields[0], uid, nil
}

func runAccountCommand(ctx context.Context, commands commandRunner, name string, arguments ...string) error {
	output, err := commands.CombinedOutput(ctx, name, arguments...)
	if err != nil {
		return commandOutputError(name, arguments, output, err)
	}
	return nil
}

func commandOutputError(name string, arguments []string, output []byte, err error) error {
	diagnostic := strings.TrimSpace(string(output))
	if diagnostic == "" {
		return fmt.Errorf("run %s: %w", describeCommand(name, arguments), err)
	}
	return fmt.Errorf("run %s: %w: %s", describeCommand(name, arguments), err, diagnostic)
}

func describeCommand(name string, arguments []string) string {
	return fmt.Sprintf("%s %q", name, arguments)
}
