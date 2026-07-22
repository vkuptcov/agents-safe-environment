package dockercli

import (
	"context"
	"fmt"
	"io"
)

// RunAttached runs one container in the foreground with the caller's stdin/stdout/stderr attached,
// streaming output directly rather than buffering it. Docker owns signal forwarding and `--rm`
// cleanup; the caller owns the request's image, mounts, and command policy.
func (client *Client) RunAttached(
	ctx context.Context,
	request CreateRequest,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) error {
	arguments, err := BuildRunAttachedArgs(request)
	if err != nil {
		return err
	}
	if err := client.run(ctx, arguments, stdin, stdout, stderr); err != nil {
		return fmt.Errorf("run attached container: %w", err)
	}
	return nil
}
