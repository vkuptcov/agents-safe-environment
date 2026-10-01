package smoke_test

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestSysboxSessionServiceAccess exercises the host/session and nested-Docker boundaries through
// the public launcher. The in-session services start after the network rules are installed.
func TestSysboxSessionServiceAccess(t *testing.T) {
	if os.Getenv(goSmokeEnv) != "1" {
		t.Skipf("set %s=1 to run the real Sysbox service-access test", goSmokeEnv)
	}
	fixture := newSmokeFixture(t)
	ready := filepath.Join(fixture.project.worktree, "service-access.ready")
	release := filepath.Join(fixture.project.worktree, "service-access.release")
	nestedLoopback := "service-access-loopback"
	nestedWildcard := "service-access-wildcard"
	nestedPrivate := "service-access-private"

	script := `set -euo pipefail
ready=$1; release=$2; image=$3; loopback_name=$4; wildcard_name=$5; private_name=$6
node -e 'const net=require("net"); for (const [host,port] of [["127.0.0.1",18080],["0.0.0.0",18081]]) net.createServer(s=>s.end(s.remoteAddress)).listen(port,host)' &
node_pid=$!
trap 'docker rm -f "$loopback_name" "$wildcard_name" "$private_name" >/dev/null 2>&1 || true; kill "$node_pid" 2>/dev/null || true' EXIT
docker run -d --rm --name "$loopback_name" --hostname "$loopback_name" -p 127.0.0.1:18082:80 "$image" nc -lk -p 80 -e /bin/hostname >/dev/null
docker run -d --rm --name "$wildcard_name" --hostname "$wildcard_name" -p 0.0.0.0:18083:80 "$image" nc -lk -p 80 -e /bin/hostname >/dev/null
docker run -d --rm --name "$private_name" --hostname "$private_name" "$image" nc -lk -p 18084 -e /bin/hostname >/dev/null
: > "$ready"
while [[ ! -e "$release" ]]; do sleep 1; done`
	process := fixture.launcher.start(
		fixture.project.worktree, "bash", "-c", script, "bash",
		ready, release, nestedImage, nestedLoopback, nestedWildcard, nestedPrivate,
	)
	fixture.waitForFile(ready, process)
	defer fixture.release(release, process, "service access command")

	inspection := fixture.docker.inspectContainer()
	bridge := inspection.NetworkSettings.Networks["bridge"]
	require.NotNil(t, bridge, "managed session must use Docker's bridge network")
	address := bridge.IPAddress
	require.NotEmpty(t, address, "managed session must have a bridge IPv4 address")

	for _, probe := range []struct {
		port int
		want string
	}{
		{18080, "127.0.0.1"},
		{18081, "127.0.0.1"},
		{18082, nestedLoopback},
		{18083, nestedWildcard},
	} {
		addressAndPort := net.JoinHostPort(address, strconv.Itoa(probe.port))
		require.Eventually(t, func() bool {
			connection, err := net.DialTimeout("tcp", addressAndPort, 2*time.Second)
			if err != nil {
				return false
			}
			defer connection.Close()
			_ = connection.SetReadDeadline(time.Now().Add(2 * time.Second))
			body, err := io.ReadAll(connection)
			return err == nil && strings.TrimSpace(string(body)) == probe.want
		}, 30*time.Second, 200*time.Millisecond, "host must receive %q from %s", probe.want, addressAndPort)
	}

	// The nested container publishes nothing, so its port stays inside its own namespace. The
	// translation must not invent a path to it.
	private := net.JoinHostPort(address, "18084")
	connection, err := net.DialTimeout("tcp", private, 2*time.Second)
	if err == nil {
		_ = connection.Close()
		t.Fatalf("unpublished nested port answered at %s", private)
	}

	require.Empty(t, inspection.NetworkSettings.Ports, "the session must not publish any host port")
	require.Empty(t, inspection.HostConfig.PortBindings, "the session must not request any host port binding")
	hostSysctl, err := os.ReadFile("/proc/sys/net/ipv4/conf/docker0/route_localnet")
	require.NoError(t, err, "the host bridge sysctl must be readable to prove it stayed off")
	require.Equal(t, "0", strings.TrimSpace(string(hostSysctl)),
		"route_localnet must stay disabled on the host bridge")
}
