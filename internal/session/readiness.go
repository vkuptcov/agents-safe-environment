package session

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

// WaitForSocket blocks until the session manager has published its Unix socket.
//
// The launcher invokes this command as root after container creation. It completes only after the
// root bootstrap has reconciled the host account, prepared its filesystem, started the nested
// daemon, and bound the manager listener. It does not connect to the manager, so it cannot
// register a command or affect idle lifecycle state.
func WaitForSocket(ctx context.Context, socketPath string) error {
	if socketPath == "" {
		return errors.New("session socket path is required")
	}

	for {
		info, err := os.Lstat(socketPath)
		if err == nil {
			if info.Mode()&fs.ModeSocket == 0 {
				return fmt.Errorf("session readiness path %q is not a socket", socketPath)
			}
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect session readiness path %q: %w", socketPath, err)
		}

		timer := time.NewTimer(connectionRetryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("wait for session readiness at %q: %w", socketPath, ctx.Err())
		case <-timer.C:
		}
	}
}
