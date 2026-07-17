package relay

import (
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vkuptcov/agents-safe-environment/internal/mcpchannel"
)

// scriptedConn is a lease connection whose read outcome the test dictates: after the role byte, its
// read returns either clean EOF or a chosen error. A real AF_UNIX socket cannot be made to reset
// from the peer, so this is how the EOF-versus-error decision F-004 turns on is exercised directly.
type scriptedConn struct {
	role     byte
	roleSent bool
	readErr  error // returned after the role byte; nil means clean EOF
}

func (conn *scriptedConn) Read(buffer []byte) (int, error) {
	if !conn.roleSent {
		conn.roleSent = true
		buffer[0] = conn.role
		return 1, nil
	}
	if conn.readErr != nil {
		return 0, conn.readErr
	}
	return 0, io.EOF
}

func (conn *scriptedConn) Write(buffer []byte) (int, error) { return len(buffer), nil }
func (conn *scriptedConn) Close() error                     { return nil }
func (conn *scriptedConn) LocalAddr() net.Addr              { return nil }
func (conn *scriptedConn) RemoteAddr() net.Addr             { return nil }
func (conn *scriptedConn) SetDeadline(time.Time) error      { return nil }
func (conn *scriptedConn) SetReadDeadline(time.Time) error  { return nil }
func (conn *scriptedConn) SetWriteDeadline(time.Time) error { return nil }

// handleControl reports the lease outcome: a clean EOF yields a nil error, which alone permits
// removing the generation; any read error yields a non-nil error, which must preserve it.
func TestHandleControlClassifiesLeaseEnd(t *testing.T) {
	reset := errors.New("connection reset by peer")
	cases := map[string]struct {
		readErr error
		wantErr error
	}{
		"clean EOF permits removal":      {readErr: nil, wantErr: nil},
		"read error preserves the inode": {readErr: reset, wantErr: reset},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			bound := &channel{}
			var leaseHeld atomic.Bool
			var establishedOnce sync.Once
			established := make(chan struct{}, 1)
			ended := make(chan leaseOutcome, 1)

			bound.handleControl(
				&scriptedConn{role: mcpchannel.RoleLease, readErr: testCase.readErr},
				&leaseHeld, &establishedOnce, established, ended,
			)

			select {
			case outcome := <-ended:
				if !errors.Is(outcome.err, testCase.wantErr) {
					t.Fatalf("lease outcome err = %v, want %v", outcome.err, testCase.wantErr)
				}
			default:
				t.Fatal("an established lease must report an outcome")
			}
		})
	}
}
