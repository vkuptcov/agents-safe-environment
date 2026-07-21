package session

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWaitForSocketWaitsForUnixSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.sock")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	go func() {
		time.Sleep(connectionRetryInterval)
		listener, err := net.Listen("unix", path)
		if err != nil {
			t.Errorf("listen: %v", err)
			return
		}
		defer listener.Close()
		<-ctx.Done()
	}()

	if err := WaitForSocket(ctx, path); err != nil {
		t.Fatalf("WaitForSocket() error = %v", err)
	}
}

func TestWaitForSocketRejectsNonSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-socket")
	if err := os.WriteFile(path, []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WaitForSocket(context.Background(), path); err == nil {
		t.Fatal("WaitForSocket() accepted a regular file")
	}
}

func TestWaitForSocketHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := WaitForSocket(ctx, filepath.Join(t.TempDir(), "missing.sock"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitForSocket() error = %v, want context cancellation", err)
	}
}
