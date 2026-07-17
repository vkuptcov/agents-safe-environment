package smoke_test

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/stretchr/testify/require"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher"
)

// hostMCPSentinel is a host loopback server standing in for a Codex MCP server. It binds 127.0.0.1
// only, so a container without the two-hop forward cannot reach it, and answers one request line
// with one response line so the byte path is assertable from inside the container.
type hostMCPSentinel struct {
	listener net.Listener
	response string
}

func newHostMCPSentinel(t *testing.T) *hostMCPSentinel {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "the host MCP sentinel must bind loopback")
	sentinel := &hostMCPSentinel{
		listener: listener,
		response: fmt.Sprintf("mcp-%d", time.Now().UnixNano()%1_000_000),
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer connection.Close()
				_ = connection.SetDeadline(time.Now().Add(30 * time.Second))
				if _, err := bufio.NewReader(connection).ReadString('\n'); err != nil {
					return
				}
				_, _ = fmt.Fprintf(connection, "%s\n", sentinel.response)
			}()
		}
	}()
	return sentinel
}

func (sentinel *hostMCPSentinel) port() string {
	_, port, _ := net.SplitHostPort(sentinel.listener.Addr().String())
	return port
}

// writeCodexConfig writes a config.toml into the fixture's Codex home with one loopback MCP server.
func writeCodexConfig(t *testing.T, codexHome, port string) {
	t.Helper()
	config := fmt.Sprintf("[mcp_servers.smoke]\nurl = \"http://127.0.0.1:%s/mcp\"\n", port)
	require.NoError(t, os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(config), 0o600),
		"the Codex config.toml must be written")
}

// probeMCP is the container command: connect to the exact loopback address the URL names, using
// bash's own /dev/tcp so no extra package is required, and record the sentinel's reply.
//
// The connection runs in a subshell. A failed /dev/tcp redirect is a fatal error for the shell that
// runs it, so isolating it in `( ... )` keeps a refused connection -- the expected --no-host-mcp
// outcome -- from killing the whole script before it records the result and waits for release.
func probeMCP(port string) string {
	return fmt.Sprintf(`reply=$( (exec 3<>/dev/tcp/127.0.0.1/%[1]s && printf 'ping\n' >&3 && IFS= read -r line <&3 && printf '%%s' "$line") 2>/dev/null )
if [[ -n "$reply" ]]; then
  printf 'result=%%s\n' "$reply" > "$1"
else
  printf 'result=unreachable\n' > "$1"
fi
: > "$2"
while [[ ! -e "$3" ]]; do sleep 1; done`, port)
}

// startAgentsRaw runs agents-safe with arbitrary leading arguments, so a test can place --no-host-mcp
// before the project separator.
func (launcher *launcherHarness) startAgentsRaw(arguments ...string) *launcherProcess {
	launcher.t.Helper()
	process := exec.Command(launcher.agentsBinary, arguments...)
	process.Env = launcher.launcherEnv(nil)
	running := &launcherProcess{command: process, done: make(chan struct{})}
	process.Stdout = &running.stdout
	process.Stderr = &running.stderr
	require.NoError(launcher.t, process.Start(), "agents-safe must start: %v", arguments)
	go func() {
		running.err = process.Wait()
		close(running.done)
	}()
	return running
}

// TestSysboxHostMCPForwardsLoopbackServer is the decisive end-to-end proof: an enabled loopback MCP
// server in the base config.toml answers inside the container at the same address its URL names,
// through the container forwarder and the confined host relay sidecar.
func TestSysboxHostMCPForwardsLoopbackServer(t *testing.T) {
	if os.Getenv(goSmokeEnv) != "1" {
		t.Skipf("set %s=1 to run the real Sysbox host-MCP test", goSmokeEnv)
	}
	fixture := newSmokeFixture(t)
	sentinel := newHostMCPSentinel(t)
	writeCodexConfig(t, fixture.project.codexHome, sentinel.port())
	t.Cleanup(func() { fixture.removeHostMCPSidecars() })

	report := filepath.Join(fixture.project.worktree, "host-mcp.report")
	ready := filepath.Join(fixture.project.worktree, "host-mcp.ready")
	release := filepath.Join(fixture.project.worktree, "host-mcp.release")
	command := fixture.launcher.startAgents(
		fixture.project.worktree,
		"bash", "-c", probeMCP(sentinel.port()), "bash", report, ready, release,
	)
	fixture.waitForFile(ready, command)

	observed := parseReport(t, report)
	require.Equal(t, sentinel.response, observed["result"],
		"the container must reach the host MCP server at the address its URL names, through both hops")

	// Security gate: the sidecar shares only the host network namespace and is otherwise fully
	// confined. Every other property is what keeps this one boundary widening from becoming more.
	sidecar := fixture.requireOneHostMCPSidecar()
	require.Equal(t, "host", string(sidecar.HostConfig.NetworkMode), "the sidecar shares the host network namespace")
	require.True(t, sidecar.HostConfig.ReadonlyRootfs, "the sidecar has a read-only root filesystem")
	require.Contains(t, []string(sidecar.HostConfig.CapDrop), "ALL", "the sidecar drops every capability")
	require.Contains(t, sidecar.HostConfig.SecurityOpt, "no-new-privileges", "the sidecar carries no-new-privileges")
	require.False(t, sidecar.HostConfig.Privileged, "the sidecar is never privileged")
	require.NotEqual(t, "sysbox-runc", sidecar.HostConfig.Runtime, "the sidecar takes the Docker default runtime")
	require.Len(t, sidecar.Mounts, 1, "the sidecar receives exactly one mount")
	for _, mount := range sidecar.Mounts {
		require.NotContains(t, mount.Source, "docker.sock", "the sidecar never receives a Docker socket")
	}
	require.Equal(t, "true", sidecar.Config.Labels["codex-safe.host-mcp-sidecar"], "the sidecar carries its role label")
	require.NotEqual(t, "true", sidecar.Config.Labels["codex-safe.managed"],
		"the sidecar must not carry the managed label reserved for session containers")

	// The session container itself is unchanged: it shares no host namespace and stays under Sysbox.
	session := fixture.docker.inspectContainer()
	require.NotEqual(t, "host", string(session.HostConfig.NetworkMode),
		"the session container never shares the host network")
	require.Equal(t, "sysbox-runc", session.HostConfig.Runtime, "the session container still runs under Sysbox")

	fixture.release(release, command, "host-MCP forwarding command")
	fixture.docker.waitForContainerRemoval()
}

// TestSysboxHostMCPNoHostMCPMakesSentinelUnreachable proves --no-host-mcp forwards nothing: the same
// loopback sentinel is unreachable from inside the container, and no sidecar is created.
func TestSysboxHostMCPNoHostMCPMakesSentinelUnreachable(t *testing.T) {
	if os.Getenv(goSmokeEnv) != "1" {
		t.Skipf("set %s=1 to run the real Sysbox host-MCP test", goSmokeEnv)
	}
	fixture := newSmokeFixture(t)
	sentinel := newHostMCPSentinel(t)
	writeCodexConfig(t, fixture.project.codexHome, sentinel.port())
	t.Cleanup(func() { fixture.removeHostMCPSidecars() })

	report := filepath.Join(fixture.project.worktree, "no-host-mcp.report")
	ready := filepath.Join(fixture.project.worktree, "no-host-mcp.ready")
	release := filepath.Join(fixture.project.worktree, "no-host-mcp.release")
	command := fixture.launcher.startAgentsRaw(
		"--no-host-mcp", "--project", fixture.project.worktree, "--image", goSmokeImage, "--",
		"bash", "-c", probeMCP(sentinel.port()), "bash", report, ready, release,
	)
	fixture.waitForFile(ready, command)

	observed := parseReport(t, report)
	require.Equal(t, "unreachable", observed["result"],
		"--no-host-mcp must leave the host MCP server unreachable from the container")
	require.Empty(t, fixture.listHostMCPSidecars(), "--no-host-mcp must create no relay sidecar")

	fixture.release(release, command, "no-host-mcp command")
	fixture.docker.waitForContainerRemoval()
}

func (fixture *smokeFixture) requireOneHostMCPSidecar() container.InspectResponse {
	fixture.t.Helper()
	var sidecar container.InspectResponse
	require.Eventually(fixture.t, func() bool {
		ids := fixture.listHostMCPSidecars()
		if len(ids) != 1 {
			return false
		}
		inspection, err := fixture.docker.client.ContainerInspect(fixture.docker.ctx, ids[0])
		if err != nil {
			return false
		}
		sidecar = inspection
		return true
	}, 30*time.Second, 100*time.Millisecond, "exactly one host-MCP sidecar must be running")
	return sidecar
}

// listHostMCPSidecars returns only this fixture's own sidecars, filtered by the project path and
// host UID it was created for. A test must never touch another project's forwarding session, which
// may be live on a shared developer machine.
func (fixture *smokeFixture) listHostMCPSidecars() []string {
	fixture.t.Helper()
	items, err := fixture.docker.client.ContainerList(fixture.docker.ctx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", "codex-safe.host-mcp-sidecar=true"),
			filters.Arg("label", "codex-safe.project-path="+fixture.project.worktree),
			filters.Arg("label", "codex-safe.host-uid="+strconv.Itoa(os.Getuid())),
		),
	})
	require.NoError(fixture.t, err, "host-MCP sidecars must be listable")
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

// removeHostMCPSidecars removes only this fixture's own sidecars and only the channel directories
// under this project's key. It never removes the shared channel root, which belongs to every project
// of the invoking user.
func (fixture *smokeFixture) removeHostMCPSidecars() {
	for _, id := range fixture.listHostMCPSidecars() {
		_ = fixture.docker.client.ContainerRemove(fixture.docker.ctx, id, container.RemoveOptions{Force: true})
	}
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		projectKey := launcher.ProjectKey(os.Getuid(), fixture.project.worktree)
		_ = os.RemoveAll(filepath.Join(runtimeDir, "codex-safe", projectKey))
	}
}
