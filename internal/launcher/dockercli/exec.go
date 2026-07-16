package dockercli

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// Exec runs one command in an existing container with the caller's streams attached.
func (client *Client) Exec(
	ctx context.Context,
	request ExecRequest,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) error {
	arguments, err := BuildExecArgs(request)
	if err != nil {
		return err
	}
	if err := client.run(ctx, arguments, stdin, stdout, stderr); err != nil {
		return fmt.Errorf("exec in managed Sysbox container: %w", err)
	}
	return nil
}

// BuildExecArgs encodes a typed exec request as Docker CLI argv.
func BuildExecArgs(request ExecRequest) ([]string, error) {
	if err := validateContainerID(request.ContainerID); err != nil {
		return nil, err
	}
	if request.User == "" {
		return nil, errors.New("container user is required")
	}
	if request.WorkingDir == "" {
		return nil, errors.New("container working directory is required")
	}
	if len(request.Command) == 0 {
		return nil, errors.New("command is required")
	}

	args := []string{"exec"}
	if request.Interactive {
		args = append(args, "--interactive")
	}
	if request.AllocateTTY {
		args = append(args, "--tty")
	}
	args = append(args, "--user", request.User)
	for _, environment := range request.Environment {
		args = append(args, "--env", environment.Key+"="+environment.Value)
	}
	args = append(args, "--workdir", request.WorkingDir, request.ContainerID)
	return append(args, request.Command...), nil
}
