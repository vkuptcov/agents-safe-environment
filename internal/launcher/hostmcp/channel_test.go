package hostmcp_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/hostmcp"
)

// shortRuntimeDir returns a runtime directory with a short path, owned by the invoking user, so a
// long Go test name cannot push a socket path over the sockaddr_un limit the channel guards.
func shortRuntimeDir(t *testing.T) (string, func(string) (string, bool)) {
	t.Helper()
	dir, err := os.MkdirTemp("", "cs")
	require.NoError(t, err, "runtime dir must be created")
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	lookup := func(name string) (string, bool) {
		if name == "XDG_RUNTIME_DIR" {
			return dir, true
		}
		return "", false
	}
	return dir, lookup
}

func TestNewChannelAllocatesAFreshPrivateGeneration(t *testing.T) {
	runtimeDir, lookup := shortRuntimeDir(t)
	first, err := hostmcp.NewChannel(lookup, "0123456789abcdef01234567", 1)
	require.NoError(t, err, "a channel must allocate")
	second, err := hostmcp.NewChannel(lookup, "0123456789abcdef01234567", 1)
	require.NoError(t, err, "a second channel must allocate")

	require.NotEqual(t, first.Generation, second.Generation,
		"every session creation gets a fresh generation; a directory is never reused")
	require.Equal(t, first.Parent, second.Parent, "successive sessions for one project share the parent")
	require.DirExists(t, first.Generation, "the generation directory is created before either container")

	info, err := os.Stat(first.Generation)
	require.NoError(t, err, "the generation must be inspectable")
	require.Equal(t, os.FileMode(0o700), info.Mode().Perm(), "the generation is 0700, private to the invoking user")
	require.True(t, strings.HasPrefix(first.Parent, filepath.Join(runtimeDir, "agents-safe")),
		"the channel lives under the runtime directory")
}

func TestNewChannelRequiresAnOwnedRuntimeDir(t *testing.T) {
	t.Run("unset", func(t *testing.T) {
		_, err := hostmcp.NewChannel(func(string) (string, bool) { return "", false }, "key", 1)
		require.Error(t, err, "an unset XDG_RUNTIME_DIR must fail rather than fall back to a shared directory")
		require.Contains(t, err.Error(), "XDG_RUNTIME_DIR")
	})
	t.Run("foreign owner", func(t *testing.T) {
		// /run is root-owned; a non-root test must be refused a channel there.
		if os.Getuid() == 0 {
			t.Skip("root owns /run")
		}
		lookup := func(string) (string, bool) { return "/run", true }
		_, err := hostmcp.NewChannel(lookup, "key", 1)
		require.Error(t, err, "a runtime dir owned by another user must be refused")
		require.Contains(t, err.Error(), "invoking user")
	})
}

// The launcher validates the socket-path budget before any container, because the failure is
// otherwise a confusing bind error at container start.
func TestNewChannelRejectsAnOversizedSocketPath(t *testing.T) {
	deep := filepath.Join(os.TempDir(), strings.Repeat("x", 120))
	require.NoError(t, os.MkdirAll(deep, 0o700), "the deep runtime dir must exist")
	t.Cleanup(func() { _ = os.RemoveAll(deep) })
	lookup := func(name string) (string, bool) {
		if name == "XDG_RUNTIME_DIR" {
			return deep, true
		}
		return "", false
	}
	_, err := hostmcp.NewChannel(lookup, "0123456789abcdef01234567", 1)
	require.Error(t, err, "a socket path over 108 bytes must be rejected at preflight")
	require.Contains(t, err.Error(), "platform limit")
}

func TestChannelEnvironmentEncodesTheSessionView(t *testing.T) {
	set, err := hostmcp.Discover(codexHome(t, `
[mcp_servers.b]
url = "http://localhost:64342/"

[mcp_servers.a]
url = "http://127.0.0.1:8080/"
`))
	require.NoError(t, err, "the set must resolve")

	encoded, err := set.Environment()
	require.NoError(t, err, "the environment must encode")
	// The socket index follows the sorted order, and the session sees its own mount target.
	require.Contains(t, encoded, `"socket":"/run/agents-safe-host-mcp/e0.sock"`, "the first endpoint is e0")
	require.Contains(t, encoded, `"socket":"/run/agents-safe-host-mcp/e1.sock"`, "the second endpoint is e1")
	require.Contains(t, encoded, `"127.0.0.1:8080"`, "the sorted-first endpoint's listener is present")
	require.Contains(t, encoded, `"127.0.0.1:64342"`, "localhost expands to its IPv4 leg")
	require.Contains(t, encoded, `"[::1]:64342"`, "localhost expands to its IPv6 leg")
}

func TestRelayCommandCarriesGenerationAndEndpointsInOrder(t *testing.T) {
	_, lookup := shortRuntimeDir(t)
	set, err := hostmcp.Discover(codexHome(t, `
[mcp_servers.b]
url = "http://localhost:64342/"

[mcp_servers.a]
url = "http://127.0.0.1:8080/"
`))
	require.NoError(t, err, "the set must resolve")
	channel, err := hostmcp.NewChannel(lookup, "0123456789abcdef01234567", len(set.Endpoints))
	require.NoError(t, err, "the channel must allocate")

	command := set.RelayCommand(channel, 60*time.Second)
	require.Equal(t, "relay", command[0], "the command selects the relay mode")
	require.Contains(t, command, "--generation", "the command names the generation")
	require.Contains(t, command, channel.SidecarGeneration(), "the generation is the sidecar's view")
	require.Contains(t, command, "--initial-lease-timeout", "the command carries the initial-lease bound")

	// The endpoints follow the set's sorted order, which fixes each socket index.
	endpoints := endpointArgs(command)
	require.Equal(t, []string{"127.0.0.1:8080", "localhost:64342"}, endpoints,
		"endpoints are passed in the sorted order that fixes their socket index")
}

func TestAdoptChannelReadsARecordedGeneration(t *testing.T) {
	adopted, err := hostmcp.AdoptChannel("/run/user/1000/codex-safe/key/g-abc123")
	require.NoError(t, err, "a recorded generation must be adoptable")
	require.Equal(t, "g-abc123", adopted.Name, "the generation name is the base")
	require.Equal(t, "/run/user/1000/codex-safe/key", adopted.Parent, "the parent is the directory above")
	require.Equal(t, "/run/user/1000/codex-safe/key/g-abc123", adopted.Generation, "the generation is preserved")
}

func TestAdoptChannelRejectsARelativePath(t *testing.T) {
	_, err := hostmcp.AdoptChannel("relative/g-abc")
	require.Error(t, err, "a relative recorded generation must be rejected")
}

func endpointArgs(command []string) []string {
	var endpoints []string
	for index, argument := range command {
		if argument == "--endpoint" && index+1 < len(command) {
			endpoints = append(endpoints, command[index+1])
		}
	}
	return endpoints
}
