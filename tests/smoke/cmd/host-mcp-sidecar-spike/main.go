// Command host-mcp-sidecar-spike is a throwaway helper for the host-MCP relay sidecar spike
// described in docs/exec-plans/active/2026-07-17-host-mcp-sidecar-spike-exec-plan.md.
//
// It emulates only the proposed byte path and lease lifecycle so the spike can falsify external
// Linux/Sysbox/Docker assumptions. It is not production code, has no dependencies outside the
// standard library, and is deleted once the spike records its verdict.
//
// Modes:
//
//	relay           the sidecar: binds the channel sockets, dials the host endpoint, owns the lease
//	lease-client    the session container's stand-in for `codex-safe-session serve`'s lease
//	socket-client   a one-shot byte-path probe run on the host or through `docker exec`
//	launcher-helper a host child process that creates a generation, a sidecar, and a session
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// The control protocol is one byte in each direction. A connection announces its role, and a probe
// additionally reads one readiness byte. The lease sends its role byte and no further data.
const (
	roleLease    byte = 'L'
	roleProbe    byte = 'P'
	byteReady    byte = 'R'
	byteNotReady byte = 'N'
	byteRefused  byte = 'X'
)

const (
	controlSocketName = "control.sock"
	socketMode        = 0o600
	directoryMode     = 0o700
)

func main() {
	if len(os.Args) < 2 {
		fail(errors.New("usage: host-mcp-sidecar-spike <relay|lease-client|socket-client|launcher-helper> [flags]"))
	}
	var err error
	switch os.Args[1] {
	case "relay":
		err = runRelay(os.Args[2:])
	case "lease-client":
		err = runLeaseClient(os.Args[2:])
	case "socket-client":
		err = runSocketClient(os.Args[2:])
	case "dial-check":
		err = runDialCheck(os.Args[2:])
	case "session-check":
		err = runSessionCheck(os.Args[2:])
	case "launcher-helper":
		err = runLauncherHelper(os.Args[2:])
	default:
		err = fmt.Errorf("unknown mode %q", os.Args[1])
	}
	if err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "host-mcp-sidecar-spike:", err)
	os.Exit(1)
}

func logf(format string, args ...any) {
	fmt.Printf(format+"\n", args...)
}

// ---------------------------------------------------------------------------
// events
// ---------------------------------------------------------------------------

// emitter reports lifecycle transitions to the spike orchestrator over host loopback. The
// orchestrator timestamps each event on its own monotonic clock, which is why the sidecar reports
// rather than being polled, and it withholds its reply to gate a transition when a scenario needs
// a real barrier instead of a sleep.
type emitter struct {
	address  string
	instance string
	timeout  time.Duration
}

func (e emitter) emit(event string) {
	if e.address == "" {
		return
	}
	connection, err := net.DialTimeout("tcp", e.address, e.timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "event %q dial failed: %v\n", event, err)
		return
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(e.timeout))
	if _, err := fmt.Fprintf(connection, "%s\t%s\n", e.instance, event); err != nil {
		fmt.Fprintf(os.Stderr, "event %q write failed: %v\n", event, err)
		return
	}
	if _, err := bufio.NewReader(connection).ReadString('\n'); err != nil {
		fmt.Fprintf(os.Stderr, "event %q acknowledgement failed: %v\n", event, err)
	}
}

// ---------------------------------------------------------------------------
// relay
// ---------------------------------------------------------------------------

func runRelay(args []string) error {
	flags := flag.NewFlagSet("relay", flag.ExitOnError)
	generation := flags.String("generation", "", "generation directory as seen inside this container")
	endpoint := flags.String("endpoint", "", "host loopback endpoint as host:port")
	sockets := flags.Int("sockets", 1, "number of endpoint sockets to bind")
	events := flags.String("events", "", "orchestrator event sink as host:port")
	instance := flags.String("instance", "relay", "instance name reported with each event")
	initialLease := flags.Duration("initial-lease-timeout", 60*time.Second, "spike-only initial-lease failure bound")
	eventTimeout := flags.Duration("event-timeout", 90*time.Second, "bound on one event round trip")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *generation == "" || *endpoint == "" {
		return errors.New("relay requires --generation and --endpoint")
	}

	report := emitter{address: *events, instance: *instance, timeout: *eventTimeout}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)

	endpointPaths := make([]string, 0, *sockets)
	for index := range *sockets {
		endpointPaths = append(endpointPaths, filepath.Join(*generation, fmt.Sprintf("e%d.sock", index)))
	}
	controlPath := filepath.Join(*generation, controlSocketName)
	expected := append(append([]string{}, endpointPaths...), controlPath)

	// Startup is all-or-nothing. A stale socket left by a previous sidecar in this owned generation
	// is removed; any other entry is an error and is never removed.
	if err := removeStaleSockets(expected); err != nil {
		return err
	}

	listeners := make([]net.Listener, 0, len(expected))
	closeListeners := func() {
		for _, listener := range listeners {
			_ = listener.Close()
		}
		// Closing a Go unix listener unlinks the path it created; make removal explicit so a
		// failed attempt cannot leave a partial endpoint set behind.
		for _, path := range expected {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				fmt.Fprintf(os.Stderr, "removing socket %s: %v\n", path, err)
			}
		}
	}

	for _, path := range endpointPaths {
		listener, err := listenUnix(path)
		if err != nil {
			closeListeners()
			return fmt.Errorf("binding endpoint socket %s: %w", path, err)
		}
		listeners = append(listeners, listener)
	}

	// control.sock is bound last so its existence cannot be observed while the endpoint set is
	// partial, and accepting starts only once the whole set is bound.
	controlListener, err := listenUnix(controlPath)
	if err != nil {
		closeListeners()
		return fmt.Errorf("binding control socket %s: %w", controlPath, err)
	}
	listeners = append(listeners, controlListener)

	for index, listener := range listeners[:len(endpointPaths)] {
		go acceptEndpoint(listener, *endpoint, index)
	}

	leaseEstablished := make(chan struct{})
	leaseClosed := make(chan struct{})
	go acceptControl(controlListener, leaseEstablished, leaseClosed)

	logf("relay bound %d endpoint sockets and %s in %s", len(endpointPaths), controlSocketName, *generation)
	report.emit("bound")

	select {
	case <-leaseEstablished:
	case <-time.After(*initialLease):
		// No lease arrived. The same path may already be bind-mounted by a live session during
		// recovery, so the directory inode is preserved and only the socket entries go.
		logf("relay initial-lease timeout after %s", *initialLease)
		closeListeners()
		report.emit("initial-lease-timeout")
		return fmt.Errorf("no lease within %s", *initialLease)
	case received := <-signals:
		logf("relay received %s before a lease", received)
		closeListeners()
		report.emit("signal-before-lease")
		return nil
	}

	logf("relay lease established")
	report.emit("lease-established")

	select {
	case <-leaseClosed:
		// EOF from an established lease proves the session process holding the bind mount is gone,
		// which is the only condition that permits removing the generation directory.
		logf("relay observed lease EOF")
		report.emit("lease-eof")
		closeListeners()
		if err := os.RemoveAll(*generation); err != nil {
			report.emit("cleanup-failed")
			return fmt.Errorf("removing generation %s: %w", *generation, err)
		}
		logf("relay removed generation %s", *generation)
		report.emit("cleanup-done")
		return nil
	case received := <-signals:
		logf("relay received %s while leased", received)
		closeListeners()
		report.emit("signal-after-lease")
		return nil
	}
}

func listenUnix(path string) (net.Listener, error) {
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	// Go creates the socket under the process umask, so the mode the design requires is applied
	// explicitly rather than assumed.
	if err := os.Chmod(path, socketMode); err != nil {
		_ = listener.Close()
		return nil, err
	}
	return listener, nil
}

func removeStaleSockets(paths []string) error {
	for _, path := range paths {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspecting %s: %w", path, err)
		}
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("unexpected non-socket entry %s with mode %v", path, info.Mode())
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("removing stale socket %s: %w", path, err)
		}
		logf("relay removed stale socket %s", path)
	}
	return nil
}

func acceptEndpoint(listener net.Listener, endpoint string, index int) {
	for {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer connection.Close()
			// The sidecar shares the host network namespace, so this dial reaches the host's own
			// loopback. A dial failure affects only the connection that caused it.
			target, err := net.DialTimeout("tcp", endpoint, 10*time.Second)
			if err != nil {
				fmt.Fprintf(os.Stderr, "e%d dial %s failed: %v\n", index, endpoint, err)
				return
			}
			defer target.Close()
			logf("relay e%d connected to %s", index, endpoint)
			copyBothWays(connection, target)
		}()
	}
}

func acceptControl(listener net.Listener, established chan<- struct{}, closed chan<- struct{}) {
	var leaseHeld atomic.Bool
	var establishedOnce atomic.Bool
	for {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			role := make([]byte, 1)
			_ = connection.SetReadDeadline(time.Now().Add(30 * time.Second))
			if _, err := io.ReadFull(connection, role); err != nil {
				_ = connection.Close()
				return
			}
			_ = connection.SetReadDeadline(time.Time{})
			switch role[0] {
			case roleProbe:
				// A probe reports ready only once the lease also exists, so a launcher never starts
				// a command against a channel whose container side has not attached yet.
				answer := byteNotReady
				if leaseHeld.Load() {
					answer = byteReady
				}
				_, _ = connection.Write([]byte{answer})
				_ = connection.Close()
			case roleLease:
				if !leaseHeld.CompareAndSwap(false, true) {
					_, _ = connection.Write([]byte{byteRefused})
					_ = connection.Close()
					return
				}
				if establishedOnce.CompareAndSwap(false, true) {
					close(established)
				}
				// The lease sends no further data; its EOF is the session's death certificate.
				_, _ = io.Copy(io.Discard, connection)
				_ = connection.Close()
				leaseHeld.Store(false)
				close(closed)
			default:
				_ = connection.Close()
			}
		}()
	}
}

// copyBothWays moves bytes until each side closes, propagating a half-close rather than tearing the
// peer down, so a request/response exchange completes in both directions.
func copyBothWays(first, second net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(first, second)
		closeWrite(first)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(second, first)
		closeWrite(second)
		done <- struct{}{}
	}()
	<-done
	<-done
}

func closeWrite(connection net.Conn) {
	if half, ok := connection.(interface{ CloseWrite() error }); ok {
		_ = half.CloseWrite()
	}
}

// ---------------------------------------------------------------------------
// lease-client
// ---------------------------------------------------------------------------

// runLeaseClient stands in for the lease `codex-safe-session serve` holds for the session's life. It
// retries in the background so a replacement sidecar is leased without replacing the session.
func runLeaseClient(args []string) error {
	flags := flag.NewFlagSet("lease-client", flag.ExitOnError)
	control := flags.String("control", "", "control socket path inside this container")
	retry := flags.Duration("retry", time.Second, "lease retry interval")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *control == "" {
		return errors.New("lease-client requires --control")
	}
	for {
		// Each attempt is logged before it is made so the orchestrator can measure the retry gap
		// from the host daemon's own log timestamps.
		logf("lease attempt")
		connection, err := net.Dial("unix", *control)
		if err != nil {
			logf("lease attempt failed: %v", err)
			time.Sleep(*retry)
			continue
		}
		if _, err := connection.Write([]byte{roleLease}); err != nil {
			logf("lease role byte failed: %v", err)
			_ = connection.Close()
			time.Sleep(*retry)
			continue
		}
		logf("lease established on %s", *control)
		_, _ = io.Copy(io.Discard, connection)
		_ = connection.Close()
		logf("lease lost")
		time.Sleep(*retry)
	}
}

// ---------------------------------------------------------------------------
// socket-client
// ---------------------------------------------------------------------------

// runSocketClient is the byte-path assertion: it sends the request nonce through an endpoint socket
// and requires the sentinel's response nonce to come back.
func runSocketClient(args []string) error {
	flags := flag.NewFlagSet("socket-client", flag.ExitOnError)
	socket := flags.String("socket", "", "endpoint socket path")
	request := flags.String("request", "", "request nonce to send")
	expect := flags.String("expect", "", "response nonce required from the sentinel")
	timeout := flags.Duration("timeout", 30*time.Second, "bound on the whole round trip")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *socket == "" || *request == "" || *expect == "" {
		return errors.New("socket-client requires --socket, --request and --expect")
	}
	connection, err := net.DialTimeout("unix", *socket, *timeout)
	if err != nil {
		return fmt.Errorf("dialing %s: %w", *socket, err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(*timeout))
	if _, err := fmt.Fprintf(connection, "%s\n", *request); err != nil {
		return fmt.Errorf("writing request nonce: %w", err)
	}
	closeWrite(connection)
	answer, err := io.ReadAll(connection)
	if err != nil {
		return fmt.Errorf("reading response nonce: %w", err)
	}
	got := strings.TrimSpace(string(answer))
	if got != *expect {
		return fmt.Errorf("response nonce mismatch: got %q, want %q", got, *expect)
	}
	logf("nonce round trip ok")
	return nil
}

// ---------------------------------------------------------------------------
// session-check
// ---------------------------------------------------------------------------

// runSessionCheck asserts, from inside the Sysbox session container, the two facts requirement 2
// actually rests on: that this process really is the mapped host identity rather than root, and that
// Sysbox presents the sidecar's socket with the host's ownership and mode intact. A nonce round trip
// proves connectability but would look identical if the socket were root-owned and this process held
// a DAC bypass, so connectability alone cannot carry the requirement.
func runSessionCheck(args []string) error {
	flags := flag.NewFlagSet("session-check", flag.ExitOnError)
	socket := flags.String("socket", "", "endpoint socket path inside this container")
	expectUID := flags.Int("expect-uid", -1, "required effective UID and socket owner")
	expectGID := flags.Int("expect-gid", -1, "required effective GID and socket group")
	expectMode := flags.Int("expect-mode", 0o600, "required socket permission bits")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *socket == "" || *expectUID < 0 || *expectGID < 0 {
		return errors.New("session-check requires --socket, --expect-uid and --expect-gid")
	}

	if effective := os.Geteuid(); effective != *expectUID {
		return fmt.Errorf("effective UID is %d, want the mapped host UID %d", effective, *expectUID)
	}
	if effective := os.Getegid(); effective != *expectGID {
		return fmt.Errorf("effective GID is %d, want the mapped host GID %d", effective, *expectGID)
	}

	info, err := os.Lstat(*socket)
	if err != nil {
		return fmt.Errorf("inspecting %s: %w", *socket, err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("%s is %v, not a Unix socket", *socket, info.Mode())
	}
	if permission := info.Mode().Perm(); permission != os.FileMode(*expectMode) {
		return fmt.Errorf("%s has mode %04o, want %04o", *socket, permission, *expectMode)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot read ownership of %s", *socket)
	}
	if int(stat.Uid) != *expectUID || int(stat.Gid) != *expectGID {
		return fmt.Errorf("%s is owned by %d:%d, want %d:%d as mapped by Sysbox",
			*socket, stat.Uid, stat.Gid, *expectUID, *expectGID)
	}
	logf("session-check euid=%d egid=%d socket=%s type=socket mode=%04o owner=%d:%d",
		os.Geteuid(), os.Getegid(), *socket, info.Mode().Perm(), stat.Uid, stat.Gid)
	return nil
}

// ---------------------------------------------------------------------------
// dial-check
// ---------------------------------------------------------------------------

// runDialCheck dials the host endpoint directly, with no channel involved. It exists so the spike can
// run the same image, command, identity and confinement in two network namespaces and show that only
// the host one reaches a loopback-bound service. Without that control, host-loopback reachability
// would be evidenced only by a success, never by the matching failure.
func runDialCheck(args []string) error {
	flags := flag.NewFlagSet("dial-check", flag.ExitOnError)
	endpoint := flags.String("endpoint", "", "host loopback endpoint as host:port")
	request := flags.String("request", "", "request nonce to send")
	expect := flags.String("expect", "", "response nonce required from the sentinel")
	timeout := flags.Duration("timeout", 10*time.Second, "bound on the whole round trip")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *endpoint == "" || *request == "" || *expect == "" {
		return errors.New("dial-check requires --endpoint, --request and --expect")
	}
	connection, err := net.DialTimeout("tcp", *endpoint, *timeout)
	if err != nil {
		return fmt.Errorf("dialing %s: %w", *endpoint, err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(*timeout))
	if _, err := fmt.Fprintf(connection, "%s\n", *request); err != nil {
		return fmt.Errorf("writing request nonce: %w", err)
	}
	closeWrite(connection)
	answer, err := io.ReadAll(connection)
	if err != nil {
		return fmt.Errorf("reading response nonce: %w", err)
	}
	if got := strings.TrimSpace(string(answer)); got != *expect {
		return fmt.Errorf("response nonce mismatch: got %q, want %q", got, *expect)
	}
	logf("direct host dial ok")
	return nil
}

// ---------------------------------------------------------------------------
// launcher-helper
// ---------------------------------------------------------------------------

// runLauncherHelper emulates one launch attempt as a host child process. It uses the host Docker CLI
// for the same reason the product launcher does, and it exists so the spike can prove a sidecar and
// its lease outlive the launcher that created them.
func runLauncherHelper(args []string) error {
	flags := flag.NewFlagSet("launcher-helper", flag.ExitOnError)
	barrier := flags.String("barrier", "", "orchestrator barrier as host:port, released before this attempt starts")
	createBarrier := flags.String("create-barrier", "", "orchestrator barrier as host:port, released immediately before the contested session create")
	hold := flags.String("hold", "", "after handoff, run this heartbeat file inside the session and block until killed")
	hostParent := flags.String("host-parent", "", "project runtime parent on the host")
	generation := flags.String("generation", "", "this attempt's generation directory name")
	sidecarName := flags.String("sidecar-name", "", "this attempt's sidecar container name")
	sessionName := flags.String("session-name", "", "deterministic session container name")
	image := flags.String("image", "", "immutable image ID for both containers")
	endpoint := flags.String("endpoint", "", "host sentinel endpoint as host:port")
	events := flags.String("events", "", "orchestrator event sink as host:port")
	label := flags.String("label", "", "spike isolation label as key=value")
	result := flags.String("result", "", "path for this attempt's result report")
	readiness := flags.Duration("readiness", 60*time.Second, "spike-only readiness failure bound")
	if err := flags.Parse(args); err != nil {
		return err
	}
	for name, value := range map[string]string{
		"host-parent": *hostParent, "generation": *generation, "sidecar-name": *sidecarName,
		"session-name": *sessionName, "image": *image, "endpoint": *endpoint, "label": *label,
		"result": *result,
	} {
		if value == "" {
			return fmt.Errorf("launcher-helper requires --%s", name)
		}
	}

	generationPath := filepath.Join(*hostParent, *generation)
	if err := os.MkdirAll(*hostParent, directoryMode); err != nil {
		return fmt.Errorf("creating runtime parent: %w", err)
	}
	if err := os.Mkdir(generationPath, directoryMode); err != nil {
		return fmt.Errorf("creating candidate generation: %w", err)
	}

	if *barrier != "" {
		if err := waitForBarrier(*barrier); err != nil {
			return err
		}
	}

	if err := createSidecar(*sidecarName, *image, *hostParent, *generation, *endpoint, *events, *label); err != nil {
		return fmt.Errorf("creating candidate sidecar: %w", err)
	}
	if _, err := docker("start", *sidecarName); err != nil {
		return fmt.Errorf("starting candidate sidecar: %w", err)
	}

	// Building a candidate sidecar takes long enough that a barrier released before it would let one
	// attempt finish the whole race before the other reached it. Wait until this attempt's channel is
	// actually bound, then rendezvous again, so the contested create is the next thing both do.
	if *createBarrier != "" {
		if err := awaitPath(filepath.Join(generationPath, controlSocketName), *readiness); err != nil {
			return fmt.Errorf("awaiting candidate channel: %w", err)
		}
		if err := waitForBarrier(*createBarrier); err != nil {
			return err
		}
	}

	// The session container's name is the sole arbiter of creation. Each attempt allocates its own
	// generation, so both candidate sidecars are created; only one attempt wins this create.
	output, err := createSession(*sessionName, *image, generationPath, *label)
	if err != nil {
		if !isNameConflict(output) {
			return fmt.Errorf("creating session: %w: %s", err, output)
		}
		// Loser: stop and await only this attempt's sidecar, remove only this attempt's
		// generation, and adopt nothing.
		logf("launcher-helper lost the session-name race")
		if _, err := docker("stop", "--timeout", "10", *sidecarName); err != nil {
			return fmt.Errorf("stopping candidate sidecar: %w", err)
		}
		if err := awaitContainerGone(*sidecarName, *readiness); err != nil {
			return err
		}
		if err := os.RemoveAll(generationPath); err != nil {
			return fmt.Errorf("removing candidate generation: %w", err)
		}
		return writeReport(*result, map[string]string{
			"outcome":    "loser",
			"sidecar":    *sidecarName,
			"generation": *generation,
		})
	}

	if _, err := docker("start", *sessionName); err != nil {
		return fmt.Errorf("starting session: %w", err)
	}
	logf("launcher-helper won the session-name race")
	if err := awaitReady(filepath.Join(generationPath, controlSocketName), *readiness); err != nil {
		return err
	}

	// A launcher that returns cleanly is not the case the design cares about. --hold starts a command
	// through docker exec and stays attached to it, so the orchestrator can kill this process group
	// out from under a running command, the way a dying terminal would.
	var held *exec.Cmd
	if *hold != "" {
		held = exec.Command("docker", "exec", *sessionName, "/bin/sh", "-c",
			fmt.Sprintf("while true; do date +%%s%%N >> %s; sleep 0.2; done", *hold))
		if err := held.Start(); err != nil {
			return fmt.Errorf("starting the held command: %w", err)
		}
		logf("launcher-helper is attached to a running command and holding")
	}

	if err := writeReport(*result, map[string]string{
		"outcome":    "winner",
		"sidecar":    *sidecarName,
		"generation": *generation,
		"session":    strings.TrimSpace(output),
	}); err != nil {
		return err
	}
	if held != nil {
		// Waiting on the attached command is what a launcher does, and it parks this process on a
		// real syscall. An empty select would instead trip Go's all-goroutines-asleep detector and
		// kill the launcher before the orchestrator could.
		return held.Wait()
	}
	return nil
}

// awaitPath waits for a path to exist, bounded, so a rendezvous cannot be reached before the thing
// it is meant to synchronize on actually exists.
func awaitPath(path string, bound time.Duration) error {
	deadline := time.Now().Add(bound)
	for time.Now().Before(deadline) {
		if _, err := os.Lstat(path); err == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("%s did not appear within %s", path, bound)
}

func createSidecar(name, image, hostParent, generation, endpoint, events, label string) error {
	arguments := []string{
		"create",
		"--name", name,
		"--network=host",
		"--user", numericIdentity(),
		"--read-only",
		"--cap-drop=ALL",
		"--security-opt=no-new-privileges",
		"--rm",
		"--label", label,
		"--label", "codex-safe.host-mcp-spike-role=sidecar",
		"--volume", hostParent + ":" + sidecarParentTarget,
		image,
		"relay",
		"--generation", filepath.Join(sidecarParentTarget, generation),
		"--endpoint", endpoint,
		"--instance", name,
	}
	if events != "" {
		arguments = append(arguments, "--events", events)
	}
	_, err := docker(arguments...)
	return err
}

func createSession(name, image, generationPath, label string) (string, error) {
	return docker(
		"create",
		"--name", name,
		"--runtime=sysbox-runc",
		"--user", numericIdentity(),
		"--rm",
		"--label", label,
		"--label", "codex-safe.host-mcp-spike-role=session",
		"--volume", generationPath+":"+sessionGenerationTarget,
		image,
		"lease-client",
		"--control", filepath.Join(sessionGenerationTarget, controlSocketName),
		"--retry", "1s",
	)
}

const (
	// The design names only the session container's target. The sidecar mounts the project runtime
	// parent, so the spike gives it a distinct target of its own.
	sidecarParentTarget     = "/run/codex-safe-host-mcp-parent"
	sessionGenerationTarget = "/run/codex-safe-host-mcp"
)

func numericIdentity() string {
	return fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
}

func docker(arguments ...string) (string, error) {
	command := exec.Command("docker", arguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("docker %s: %w", strings.Join(arguments, " "), err)
	}
	return string(output), nil
}

func isNameConflict(output string) bool {
	return strings.Contains(output, "is already in use by container")
}

func waitForBarrier(address string) error {
	connection, err := net.DialTimeout("tcp", address, 30*time.Second)
	if err != nil {
		return fmt.Errorf("dialing barrier: %w", err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(60 * time.Second))
	if _, err := bufio.NewReader(connection).ReadString('\n'); err != nil {
		return fmt.Errorf("awaiting barrier release: %w", err)
	}
	return nil
}

func awaitReady(controlPath string, bound time.Duration) error {
	deadline := time.Now().Add(bound)
	var last error
	for time.Now().Before(deadline) {
		ready, err := probeReady(controlPath)
		if err == nil && ready {
			return nil
		}
		last = err
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("channel %s did not report ready within %s (last: %v)", controlPath, bound, last)
}

func probeReady(controlPath string) (bool, error) {
	connection, err := net.DialTimeout("unix", controlPath, 5*time.Second)
	if err != nil {
		return false, err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := connection.Write([]byte{roleProbe}); err != nil {
		return false, err
	}
	answer := make([]byte, 1)
	if _, err := io.ReadFull(connection, answer); err != nil {
		return false, err
	}
	return answer[0] == byteReady, nil
}

func awaitContainerGone(name string, bound time.Duration) error {
	deadline := time.Now().Add(bound)
	for time.Now().Before(deadline) {
		output, err := docker("container", "inspect", "--format", "{{.State.Status}}", name)
		if err != nil && strings.Contains(output, "No such container") {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("container %s still exists after %s", name, bound)
}

func writeReport(path string, values map[string]string) error {
	builder := strings.Builder{}
	for key, value := range values {
		fmt.Fprintf(&builder, "%s=%s\n", key, value)
	}
	return os.WriteFile(path, []byte(builder.String()), 0o600)
}
