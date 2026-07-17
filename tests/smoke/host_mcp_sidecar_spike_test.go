package smoke_test

// Throwaway orchestration for the host-MCP relay sidecar spike described in
// docs/exec-plans/active/2026-07-17-host-mcp-sidecar-spike-exec-plan.md.
//
// This file and tests/smoke/cmd/host-mcp-sidecar-spike are deleted once the spike records its
// verdict. Nothing here becomes production code or permanent smoke coverage: it exists only to
// falsify the seven external Linux/Sysbox/Docker assumptions the design rests on.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/strslice"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/stretchr/testify/require"
)

const (
	spikeEnv      = "CODEX_SAFE_RUN_HOST_MCP_SPIKE"
	spikeBaseTag  = "codex-safe-mvp:local"
	spikeLabelKey = "codex-safe.host-mcp-spike"
	spikeRoleKey  = "codex-safe.host-mcp-spike-role"
	spikeHelper   = "/usr/local/bin/host-mcp-sidecar-spike"

	// The sidecar mounts the project runtime parent so it can remove its own generation directory;
	// the session container mounts only the generation itself.
	spikeParentTarget     = "/run/codex-safe-host-mcp-parent"
	spikeGenerationTarget = "/run/codex-safe-host-mcp"
	spikeControlSocket    = "control.sock"
	spikeEndpointSocket   = "e0.sock"

	// spikeWatchdog bounds every awaited lifecycle transition so a hung experiment terminates. It is
	// a failure bound for this throwaway harness, never a proposed production timeout.
	spikeWatchdog = 60 * time.Second
	spikeOverall  = 12 * time.Minute
	spikePoll     = 20 * time.Millisecond
)

// ---------------------------------------------------------------------------
// evidence
// ---------------------------------------------------------------------------

type spikeRequirement struct {
	id          string
	title       string
	status      string
	observation string
}

var spikeRequirementTitles = []struct{ id, title string }{
	{"R1", "Host-loopback reachability"},
	{"R2", "Shared socket ownership"},
	{"R3", "Concurrent-create arbitration"},
	{"R4", "Launcher-death survival"},
	{"R5", "Lease closure"},
	{"R6", "Sidecar restart"},
	{"R7", "Generation cleanup"},
}

// ---------------------------------------------------------------------------
// host sentinel
// ---------------------------------------------------------------------------

// spikeSentinel is the host loopback service the sidecar must reach. It is bound to 127.0.0.1 only,
// so a container without the host network namespace cannot see it, and it answers a random request
// nonce with a random response nonce to make the byte path assertable in both directions.
type spikeSentinel struct {
	listener net.Listener
	request  string
	response string
}

func newSpikeSentinel(t *testing.T) *spikeSentinel {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "host sentinel must bind loopback")
	sentinel := &spikeSentinel{listener: listener, request: spikeNonce(), response: spikeNonce()}
	go sentinel.serve()
	t.Cleanup(func() { _ = listener.Close() })
	return sentinel
}

func (s *spikeSentinel) address() string { return s.listener.Addr().String() }

func (s *spikeSentinel) serve() {
	for {
		connection, err := s.listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer connection.Close()
			_ = connection.SetDeadline(time.Now().Add(spikeWatchdog))
			line, err := bufio.NewReader(connection).ReadString('\n')
			if err != nil || strings.TrimSpace(line) != s.request {
				return
			}
			_, _ = fmt.Fprintf(connection, "%s\n", s.response)
		}()
	}
}

// ---------------------------------------------------------------------------
// event sink
// ---------------------------------------------------------------------------

type spikeEvent struct {
	instance string
	name     string
	at       time.Time
}

// spikeEventSink timestamps sidecar lifecycle transitions on the host orchestrator's clock, which is
// the only clock this spike measures with. It also withholds its acknowledgement when a scenario
// needs to hold a sidecar at a transition, giving a real barrier instead of a sleep.
type spikeEventSink struct {
	t        *testing.T
	listener net.Listener
	mutex    sync.Mutex
	records  []spikeEvent
	gates    map[string]chan struct{}
}

func newSpikeEventSink(t *testing.T) *spikeEventSink {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "event sink must bind loopback")
	sink := &spikeEventSink{t: t, listener: listener, gates: map[string]chan struct{}{}}
	go sink.serve()
	t.Cleanup(func() { _ = listener.Close() })
	return sink
}

func (s *spikeEventSink) address() string { return s.listener.Addr().String() }

func (s *spikeEventSink) serve() {
	for {
		connection, err := s.listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer connection.Close()
			_ = connection.SetDeadline(time.Now().Add(2 * spikeWatchdog))
			line, err := bufio.NewReader(connection).ReadString('\n')
			if err != nil {
				return
			}
			at := time.Now()
			instance, name, found := strings.Cut(strings.TrimSpace(line), "\t")
			if !found {
				return
			}
			s.mutex.Lock()
			s.records = append(s.records, spikeEvent{instance: instance, name: name, at: at})
			gate := s.gates[instance+"\t"+name]
			s.mutex.Unlock()
			if gate != nil {
				<-gate
			}
			_, _ = fmt.Fprintln(connection, "ok")
		}()
	}
}

// gate holds every future acknowledgement of one instance's event until the returned release runs.
func (s *spikeEventSink) gate(instance, name string) (release func()) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	blocked := make(chan struct{})
	s.gates[instance+"\t"+name] = blocked
	return sync.OnceFunc(func() { close(blocked) })
}

func (s *spikeEventSink) find(instance, name string) (spikeEvent, bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for _, record := range s.records {
		if record.instance == instance && record.name == name {
			return record, true
		}
	}
	return spikeEvent{}, false
}

// await polls the already-timestamped record set. Polling bounds only how quickly the orchestrator
// notices an event, never when the event is recorded as having happened.
func (s *spikeEventSink) await(instance, name string) spikeEvent {
	s.t.Helper()
	deadline := time.Now().Add(spikeWatchdog)
	for time.Now().Before(deadline) {
		if record, ok := s.find(instance, name); ok {
			return record
		}
		time.Sleep(spikePoll)
	}
	s.t.Fatalf("sidecar %q never reported %q within the %s watchdog", instance, name, spikeWatchdog)
	return spikeEvent{}
}

func (s *spikeEventSink) requireAbsent(instance, name string) {
	s.t.Helper()
	_, found := s.find(instance, name)
	require.False(s.t, found, "sidecar %q must not have reported %q", instance, name)
}

// ---------------------------------------------------------------------------
// fixture
// ---------------------------------------------------------------------------

type spikeFixture struct {
	t           *testing.T
	ctx         context.Context
	docker      *client.Client
	runID       string
	label       string
	imageTag    string
	imageID     string
	baseImageID string
	mutableTag  string
	root        string
	parent      string
	sentinel    *spikeSentinel
	events      *spikeEventSink
	identity    string
	facts       []string
	timings     []string
	results     map[string]spikeRequirement
}

func newSpikeFixture(t *testing.T) *spikeFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), spikeOverall)
	t.Cleanup(cancel)
	docker, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err, "Moby client must initialize from the Docker environment")
	t.Cleanup(func() { _ = docker.Close() })

	fixture := &spikeFixture{
		t:        t,
		ctx:      ctx,
		docker:   docker,
		runID:    spikeNonce(),
		identity: fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		results:  map[string]spikeRequirement{},
	}
	fixture.label = spikeLabelKey + "=" + fixture.runID
	fixture.imageTag = "codex-safe-host-mcp-spike:" + fixture.runID
	fixture.mutableTag = "codex-safe-host-mcp-spike-mutable:" + fixture.runID
	fixture.sentinel = newSpikeSentinel(t)
	fixture.events = newSpikeEventSink(t)
	return fixture
}

func spikeNonce() string {
	buffer := make([]byte, 6)
	if _, err := rand.Read(buffer); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buffer)
}

func (f *spikeFixture) fact(format string, args ...any) {
	f.facts = append(f.facts, fmt.Sprintf(format, args...))
	f.t.Logf("FACT %s", fmt.Sprintf(format, args...))
}

func (f *spikeFixture) timing(format string, args ...any) {
	f.timings = append(f.timings, fmt.Sprintf(format, args...))
	f.t.Logf("TIMING %s", fmt.Sprintf(format, args...))
}

// elapsed records one measured interval. It rejects a negative result outright: a duration that runs
// backwards means the harness matched the wrong event, and a spike that reports such a number is
// reporting a harness bug as evidence.
func (f *spikeFixture) elapsed(label string, from, to time.Time) time.Duration {
	f.t.Helper()
	measured := to.Sub(from)
	require.GreaterOrEqual(f.t, measured, time.Duration(0),
		"%s measured %s: the harness matched an event that predates its own start point", label, measured)
	f.timing("%s: %s", label, measured.Round(time.Millisecond))
	return measured
}

func (f *spikeFixture) pass(id, observation string) {
	f.record(id, "PASS", observation)
}

func (f *spikeFixture) record(id, status, observation string) {
	for _, requirement := range spikeRequirementTitles {
		if requirement.id == id {
			f.results[id] = spikeRequirement{id: id, title: requirement.title, status: status, observation: observation}
			f.t.Logf("RESULT %s %s: %s -- %s", id, requirement.title, status, observation)
			return
		}
	}
	f.t.Fatalf("unknown requirement %q", id)
}

// report prints the seven-row evidence table. It runs even when a requirement fails, so an aborted
// spike still shows which row is decisive and which rows were never reached.
func (f *spikeFixture) report() {
	var builder strings.Builder
	builder.WriteString("\n===== HOST MCP SIDECAR SPIKE EVIDENCE =====\n")
	builder.WriteString("-- environment --\n")
	for _, entry := range f.facts {
		builder.WriteString("  " + entry + "\n")
	}
	builder.WriteString("-- timings (host-side clocks: orchestrator, or the Docker daemon for its own events) --\n")
	for _, entry := range f.timings {
		builder.WriteString("  " + entry + "\n")
	}
	builder.WriteString("-- requirements --\n")
	verdict := "PASS"
	for _, requirement := range spikeRequirementTitles {
		result, ok := f.results[requirement.id]
		if !ok {
			builder.WriteString(fmt.Sprintf("  %s %-32s NOT REACHED\n", requirement.id, requirement.title))
			verdict = "FAIL"
			continue
		}
		builder.WriteString(fmt.Sprintf("  %s %-32s %s -- %s\n", result.id, result.title, result.status, result.observation))
		if result.status != "PASS" {
			verdict = "FAIL"
		}
	}
	builder.WriteString("-- verdict --\n  " + verdict + "\n")
	builder.WriteString("===========================================\n")
	f.t.Log(builder.String())
}

// ---------------------------------------------------------------------------
// preflight
// ---------------------------------------------------------------------------

func (f *spikeFixture) preflight() {
	f.t.Helper()
	require.Equal(f.t, "linux", runtime.GOOS, "the spike is Linux-only")

	version, err := f.docker.ServerVersion(f.ctx)
	require.NoError(f.t, err, "host Docker Engine must be reachable")
	info, err := f.docker.Info(f.ctx)
	require.NoError(f.t, err, "host Docker Engine must report its info")
	_, registered := info.Runtimes["sysbox-runc"]
	require.True(f.t, registered, "sysbox-runc must be a registered Docker runtime")

	kernel, err := os.ReadFile("/proc/sys/kernel/osrelease")
	require.NoError(f.t, err, "kernel release must be readable")

	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	require.NotEmpty(f.t, runtimeDir, "XDG_RUNTIME_DIR must be set")
	runtimeInfo, err := os.Stat(runtimeDir)
	require.NoError(f.t, err, "XDG_RUNTIME_DIR must exist")
	require.True(f.t, runtimeInfo.IsDir(), "XDG_RUNTIME_DIR must be a directory")
	runtimeStat, ok := runtimeInfo.Sys().(*syscall.Stat_t)
	require.True(f.t, ok, "XDG_RUNTIME_DIR ownership must be readable")
	require.Equal(f.t, os.Getuid(), int(runtimeStat.Uid), "XDG_RUNTIME_DIR must be owned by the invoking user")
	require.Zero(f.t, runtimeInfo.Mode().Perm()&0o077, "XDG_RUNTIME_DIR must grant no group or other access")

	base, err := f.docker.ImageInspect(f.ctx, spikeBaseTag)
	require.NoError(f.t, err, "the local %s image must exist; run make docker-build", spikeBaseTag)
	f.baseImageID = base.ID

	sysboxVersion := "unknown"
	if output, err := exec.Command("sysbox-runc", "--version").CombinedOutput(); err == nil {
		for _, line := range strings.Split(string(output), "\n") {
			if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "version:") {
				sysboxVersion = strings.TrimSpace(strings.TrimPrefix(trimmed, "version:"))
			}
		}
	}

	f.fact("kernel: %s", strings.TrimSpace(string(kernel)))
	f.fact("docker server: %s (API %s), default runtime %s", version.Version, version.APIVersion, info.DefaultRuntime)
	f.fact("sysbox-runc: registered, version %s", sysboxVersion)
	f.fact("host identity: uid=%d gid=%d", os.Getuid(), os.Getgid())
	f.fact("XDG_RUNTIME_DIR: %s mode %04o owner %d:%d", runtimeDir, runtimeInfo.Mode().Perm(), runtimeStat.Uid, runtimeStat.Gid)
	f.fact("base image %s: %s", spikeBaseTag, f.baseImageID)
	f.fact("spike run id: %s", f.runID)

	// Every resource carries the run label and cleanup is registered before anything is created.
	f.t.Cleanup(f.removeRuntimeRoot)
	f.t.Cleanup(f.removeImages)
	f.t.Cleanup(f.removeContainers)

	f.root = filepath.Join(runtimeDir, "cs-mcp-spike-"+f.runID)
	f.parent = filepath.Join(f.root, "p1")
	require.NoError(f.t, os.MkdirAll(f.parent, 0o700), "run-specific runtime parent must be created")
	control := filepath.Join(f.parent, "g-"+spikeNonce(), spikeControlSocket)
	require.Less(f.t, len(control), 108, "the deepest host socket path must fit sockaddr_un: %s", control)
	f.fact("runtime root: %s (deepest host socket path %d bytes)", f.root, len(control))
}

func (f *spikeFixture) removeContainers() {
	ctx, cancel := context.WithTimeout(context.Background(), spikeWatchdog)
	defer cancel()
	items, err := f.docker.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("label", f.label)),
	})
	if err != nil {
		f.t.Logf("spike cleanup could not list containers: %v", err)
		return
	}
	for _, item := range items {
		if err := f.docker.ContainerRemove(ctx, item.ID, container.RemoveOptions{Force: true}); err != nil {
			f.t.Logf("spike cleanup could not remove container %s: %v", item.ID, err)
		}
	}
}

func (f *spikeFixture) removeImages() {
	ctx, cancel := context.WithTimeout(context.Background(), spikeWatchdog)
	defer cancel()
	// The mutable tag is removed by name because immutable-image recovery deliberately points it at
	// an image the spike label does not cover.
	for _, reference := range []string{f.mutableTag, f.imageTag} {
		if _, err := f.docker.ImageRemove(ctx, reference, image.RemoveOptions{Force: true, PruneChildren: true}); err != nil {
			if !client.IsErrNotFound(err) {
				f.t.Logf("spike cleanup could not remove image %s: %v", reference, err)
			}
		}
	}
}

func (f *spikeFixture) removeRuntimeRoot() {
	if f.root == "" {
		return
	}
	if err := os.RemoveAll(f.root); err != nil {
		f.t.Logf("spike cleanup could not remove %s: %v", f.root, err)
	}
}

// ---------------------------------------------------------------------------
// throwaway image
// ---------------------------------------------------------------------------

// buildImage installs the helper into a uniquely tagged throwaway image derived from the real local
// image. The helper is installed rather than bind-mounted because the sidecar's one-mount rule is
// itself under test.
func (f *spikeFixture) buildImage() {
	f.t.Helper()
	contextDir := f.t.TempDir()
	build := exec.Command("go", "build", "-o", filepath.Join(contextDir, "host-mcp-sidecar-spike"),
		"./cmd/host-mcp-sidecar-spike")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	output, err := build.CombinedOutput()
	require.NoError(f.t, err, "the spike helper must build: %s", output)

	dockerfile := fmt.Sprintf(`FROM %s
COPY host-mcp-sidecar-spike %s
LABEL %s
ENTRYPOINT ["/usr/bin/tini", "--", "%s"]
`, spikeBaseTag, spikeHelper, f.label, spikeHelper)
	require.NoError(f.t, os.WriteFile(filepath.Join(contextDir, "Dockerfile"), []byte(dockerfile), 0o600),
		"the temporary Dockerfile must be written")

	output, err = exec.Command("docker", "build", "--tag", f.imageTag, contextDir).CombinedOutput()
	require.NoError(f.t, err, "the throwaway image must build: %s", output)

	inspection, err := f.docker.ImageInspect(f.ctx, f.imageTag)
	require.NoError(f.t, err, "the throwaway image must be inspectable")
	f.imageID = inspection.ID
	require.Equal(f.t, []string{"/usr/bin/tini", "--", spikeHelper}, []string(inspection.Config.Entrypoint),
		"the throwaway entrypoint must supervise the helper through tini")
	require.Empty(f.t, inspection.Config.Cmd, "the throwaway image must supply no default command")
	require.Equal(f.t, f.runID, inspection.Config.Labels[spikeLabelKey], "the throwaway image must carry the run label")
	require.NotEqual(f.t, f.baseImageID, f.imageID, "the throwaway image must differ from its base")

	require.NoError(f.t, f.docker.ImageTag(f.ctx, f.imageTag, f.mutableTag), "the run-specific mutable tag must be created")
	f.fact("throwaway image %s: %s", f.imageTag, f.imageID)
	f.fact("mutable tag %s initially resolves to %s", f.mutableTag, f.resolveTag(f.mutableTag))
}

func (f *spikeFixture) resolveTag(reference string) string {
	f.t.Helper()
	inspection, err := f.docker.ImageInspect(f.ctx, reference)
	require.NoError(f.t, err, "image reference %q must resolve", reference)
	return inspection.ID
}

// ---------------------------------------------------------------------------
// container creation
// ---------------------------------------------------------------------------

func (f *spikeFixture) generationPath(generation string) string {
	return filepath.Join(f.parent, generation)
}

func (f *spikeFixture) newGeneration() string {
	f.t.Helper()
	generation := "g-" + spikeNonce()
	require.NoError(f.t, os.Mkdir(f.generationPath(generation), 0o700), "generation directory must be created 0700")
	return generation
}

// createSidecar builds the relay's create request exactly as the design specifies it: host network
// only, the recreated host identity, read-only root, no capability, no-new-privileges, the Docker
// default runtime, --rm, and the project runtime parent as its single mount.
//
// instance is the relay's event label and is deliberately independent of the container name, because
// recovery deliberately reuses one deterministic name for two successive relay processes and their
// events must stay distinguishable.
func (f *spikeFixture) createSidecar(name, instance, generation, imageID string) (string, time.Time) {
	f.t.Helper()
	created, err := f.tryCreateSidecar(name, instance, generation, imageID)
	require.NoError(f.t, err, "sidecar %q must be created", name)
	return created, time.Now()
}

func (f *spikeFixture) tryCreateSidecar(name, instance, generation, imageID string) (string, error) {
	config := &container.Config{
		Image: imageID,
		User:  f.identity,
		Cmd: []string{
			"relay",
			"--generation", filepath.Join(spikeParentTarget, generation),
			"--endpoint", f.sentinel.address(),
			"--events", f.events.address(),
			"--instance", instance,
			"--initial-lease-timeout", spikeWatchdog.String(),
		},
		Labels: map[string]string{spikeLabelKey: f.runID, spikeRoleKey: "sidecar"},
	}
	hostConfig := &container.HostConfig{
		NetworkMode:    "host",
		ReadonlyRootfs: true,
		CapDrop:        strslice.StrSlice{"ALL"},
		SecurityOpt:    []string{"no-new-privileges"},
		AutoRemove:     true,
		Mounts:         []mount.Mount{{Type: mount.TypeBind, Source: f.parent, Target: spikeParentTarget}},
	}
	created, err := f.docker.ContainerCreate(f.ctx, config, hostConfig, nil, nil, name)
	if err != nil {
		return "", err
	}
	return created.ID, nil
}

func (f *spikeFixture) createSession(name, generation, imageID string) (string, time.Time) {
	f.t.Helper()
	config := &container.Config{
		Image: imageID,
		User:  f.identity,
		Cmd: []string{
			"lease-client",
			"--control", filepath.Join(spikeGenerationTarget, spikeControlSocket),
			"--retry", "1s",
		},
		Labels: map[string]string{spikeLabelKey: f.runID, spikeRoleKey: "session"},
	}
	hostConfig := &container.HostConfig{
		Runtime:    "sysbox-runc",
		AutoRemove: true,
		Mounts: []mount.Mount{{
			Type:   mount.TypeBind,
			Source: f.generationPath(generation),
			Target: spikeGenerationTarget,
		}},
	}
	created, err := f.docker.ContainerCreate(f.ctx, config, hostConfig, nil, nil, name)
	require.NoError(f.t, err, "session %q must be created", name)
	at := time.Now()
	return created.ID, at
}

func (f *spikeFixture) start(containerID, description string) time.Time {
	f.t.Helper()
	require.NoError(f.t, f.docker.ContainerStart(f.ctx, containerID, container.StartOptions{}),
		"%s must start", description)
	return time.Now()
}

// startPair creates and starts one sidecar and one session against a fresh generation, and returns
// once the channel is ready. It is the cold-start path the design's launch sequence describes.
func (f *spikeFixture) startPair(prefix string) (generation, sidecarName, sidecarID, sessionName, sessionID string) {
	f.t.Helper()
	generation = f.newGeneration()
	sidecarName = "codex-safe-mcp-" + prefix + "-" + generation
	sessionName = "codex-safe-session-" + prefix + "-" + f.runID

	sidecarID, createdAt := f.createSidecar(sidecarName, sidecarName, generation, f.imageID)
	f.start(sidecarID, "sidecar "+sidecarName)
	f.events.await(sidecarName, "bound")

	sessionID, _ = f.createSession(sessionName, generation, f.imageID)
	f.start(sessionID, "session "+sessionName)

	leased := f.events.await(sidecarName, "lease-established")
	f.elapsed(prefix+" cold sidecar-create-to-first-successful-lease", createdAt, leased.at)
	f.requireReady(generation)
	return generation, sidecarName, sidecarID, sessionName, sessionID
}

// runDialCheck runs one throwaway container that dials the host sentinel directly and returns its
// exit code. Everything except the network namespace is held constant between calls, so the pair of
// results is a controlled experiment rather than a single hopeful success.
func (f *spikeFixture) runDialCheck(name, networkMode string) (int, string) {
	f.t.Helper()
	config := &container.Config{
		Image: f.imageID,
		User:  f.identity,
		Cmd: []string{
			"dial-check",
			"--endpoint", f.sentinel.address(),
			"--request", f.sentinel.request,
			"--expect", f.sentinel.response,
		},
		Labels: map[string]string{spikeLabelKey: f.runID, spikeRoleKey: "dial-check"},
	}
	hostConfig := &container.HostConfig{
		NetworkMode:    container.NetworkMode(networkMode),
		ReadonlyRootfs: true,
		CapDrop:        strslice.StrSlice{"ALL"},
		SecurityOpt:    []string{"no-new-privileges"},
	}
	created, err := f.docker.ContainerCreate(f.ctx, config, hostConfig, nil, nil, name)
	require.NoError(f.t, err, "dial-check container %q must be created", name)
	defer func() {
		_ = f.docker.ContainerRemove(f.ctx, created.ID, container.RemoveOptions{Force: true})
	}()
	f.start(created.ID, "dial check on "+networkMode)
	waitOK, waitErr := f.docker.ContainerWait(f.ctx, created.ID, container.WaitConditionNotRunning)
	select {
	case err := <-waitErr:
		f.t.Fatalf("dial-check on %s could not be awaited: %v", networkMode, err)
	case status := <-waitOK:
		return int(status.StatusCode), f.logs(created.ID)
	case <-time.After(spikeWatchdog):
		f.t.Fatalf("dial-check on %s never exited within the %s watchdog", networkMode, spikeWatchdog)
	}
	return -1, ""
}

// ---------------------------------------------------------------------------
// channel probes
// ---------------------------------------------------------------------------

func (f *spikeFixture) controlPath(generation string) string {
	return filepath.Join(f.generationPath(generation), spikeControlSocket)
}

func (f *spikeFixture) endpointPath(generation string) string {
	return filepath.Join(f.generationPath(generation), spikeEndpointSocket)
}

func (f *spikeFixture) requireReady(generation string) {
	f.t.Helper()
	deadline := time.Now().Add(spikeWatchdog)
	var last error
	for time.Now().Before(deadline) {
		ready, err := f.probe(generation)
		if err == nil && ready {
			return
		}
		last = err
		time.Sleep(spikePoll)
	}
	f.t.Fatalf("channel %s never reported ready within %s (last error: %v)", generation, spikeWatchdog, last)
}

func (f *spikeFixture) probe(generation string) (bool, error) {
	connection, err := net.DialTimeout("unix", f.controlPath(generation), 5*time.Second)
	if err != nil {
		return false, err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := connection.Write([]byte{'P'}); err != nil {
		return false, err
	}
	answer := make([]byte, 1)
	if _, err := io.ReadFull(connection, answer); err != nil {
		return false, err
	}
	return answer[0] == 'R', nil
}

// nonceFromHost drives the byte path from the host side of the mount: host -> endpoint socket ->
// sidecar -> host loopback sentinel and back.
func (f *spikeFixture) nonceFromHost(generation string) (string, error) {
	connection, err := net.DialTimeout("unix", f.endpointPath(generation), 5*time.Second)
	if err != nil {
		return "", err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(spikeWatchdog))
	if _, err := fmt.Fprintf(connection, "%s\n", f.sentinel.request); err != nil {
		return "", err
	}
	if half, ok := connection.(interface{ CloseWrite() error }); ok {
		_ = half.CloseWrite()
	}
	answer, err := io.ReadAll(connection)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(answer)), nil
}

func (f *spikeFixture) requireNonceFromHost(generation, description string) {
	f.t.Helper()
	answer, err := f.nonceFromHost(generation)
	require.NoError(f.t, err, "%s must complete a nonce round trip", description)
	require.Equal(f.t, f.sentinel.response, answer, "%s must receive the sentinel's response nonce", description)
}

// nonceFromSession drives the same byte path from inside the Sysbox session container, through its
// ID-shifted bind mount.
func (f *spikeFixture) nonceFromSession(sessionID string) (string, int) {
	f.t.Helper()
	return f.execInSession(sessionID, []string{
		spikeHelper, "socket-client",
		"--socket", filepath.Join(spikeGenerationTarget, spikeEndpointSocket),
		"--request", f.sentinel.request,
		"--expect", f.sentinel.response,
	})
}

func (f *spikeFixture) requireNonceFromSession(sessionID, description string) {
	f.t.Helper()
	deadline := time.Now().Add(spikeWatchdog)
	var output string
	var code int
	for time.Now().Before(deadline) {
		output, code = f.nonceFromSession(sessionID)
		if code == 0 {
			return
		}
		time.Sleep(spikePoll)
	}
	f.t.Fatalf("%s never completed a nonce round trip within %s: exit %d\n%s", description, spikeWatchdog, code, output)
}

func (f *spikeFixture) execInSession(sessionID string, command []string) (string, int) {
	f.t.Helper()
	created, err := f.docker.ContainerExecCreate(f.ctx, sessionID, container.ExecOptions{
		Cmd:          command,
		AttachStdout: true,
		AttachStderr: true,
	})
	require.NoError(f.t, err, "exec must be created in the session container")
	attached, err := f.docker.ContainerExecAttach(f.ctx, created.ID, container.ExecAttachOptions{})
	require.NoError(f.t, err, "exec must attach in the session container")
	defer attached.Close()
	var out, errOut bytes.Buffer
	_, err = stdcopy.StdCopy(&out, &errOut, attached.Reader)
	require.NoError(f.t, err, "exec output must be readable")
	inspection, err := f.docker.ContainerExecInspect(f.ctx, created.ID)
	require.NoError(f.t, err, "exec must be inspectable")
	return out.String() + errOut.String(), inspection.ExitCode
}

// ---------------------------------------------------------------------------
// docker observation helpers
// ---------------------------------------------------------------------------

func (f *spikeFixture) inspect(reference string) container.InspectResponse {
	f.t.Helper()
	inspection, err := f.docker.ContainerInspect(f.ctx, reference)
	require.NoError(f.t, err, "container %q must be inspectable", reference)
	return inspection
}

func (f *spikeFixture) listRole(role string) []container.Summary {
	f.t.Helper()
	items, err := f.docker.ContainerList(f.ctx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", f.label),
			filters.Arg("label", spikeRoleKey+"="+role),
		),
	})
	require.NoError(f.t, err, "containers with role %q must be listable", role)
	return items
}

func (f *spikeFixture) awaitGone(name string) time.Time {
	f.t.Helper()
	deadline := time.Now().Add(spikeWatchdog)
	for time.Now().Before(deadline) {
		_, err := f.docker.ContainerInspect(f.ctx, name)
		if client.IsErrNotFound(err) {
			return time.Now()
		}
		time.Sleep(spikePoll)
	}
	f.t.Fatalf("container %q still exists after the %s watchdog", name, spikeWatchdog)
	return time.Time{}
}

func (f *spikeFixture) logs(containerID string) string {
	f.t.Helper()
	reader, err := f.docker.ContainerLogs(f.ctx, containerID, container.LogsOptions{
		ShowStdout: true, ShowStderr: true, Timestamps: true,
	})
	if err != nil {
		return fmt.Sprintf("logs unavailable: %v", err)
	}
	defer reader.Close()
	var out, errOut bytes.Buffer
	if _, err := stdcopy.StdCopy(&out, &errOut, reader); err != nil {
		return fmt.Sprintf("logs unreadable: %v", err)
	}
	return out.String() + errOut.String()
}

// leaseRetryGap reports the widest interval the helper actually let pass between two steps of its
// lease loop after `since` -- losing the lease, or an attempt failing, and the next attempt. The
// timestamps are applied by the host Docker daemon as it reads the container's stream, so this stays
// a host-side measurement even though the session container has no host channel of its own.
func (f *spikeFixture) leaseRetryGap(containerID string, since time.Time) (time.Duration, int) {
	f.t.Helper()
	var stamps []time.Time
	for _, line := range strings.Split(f.logs(containerID), "\n") {
		if !strings.Contains(line, "lease attempt") && !strings.Contains(line, "lease lost") {
			continue
		}
		stamp, _, found := strings.Cut(strings.TrimSpace(line), " ")
		if !found {
			continue
		}
		parsed, err := time.Parse(time.RFC3339Nano, stamp)
		if err != nil || !parsed.After(since) {
			continue
		}
		stamps = append(stamps, parsed)
	}
	if len(stamps) < 2 {
		return 0, len(stamps)
	}
	var widest time.Duration
	for index := 1; index < len(stamps); index++ {
		if gap := stamps[index].Sub(stamps[index-1]); gap > widest {
			widest = gap
		}
	}
	return widest, len(stamps)
}

func (f *spikeFixture) statSocket(path string) (os.FileInfo, *syscall.Stat_t) {
	f.t.Helper()
	info, err := os.Lstat(path)
	require.NoError(f.t, err, "socket %q must exist on the host", path)
	stat, ok := info.Sys().(*syscall.Stat_t)
	require.True(f.t, ok, "socket %q ownership must be readable", path)
	return info, stat
}

func (f *spikeFixture) directoryIdentity(path string) (uint64, uint64) {
	f.t.Helper()
	info, err := os.Stat(path)
	require.NoError(f.t, err, "directory %q must exist", path)
	stat, ok := info.Sys().(*syscall.Stat_t)
	require.True(f.t, ok, "directory %q identity must be readable", path)
	return uint64(stat.Dev), stat.Ino
}

// subscribe returns the Docker event stream for one container, so lifecycle transitions are observed
// as daemon events rather than inferred from polling.
func (f *spikeFixture) subscribe(containerID string) (context.CancelFunc, <-chan events.Message, <-chan error) {
	ctx, cancel := context.WithCancel(f.ctx)
	messages, errs := f.docker.Events(ctx, events.ListOptions{Filters: filters.NewArgs(
		filters.Arg("type", "container"),
		filters.Arg("container", containerID),
	)})
	return cancel, messages, errs
}

// awaitDockerEvent returns the daemon's own timestamp for the transition, not the moment this test
// happened to read it. The difference is not cosmetic: ContainerStop returns only after the
// container has died, so the die event is already buffered and a read-time reading would silently
// understate every interval measured from it.
func (f *spikeFixture) awaitDockerEvent(messages <-chan events.Message, errs <-chan error, action string) time.Time {
	f.t.Helper()
	watchdog := time.NewTimer(spikeWatchdog)
	defer watchdog.Stop()
	for {
		select {
		case message := <-messages:
			if string(message.Action) == action {
				return time.Unix(0, message.TimeNano)
			}
		case err := <-errs:
			f.t.Fatalf("Docker event stream failed while awaiting %q: %v", action, err)
		case <-watchdog.C:
			f.t.Fatalf("Docker never reported %q within the %s watchdog", action, spikeWatchdog)
		}
	}
}

// ---------------------------------------------------------------------------
// the spike
// ---------------------------------------------------------------------------

func TestHostMCPSidecarSpike(t *testing.T) {
	if os.Getenv(spikeEnv) != "1" {
		t.Skipf("set %s=1 to run the throwaway host-MCP sidecar spike", spikeEnv)
	}
	fixture := newSpikeFixture(t)
	defer fixture.report()
	fixture.preflight()
	fixture.buildImage()

	fixture.phaseReachabilityAndOwnership()
	fixture.phaseArbitrationAndLifetime()
	fixture.phaseClosureRecoveryAndIsolation()
}

// phaseReachabilityAndOwnership settles requirements 1 and 2.
func (f *spikeFixture) phaseReachabilityAndOwnership() {
	f.t.Helper()
	generation := f.newGeneration()
	sidecarName := "codex-safe-mcp-reach-" + generation
	sidecarID, createdAt := f.createSidecar(sidecarName, sidecarName, generation, f.imageID)
	f.start(sidecarID, "reachability sidecar")
	f.events.await(sidecarName, "bound")

	// The security contract is asserted from the daemon's own view of the running container.
	inspection := f.inspect(sidecarID)
	require.Equal(f.t, "host", string(inspection.HostConfig.NetworkMode), "the sidecar shares the host network namespace")
	require.Empty(f.t, inspection.HostConfig.PidMode, "the sidecar must not share the host PID namespace")
	require.Empty(f.t, inspection.HostConfig.UTSMode, "the sidecar must not share the host UTS namespace")
	require.NotEqual(f.t, "host", string(inspection.HostConfig.IpcMode), "the sidecar must not share the host IPC namespace")
	require.Equal(f.t, f.identity, inspection.Config.User, "the sidecar runs as the numeric host identity")
	require.Equal(f.t, strslice.StrSlice{"ALL"}, inspection.HostConfig.CapDrop, "the sidecar drops every capability")
	require.Empty(f.t, inspection.HostConfig.CapAdd, "the sidecar adds no capability")
	require.True(f.t, inspection.HostConfig.ReadonlyRootfs, "the sidecar has a read-only root filesystem")
	require.Contains(f.t, inspection.HostConfig.SecurityOpt, "no-new-privileges", "the sidecar carries no-new-privileges")
	require.False(f.t, inspection.HostConfig.Privileged, "the sidecar is never privileged")
	require.True(f.t, inspection.HostConfig.AutoRemove, "Docker owns the sidecar's removal through --rm")
	require.NotEqual(f.t, "sysbox-runc", inspection.HostConfig.Runtime, "the sidecar uses the Docker default runtime")
	require.Len(f.t, inspection.Mounts, 1, "the sidecar receives exactly one mount")
	require.Equal(f.t, f.parent, inspection.Mounts[0].Source, "the sidecar's only mount is the project runtime parent")
	require.Equal(f.t, spikeParentTarget, inspection.Mounts[0].Destination, "the sidecar's mount target is the runtime parent")
	require.True(f.t, inspection.Mounts[0].RW, "the sidecar's mount is read-write")
	for _, entry := range inspection.Mounts {
		require.NotContains(f.t, entry.Source, "docker.sock", "the sidecar never receives a Docker socket")
		require.NotContains(f.t, entry.Destination, "docker.sock", "the sidecar never receives a Docker socket")
	}
	require.Equal(f.t, f.runID, inspection.Config.Labels[spikeLabelKey], "the sidecar carries the run label")
	f.fact("sidecar runtime resolved to %q with %d mount(s)", inspection.HostConfig.Runtime, len(inspection.Mounts))

	// Requirement 1: the confined host-network sidecar reaches a loopback-only host service.
	f.requireNonceFromHost(generation, "the host side of the channel")

	// The control: the same image, command, identity and confinement on the default bridge network.
	// It must fail, or the sentinel was never loopback-only and R1 would prove nothing.
	bridgeCode, bridgeOutput := f.runDialCheck("codex-safe-mcp-control-bridge-"+f.runID, "bridge")
	require.NotZero(f.t, bridgeCode,
		"a bridge-networked container must not reach the loopback-only sentinel: %s", bridgeOutput)
	hostCode, hostOutput := f.runDialCheck("codex-safe-mcp-control-host-"+f.runID, "host")
	require.Zero(f.t, hostCode,
		"the same container on the host network must reach the sentinel: %s", hostOutput)
	f.fact("control: identical confined container reached the sentinel on --network=host (exit %d) and failed on the default bridge network (exit %d)",
		hostCode, bridgeCode)

	f.pass("R1", fmt.Sprintf(
		"--network=host --cap-drop=ALL --read-only sidecar running as %s dialed the loopback-only sentinel at %s and returned the response nonce; the identical container on the default bridge network could not reach it (exit %d), so the reach comes from the host network namespace",
		f.identity, f.sentinel.address(), bridgeCode))

	// Requirement 2, host half: the sidecar's socket has the required ownership and mode.
	info, stat := f.statSocket(f.endpointPath(generation))
	require.NotZero(f.t, info.Mode()&os.ModeSocket, "e0.sock must be a Unix socket on the host")
	require.Equal(f.t, os.FileMode(0o600), info.Mode().Perm(), "e0.sock must be 0600 on the host")
	require.Equal(f.t, os.Getuid(), int(stat.Uid), "e0.sock must be owned by the invoking host user")
	require.Equal(f.t, os.Getgid(), int(stat.Gid), "e0.sock must carry the invoking host group")
	f.fact("host socket %s: type=socket mode=%04o owner=%d:%d",
		spikeEndpointSocket, info.Mode().Perm(), stat.Uid, stat.Gid)

	// Requirement 2, container half: the same socket is connectable from the Sysbox session through
	// its ID-shifted mount, as the numeric host user.
	sessionName := "codex-safe-session-reach-" + f.runID
	sessionID, _ := f.createSession(sessionName, generation, f.imageID)
	f.start(sessionID, "reachability session")
	leased := f.events.await(sidecarName, "lease-established")
	// This window deliberately contains the harness's own inspection and host-probe work, so it is
	// an upper bound rather than a clean cold-start figure. The uninterrupted pairs started by
	// startPair are the measurements the execution plan should size against.
	f.elapsed("reachability sidecar-create-to-first-successful-lease (includes harness inspection work)",
		createdAt, leased.at)
	f.requireReady(generation)

	sessionInspection := f.inspect(sessionID)
	require.Equal(f.t, "sysbox-runc", sessionInspection.HostConfig.Runtime, "the session container runs under Sysbox")
	require.Len(f.t, sessionInspection.Mounts, 1, "the session container mounts only the generation directory")
	require.Equal(f.t, spikeGenerationTarget, sessionInspection.Mounts[0].Destination,
		"the session container's mount target is the generation directory")

	identity, code := f.execInSession(sessionID, []string{"/usr/bin/id", "-u"})
	require.Zero(f.t, code, "the session container must report its numeric user: %s", identity)
	f.fact("session container user inside Sysbox: uid=%s", strings.TrimSpace(identity))

	listing, code := f.execInSession(sessionID, []string{"/bin/ls", "-ln", spikeGenerationTarget})
	require.Zero(f.t, code, "the session container must list its mounted generation: %s", listing)
	f.fact("session view of the mounted channel:\n    %s", strings.ReplaceAll(strings.TrimSpace(listing), "\n", "\n    "))

	f.requireNonceFromSession(sessionID, "the Sysbox session container")
	f.pass("R2", fmt.Sprintf(
		"a %04o socket owned by host %d:%d and created by the sidecar was connectable from the Sysbox session as %s, and the nonce crossed both hops",
		info.Mode().Perm(), stat.Uid, stat.Gid, f.identity))

	// This pair is finished; kill it so requirement 3 starts from a clean project runtime parent.
	require.NoError(f.t, f.docker.ContainerKill(f.ctx, sessionID, "KILL"), "the reachability session must be killable")
	f.events.await(sidecarName, "cleanup-done")
	f.awaitGone(sidecarName)
	f.awaitGone(sessionName)
}

// phaseArbitrationAndLifetime settles requirements 3 and 4.
func (f *spikeFixture) phaseArbitrationAndLifetime() {
	f.t.Helper()
	helper := filepath.Join(f.t.TempDir(), "host-mcp-sidecar-spike")
	build := exec.Command("go", "build", "-o", helper, "./cmd/host-mcp-sidecar-spike")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	output, err := build.CombinedOutput()
	require.NoError(f.t, err, "the host launcher helper must build: %s", output)

	f.arbitration(helper)
	f.launcherDeath(helper)
}

// arbitration runs two synchronized child launchers against one deterministic session name.
func (f *spikeFixture) arbitration(helper string) {
	f.t.Helper()
	sessionName := "codex-safe-session-race-" + f.runID
	f.t.Cleanup(func() {
		removeCtx, cancel := context.WithTimeout(context.Background(), spikeWatchdog)
		defer cancel()
		_ = f.docker.ContainerRemove(removeCtx, sessionName, container.RemoveOptions{Force: true})
	})

	// A real barrier: neither attempt proceeds to create until both are ready to.
	barrier, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(f.t, err, "the create barrier must bind loopback")
	defer barrier.Close()
	arrived := make(chan net.Conn, 2)
	go func() {
		for {
			connection, err := barrier.Accept()
			if err != nil {
				return
			}
			arrived <- connection
		}
	}()

	reportDir := f.t.TempDir()
	attempts := []string{"a", "b"}
	reports := make([]string, 0, len(attempts))
	generations := make([]string, 0, len(attempts))
	sidecars := make([]string, 0, len(attempts))
	waits := make([]chan error, 0, len(attempts))

	for _, attempt := range attempts {
		generation := "g-" + spikeNonce()
		sidecar := "codex-safe-mcp-race-" + generation
		report := filepath.Join(reportDir, attempt+".report")
		process := exec.Command(helper, "launcher-helper",
			"--barrier", barrier.Addr().String(),
			"--host-parent", f.parent,
			"--generation", generation,
			"--sidecar-name", sidecar,
			"--session-name", sessionName,
			"--image", f.imageID,
			"--endpoint", f.sentinel.address(),
			"--events", f.events.address(),
			"--label", f.label,
			"--result", report,
			"--readiness", spikeWatchdog.String(),
		)
		var combined bytes.Buffer
		process.Stdout = &combined
		process.Stderr = &combined
		require.NoError(f.t, process.Start(), "launcher attempt %q must start", attempt)
		done := make(chan error, 1)
		go func() { done <- process.Wait() }()
		reports = append(reports, report)
		generations = append(generations, generation)
		sidecars = append(sidecars, sidecar)
		waits = append(waits, done)
	}

	// Release both attempts only once both have reached the barrier.
	held := make([]net.Conn, 0, len(attempts))
	for range attempts {
		select {
		case connection := <-arrived:
			held = append(held, connection)
		case <-time.After(spikeWatchdog):
			f.t.Fatal("both launcher attempts never reached the create barrier within the watchdog")
		}
	}
	for _, connection := range held {
		_, err := fmt.Fprintln(connection, "go")
		require.NoError(f.t, err, "the create barrier must release every attempt")
		_ = connection.Close()
	}

	for index, done := range waits {
		select {
		case err := <-done:
			require.NoError(f.t, err, "launcher attempt %q must exit cleanly", attempts[index])
		case <-time.After(2 * spikeWatchdog):
			f.t.Fatalf("launcher attempt %q never returned", attempts[index])
		}
	}

	outcomes := map[string]int{}
	winnerIndex := -1
	for index, report := range reports {
		values := parseReport(f.t, report)
		outcomes[values["outcome"]]++
		if values["outcome"] == "winner" {
			winnerIndex = index
		}
	}
	require.Equal(f.t, 1, outcomes["winner"], "exactly one attempt must win the session-name race")
	require.Equal(f.t, 1, outcomes["loser"], "exactly one attempt must lose the session-name race")
	require.NotEqual(f.t, -1, winnerIndex, "the winning attempt must be identified")

	sessions := f.listRole("session")
	require.Len(f.t, sessions, 1, "exactly one session container must exist once both attempts return")
	require.Equal(f.t, "running", sessions[0].State, "the winner's session container must be inspectably running")

	// The loser's sidecar is transient by construction; only the winner's survives.
	f.awaitGone(sidecars[1-winnerIndex])
	remaining := f.listRole("sidecar")
	require.Len(f.t, remaining, 1, "exactly one sidecar must remain once both attempts return")
	require.Equal(f.t, "/"+sidecars[winnerIndex], remaining[0].Names[0], "the surviving sidecar must be the winner's")

	require.NoDirExists(f.t, f.generationPath(generations[1-winnerIndex]), "the loser must remove only its own generation")
	require.DirExists(f.t, f.generationPath(generations[winnerIndex]), "the winner's generation must survive")
	f.requireNonceFromHost(generations[winnerIndex], "the winner's channel")

	f.pass("R3", fmt.Sprintf(
		"two synchronized launchers settled on one session container %q; the loser stopped only its own sidecar and removed only generation %s, leaving one sidecar %q and generation %s",
		sessionName, generations[1-winnerIndex], sidecars[winnerIndex], generations[winnerIndex]))

	// Tear the winner down so requirement 4 starts clean.
	require.NoError(f.t, f.docker.ContainerKill(f.ctx, sessionName, "KILL"), "the race winner's session must be killable")
	f.events.await(sidecars[winnerIndex], "cleanup-done")
	f.awaitGone(sidecars[winnerIndex])
	f.awaitGone(sessionName)
}

// launcherDeath proves the sidecar, its lease, and the data path outlive the creating launcher.
func (f *spikeFixture) launcherDeath(helper string) {
	f.t.Helper()
	generation := "g-" + spikeNonce()
	sidecarName := "codex-safe-mcp-detach-" + generation
	sessionName := "codex-safe-session-detach-" + f.runID
	report := filepath.Join(f.t.TempDir(), "detach.report")
	f.t.Cleanup(func() {
		removeCtx, cancel := context.WithTimeout(context.Background(), spikeWatchdog)
		defer cancel()
		_ = f.docker.ContainerRemove(removeCtx, sessionName, container.RemoveOptions{Force: true})
	})

	process := exec.Command(helper, "launcher-helper",
		"--host-parent", f.parent,
		"--generation", generation,
		"--sidecar-name", sidecarName,
		"--session-name", sessionName,
		"--image", f.imageID,
		"--endpoint", f.sentinel.address(),
		"--events", f.events.address(),
		"--label", f.label,
		"--result", report,
		"--readiness", spikeWatchdog.String(),
	)
	var combined bytes.Buffer
	process.Stdout = &combined
	process.Stderr = &combined
	require.NoError(f.t, process.Run(), "the creating launcher must hand off and exit cleanly: %s", combined.String())

	values := parseReport(f.t, report)
	require.Equal(f.t, "winner", values["outcome"], "the only launcher must create the session")
	require.True(f.t, process.ProcessState.Exited(), "the creating launcher must have exited")

	// The launcher is gone. Everything it created must still be alive and serving.
	sessionID := f.inspect(sessionName).ID
	require.Equal(f.t, "running", f.inspect(sidecarName).State.Status, "the sidecar must outlive its launcher")
	require.Equal(f.t, "running", f.inspect(sessionName).State.Status, "the session must outlive its launcher")
	ready, err := f.probe(generation)
	require.NoError(f.t, err, "the channel must still answer a readiness probe after the launcher exits")
	require.True(f.t, ready, "the lease must still be held after the launcher exits")
	f.requireNonceFromHost(generation, "the channel after launcher exit")
	f.requireNonceFromSession(sessionID, "the session after launcher exit")

	f.pass("R4", fmt.Sprintf(
		"child launcher pid %d exited after handoff; sidecar %q and session %q stayed running, the lease stayed held, and the session still reached the host sentinel",
		process.ProcessState.Pid(), sidecarName, sessionName))

	require.NoError(f.t, f.docker.ContainerKill(f.ctx, sessionName, "KILL"), "the detached session must be killable")
	f.events.await(sidecarName, "cleanup-done")
	f.awaitGone(sidecarName)
	f.awaitGone(sessionName)
}

// phaseClosureRecoveryAndIsolation settles requirements 5, 6 and 7.
func (f *spikeFixture) phaseClosureRecoveryAndIsolation() {
	f.t.Helper()
	f.leaseClosure()
	f.sidecarRecovery()
	f.generationIsolation()
}

// leaseClosure proves lease EOF is what makes the sidecar exit and clean up, with no Docker access.
func (f *spikeFixture) leaseClosure() {
	f.t.Helper()
	generation, sidecarName, sidecarID, sessionName, sessionID := f.startPair("closure")
	sibling := f.newGeneration()

	require.Len(f.t, f.inspect(sidecarID).Mounts, 1, "the sidecar cleans up with only its runtime-parent mount and no Docker socket")

	cancel, messages, errs := f.subscribe(sidecarID)
	defer cancel()

	killedAt := time.Now()
	require.NoError(f.t, f.docker.ContainerKill(f.ctx, sessionID, "KILL"), "the session must be killable")
	closedAt := f.events.await(sidecarName, "lease-eof").at
	f.events.await(sidecarName, "cleanup-done")
	diedAt := f.awaitDockerEvent(messages, errs, "die")
	f.awaitGone(sidecarName)
	f.awaitGone(sessionName)

	closure := f.elapsed("kill-to-lease-EOF", killedAt, closedAt)
	exit := f.elapsed("lease-EOF-to-sidecar-exit", closedAt, diedAt)

	require.NoDirExists(f.t, f.generationPath(generation), "lease EOF must remove the sidecar's own generation")
	require.DirExists(f.t, f.generationPath(sibling), "lease EOF must not touch an unrelated generation")
	require.NoError(f.t, os.RemoveAll(f.generationPath(sibling)), "the probe generation must be removable")

	f.pass("R5", fmt.Sprintf(
		"docker kill of the session closed the lease in %s; the sidecar exited %s later, Docker removed it through --rm, and only its own generation %s disappeared",
		closure.Round(time.Millisecond), exit.Round(time.Millisecond), generation))
}

// sidecarRecovery proves a replacement sidecar reattaches to a live session under the same name, from
// the session's immutable image ID, through the unchanged generation inode.
func (f *spikeFixture) sidecarRecovery() {
	f.t.Helper()
	generation, sidecarName, sidecarID, sessionName, sessionID := f.startPair("recover")
	defer func() {
		require.NoError(f.t, f.docker.ContainerKill(f.ctx, sessionName, "KILL"), "the recovery session must be killable")
		f.awaitGone(sessionName)
	}()

	sessionImageID := f.inspect(sessionID).Image
	require.Equal(f.t, f.imageID, sessionImageID, "the session records the immutable image it was created from")
	beforeDevice, beforeInode := f.directoryIdentity(f.generationPath(generation))
	f.fact("generation %s before restart: device=%d inode=%d", generation, beforeDevice, beforeInode)

	// Move only the run-specific mutable tag, so recovery must not consult it.
	require.NoError(f.t, f.docker.ImageTag(f.ctx, f.baseImageID, f.mutableTag), "the run-specific mutable tag must be movable")
	movedTarget := f.resolveTag(f.mutableTag)
	require.Equal(f.t, f.baseImageID, movedTarget, "the mutable tag must now resolve to a different image")
	require.NotEqual(f.t, sessionImageID, movedTarget, "the mutable tag must no longer resolve to the session's image")
	f.fact("mutable tag %s moved from %s to %s", f.mutableTag, sessionImageID, movedTarget)

	cancel, messages, errs := f.subscribe(sidecarID)
	defer cancel()

	stoppedAt := time.Now()
	require.NoError(f.t, f.docker.ContainerStop(f.ctx, sidecarID, container.StopOptions{}),
		"the sidecar must stop gracefully")
	diedAt := f.awaitDockerEvent(messages, errs, "die")
	f.events.await(sidecarName, "signal-after-lease")

	// The replacement is a second relay process under one deterministic container name, so it reports
	// under its own event instance and this scenario cannot match the first relay's transitions.
	replacementInstance := sidecarName + "#replacement"

	// Immediately attempt to create, but not start, the replacement under the same name. A name
	// conflict here is Docker's asynchronous --rm removal, not a competing owner.
	firstOutcome := "created without a name conflict"
	replacementID, err := f.tryCreateSidecar(sidecarName, replacementInstance, generation, sessionImageID)
	if err != nil {
		require.True(f.t, isSpikeNameConflict(err), "only a name conflict may fail the first replacement create: %v", err)
		firstOutcome = "conflicted on the name still held by the departing sidecar, then created on the single permitted retry after its destroy event"
		f.awaitDockerEvent(messages, errs, "destroy")
		replacementID, err = f.tryCreateSidecar(sidecarName, replacementInstance, generation, sessionImageID)
		require.NoError(f.t, err, "the single permitted retry must create the replacement")
	}
	createdAt := time.Now()
	f.elapsed("sidecar die-to-name-creatable", diedAt, createdAt)
	f.fact("first same-name replacement create: %s", firstOutcome)

	// The generation must be intact and unpolluted before the replacement starts.
	require.NoFileExists(f.t, f.controlPath(generation), "a signalled sidecar removes control.sock")
	require.NoFileExists(f.t, f.endpointPath(generation), "a signalled sidecar removes its endpoint sockets")
	afterDevice, afterInode := f.directoryIdentity(f.generationPath(generation))
	require.Equal(f.t, beforeDevice, afterDevice, "the generation device must survive the sidecar restart")
	require.Equal(f.t, beforeInode, afterInode, "the generation inode must survive the sidecar restart")

	startedAt := f.start(replacementID, "replacement sidecar")
	leased := f.events.await(replacementInstance, "lease-established")
	f.requireReady(generation)
	f.requireNonceFromSession(sessionID, "the live session after sidecar recovery")
	restoredAt := time.Now()

	replacement := f.inspect(replacementID)
	require.Equal(f.t, sessionImageID, replacement.Image, "the replacement sidecar uses the session's immutable image ID")
	require.Equal(f.t, "/"+sidecarName, replacement.Name, "the replacement sidecar reuses the deterministic name")
	require.Equal(f.t, sessionImageID, f.inspect(sessionID).Image, "the live session is never replaced during recovery")
	require.Equal(f.t, movedTarget, f.resolveTag(f.mutableTag), "the mutable tag still resolves to the other image")

	stillDevice, stillInode := f.directoryIdentity(f.generationPath(generation))
	require.Equal(f.t, beforeDevice, stillDevice, "the generation device is unchanged after recovery")
	require.Equal(f.t, beforeInode, stillInode, "the generation inode is unchanged after recovery")
	f.fact("generation %s after recovery: device=%d inode=%d", generation, stillDevice, stillInode)

	f.elapsed("replacement-create-to-start", createdAt, startedAt)
	sinceLease := f.elapsed("replacement-start-to-first-successful-lease", startedAt, leased.at)
	f.elapsed("replacement-create-to-first-successful-lease", createdAt, leased.at)
	f.elapsed("lease-to-restored-data-path", leased.at, restoredAt)

	// The replacement waits on a lease only the long-running session can open, so the retry gap the
	// session actually observed is the interval the design's inequality must dominate.
	gap, steps := f.leaseRetryGap(sessionID, stoppedAt)
	require.GreaterOrEqual(f.t, steps, 2, "the session must have recorded a lease loss and its retry")
	f.timing("observed lease retry gap: %s across %d recorded lease-loop steps", gap.Round(time.Millisecond), steps)
	require.Less(f.t, gap, spikeWatchdog, "the observed retry gap must fall inside the replacement's initial-lease bound")
	f.timing("initial-lease failure bound used by the replacement: %s (spike-only watchdog)", spikeWatchdog)

	f.pass("R6", fmt.Sprintf(
		"a graceful stop preserved generation %s at device=%d inode=%d and removed only its sockets; the replacement %s, ran under the same name from the session's image %s, was leased %s after start and well inside the %s bound, and restored the data path without replacing the session",
		generation, beforeDevice, beforeInode, firstOutcome, spikeShortID(sessionImageID),
		sinceLease.Round(time.Millisecond), spikeWatchdog))
}

func isSpikeNameConflict(err error) bool {
	return err != nil && strings.Contains(err.Error(), "is already in use by container")
}

func spikeShortID(id string) string {
	trimmed := strings.TrimPrefix(id, "sha256:")
	if len(trimmed) > 12 {
		return trimmed[:12]
	}
	return trimmed
}

// generationIsolation proves a departing sidecar's cleanup cannot reach the generation of the
// session that replaced it, even though it mounts their shared parent.
func (f *spikeFixture) generationIsolation() {
	f.t.Helper()
	oldGeneration, oldSidecar, _, oldSession, oldSessionID := f.startPair("oldgen")

	// Hold the departing sidecar exactly at lease EOF, before it removes anything.
	release := f.events.gate(oldSidecar, "lease-eof")
	require.NoError(f.t, f.docker.ContainerKill(f.ctx, oldSessionID, "KILL"), "the old session must be killable")
	f.events.await(oldSidecar, "lease-eof")
	f.awaitGone(oldSession)

	// While the old sidecar is held, a newer sibling generation is created and proven to serve.
	newGeneration, newSidecar, _, newSession, newSessionID := f.startPair("newgen")
	f.requireNonceFromSession(newSessionID, "the newer sibling channel before the old cleanup runs")
	require.DirExists(f.t, f.generationPath(oldGeneration), "the old generation must still exist while its cleanup is held")

	release()
	f.events.await(oldSidecar, "cleanup-done")
	f.awaitGone(oldSidecar)

	require.NoDirExists(f.t, f.generationPath(oldGeneration), "the departing sidecar must remove its own generation")
	require.DirExists(f.t, f.generationPath(newGeneration), "the departing sidecar must not remove a newer sibling generation")
	require.DirExists(f.t, f.parent, "the departing sidecar must never remove the parent it mounts")
	require.Equal(f.t, "running", f.inspect(newSidecar).State.Status, "the newer sidecar must be unaffected")
	f.requireNonceFromHost(newGeneration, "the newer sibling channel after the old cleanup ran")
	f.requireNonceFromSession(newSessionID, "the newer sibling session after the old cleanup ran")
	f.events.requireAbsent(newSidecar, "lease-eof")

	f.pass("R7", fmt.Sprintf(
		"a stopped session's sidecar released at lease EOF removed only generation %s; the newer sibling generation %s kept its directory, its sidecar %q, and its two-hop nonce path",
		oldGeneration, newGeneration, newSidecar))

	require.NoError(f.t, f.docker.ContainerKill(f.ctx, newSessionID, "KILL"), "the newer session must be killable")
	f.events.await(newSidecar, "cleanup-done")
	f.awaitGone(newSidecar)
	f.awaitGone(newSession)
	require.NoDirExists(f.t, f.generationPath(newGeneration), "the newer generation is removed with its own session")
}
