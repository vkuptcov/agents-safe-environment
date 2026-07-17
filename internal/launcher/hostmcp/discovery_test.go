package hostmcp_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/hostmcp"
)

// codexHome writes a config.toml into a fresh Codex home and returns it.
func codexHome(t *testing.T, config string) string {
	t.Helper()
	home := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600),
		"test Codex configuration must be written")
	return home
}

// addresses is the endpoint set as canonical host:port strings, in socket-index order.
func addresses(set hostmcp.Set) []string {
	values := make([]string, 0, len(set.Endpoints))
	for _, endpoint := range set.Endpoints {
		values = append(values, endpoint.Address())
	}
	return values
}

func TestDiscoverSelectsLoopbackAndIgnoresPublic(t *testing.T) {
	set, err := hostmcp.Discover(codexHome(t, `
[mcp_servers.idea]
url = "http://127.0.0.1:64342/stream"

[mcp_servers.openaiDeveloperDocs]
url = "https://developers.openai.com/mcp"
`))
	require.NoError(t, err, "a loopback and a public server must resolve")
	require.Equal(t, []string{"127.0.0.1:64342"}, addresses(set), "only the loopback server is forwarded")
	require.Equal(t, []string{"idea"}, set.Endpoints[0].Names, "the endpoint carries its configured name")
}

// The three loopback hosts are distinct endpoints. Rewriting localhost to 127.0.0.1 would make an
// IPv6-only server unreachable, so they are never folded together.
func TestDiscoverKeepsLoopbackHostsDistinct(t *testing.T) {
	set, err := hostmcp.Discover(codexHome(t, `
[mcp_servers.byName]
url = "http://localhost:1000/"

[mcp_servers.byIPv4]
url = "http://127.0.0.1:2000/"

[mcp_servers.byIPv6]
url = "http://[::1]:3000/"
`))
	require.NoError(t, err, "three distinct loopback hosts must resolve")
	require.Equal(t, []string{"127.0.0.1:2000", "[::1]:3000", "localhost:1000"}, addresses(set),
		"localhost, 127.0.0.1 and ::1 stay three endpoints in canonical order")
}

func TestDiscoverSelectsLoopbackLiteralOutside127001(t *testing.T) {
	set, err := hostmcp.Discover(codexHome(t, `
[mcp_servers.resolved]
url = "http://127.0.0.53/mcp"
`))
	require.NoError(t, err, "a 127.0.0.0/8 literal is a loopback host")
	require.Equal(t, []string{"127.0.0.53:80"}, addresses(set), "the http scheme supplies the default port")
	require.Equal(t, []string{"127.0.0.53:80"}, set.Endpoints[0].Listen(),
		"a loopback literal listens on exactly itself")
}

func TestDiscoverAppliesHTTPSDefaultPort(t *testing.T) {
	set, err := hostmcp.Discover(codexHome(t, `
[mcp_servers.secure]
url = "https://localhost/mcp"
`))
	require.NoError(t, err, "an https loopback URL must resolve")
	require.Equal(t, []string{"localhost:443"}, addresses(set), "the https scheme supplies 443")
}

func TestDiscoverEnabledTriState(t *testing.T) {
	set, err := hostmcp.Discover(codexHome(t, `
[mcp_servers.off]
url = "http://127.0.0.1:1000/"
enabled = false

[mcp_servers.on]
url = "http://127.0.0.1:2000/"
enabled = true

[mcp_servers.unset]
url = "http://127.0.0.1:3000/"
`))
	require.NoError(t, err, "a tri-state enabled key must resolve")
	require.Equal(t, []string{"127.0.0.1:2000", "127.0.0.1:3000"}, addresses(set),
		"enabled defaults to true and only an explicit false excludes an entry")
}

// A stdio server's command and args are left undecoded on purpose: typing them would let a
// legitimately-shaped entry fail the whole launch.
func TestDiscoverIgnoresStdioServerWithoutError(t *testing.T) {
	set, err := hostmcp.Discover(codexHome(t, `
[mcp_servers.stdio]
command = "npx"
args = ["-y", "some-server"]

[mcp_servers.idea]
url = "http://127.0.0.1:64342/stream"
`))
	require.NoError(t, err, "a command-based server is not an error")
	require.Equal(t, []string{"127.0.0.1:64342"}, addresses(set), "only the url-based loopback server is forwarded")
}

func TestDiscoverIgnoresUnixSocketURL(t *testing.T) {
	set, err := hostmcp.Discover(codexHome(t, `
[mcp_servers.socket]
url = "unix:///run/some/mcp.sock"
`))
	require.NoError(t, err, "a Unix-socket URL is not an error")
	require.True(t, set.Empty(), "a Unix-socket URL names no loopback host and is not forwarded")
}

func TestDiscoverDeduplicatesAndKeepsBothNames(t *testing.T) {
	set, err := hostmcp.Discover(codexHome(t, `
[mcp_servers.second]
url = "http://127.0.0.1:64342/b"

[mcp_servers.first]
url = "http://127.0.0.1:64342/a"
`))
	require.NoError(t, err, "two servers naming one endpoint must resolve")
	require.Len(t, set.Endpoints, 1, "one endpoint serves both servers")
	require.Equal(t, []string{"first", "second"}, set.Endpoints[0].Names, "both names are kept, in name order")
	require.Equal(t, []string{"first, second -> 127.0.0.1:64342"}, set.BannerLines(),
		"two servers sharing an endpoint are listed on one banner line")
}

func TestDiscoverExpandsLocalhostToBothLoopbackAddresses(t *testing.T) {
	set, err := hostmcp.Discover(codexHome(t, `
[mcp_servers.byName]
url = "http://localhost:64342/"
`))
	require.NoError(t, err, "a localhost endpoint must resolve")
	require.Equal(t, []string{"127.0.0.1:64342", "[::1]:64342"}, set.Endpoints[0].Listen(),
		"only the name localhost expands to both concrete loopback addresses")
}

func TestDiscoverRejectsLocalhostAgainstIPv4OnOnePort(t *testing.T) {
	_, err := hostmcp.Discover(codexHome(t, `
[mcp_servers.byName]
url = "http://localhost:64342/stream"

[mcp_servers.byAddress]
url = "http://127.0.0.1:64342/stream"
`))
	require.Error(t, err, "two endpoints contending for one listener must fail preflight")
	var collision *hostmcp.CollisionError
	require.ErrorAs(t, err, &collision, "the failure must be a named collision")
	require.Equal(t, "127.0.0.1:64342", collision.Address, "the contested address is named")
	require.Contains(t, err.Error(), "byAddress", "the diagnostic names the first server")
	require.Contains(t, err.Error(), "byName", "the diagnostic names the second server")
	require.Contains(t, err.Error(), "localhost:64342", "the diagnostic names both configured endpoints")
}

func TestDiscoverRejectsLocalhostAgainstIPv6OnOnePort(t *testing.T) {
	_, err := hostmcp.Discover(codexHome(t, `
[mcp_servers.byName]
url = "http://localhost:64342/stream"

[mcp_servers.byAddress]
url = "http://[::1]:64342/stream"
`))
	require.Error(t, err, "localhost against an explicit ::1 on one port must fail preflight")
	var collision *hostmcp.CollisionError
	require.ErrorAs(t, err, &collision, "the failure must be a named collision")
	require.Equal(t, "[::1]:64342", collision.Address, "the contested address is the IPv6 leg")
}

// localhost and an explicit literal on different ports do not contend.
func TestDiscoverAllowsLocalhostAndLiteralOnDifferentPorts(t *testing.T) {
	set, err := hostmcp.Discover(codexHome(t, `
[mcp_servers.byName]
url = "http://localhost:64342/stream"

[mcp_servers.byAddress]
url = "http://127.0.0.1:9999/stream"
`))
	require.NoError(t, err, "endpoints on different ports do not contend")
	require.Equal(t, []string{"127.0.0.1:9999", "localhost:64342"}, addresses(set), "both endpoints are forwarded")
}

func TestDiscoverTreatsAbsentConfigAsEmptySet(t *testing.T) {
	set, err := hostmcp.Discover(t.TempDir())
	require.NoError(t, err, "an absent config.toml is not an error")
	require.True(t, set.Empty(), "a user with no Codex configuration forwards nothing")
	require.Equal(t, hostmcp.AbsentLabel, set.Label(), "an empty set records the absent label")
	require.Empty(t, set.BannerLines(), "an empty set prints nothing")
}

// An agents-safe launch that resolved no Codex home has an empty set for the same reason.
func TestDiscoverTreatsAbsentCodexHomeAsEmptySet(t *testing.T) {
	set, err := hostmcp.Discover("")
	require.NoError(t, err, "no resolved Codex home is not an error")
	require.True(t, set.Empty(), "a launch with no Codex home forwards nothing")
}

func TestDiscoverFailsOnUnreadableConfig(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses the unreadable-file check")
	}
	home := codexHome(t, `[mcp_servers.idea]
url = "http://127.0.0.1:64342/"`)
	require.NoError(t, os.Chmod(filepath.Join(home, "config.toml"), 0o000), "config.toml must be made unreadable")
	_, err := hostmcp.Discover(home)
	require.Error(t, err, "an unreadable config.toml must fail the launch rather than forward nothing")
	require.Contains(t, err.Error(), "read Codex configuration", "the diagnostic names the read failure")
}

func TestDiscoverFailsOnTOMLParseError(t *testing.T) {
	_, err := hostmcp.Discover(codexHome(t, `
[mcp_servers.idea
url = "http://127.0.0.1:64342/"
`))
	require.Error(t, err, "a malformed config.toml must fail the launch")
	require.Contains(t, err.Error(), "parse Codex configuration", "the diagnostic names the parse failure")
}

func TestDiscoverFailsOnUnparsableURL(t *testing.T) {
	_, err := hostmcp.Discover(codexHome(t, `
[mcp_servers.broken]
url = "http://[::1/stream"
`))
	require.Error(t, err, "an unparsable url must fail the launch")
	require.Contains(t, err.Error(), "mcp_servers.broken", "the diagnostic names the server")
}

func TestDiscoverFailsOnInvalidLoopbackPort(t *testing.T) {
	_, err := hostmcp.Discover(codexHome(t, `
[mcp_servers.broken]
url = "http://127.0.0.1:99999/stream"
`))
	require.Error(t, err, "a loopback host with an out-of-range port must fail the launch")
	require.Contains(t, err.Error(), "mcp_servers.broken", "the diagnostic names the server")
	require.Contains(t, err.Error(), "invalid port", "the diagnostic names the port problem")
}

// A non-loopback URL with an odd port is left alone: it already works through outbound access.
func TestDiscoverIgnoresInvalidPortOnPublicHost(t *testing.T) {
	set, err := hostmcp.Discover(codexHome(t, `
[mcp_servers.public]
url = "http://example.com:99999/mcp"
`))
	require.NoError(t, err, "a public host is never inspected for a port this feature would not bind")
	require.True(t, set.Empty(), "a public host is not forwarded")
}

func TestDiscoverFailsOnLoopbackSchemeWithoutDefaultPort(t *testing.T) {
	_, err := hostmcp.Discover(codexHome(t, `
[mcp_servers.streamed]
url = "ws://localhost/mcp"
`))
	require.Error(t, err, "a loopback URL whose scheme has no default port must fail rather than guess")
	require.Contains(t, err.Error(), "mcp_servers.streamed", "the diagnostic names the server")
}

func TestSetLabelAndSocketIndexFollowSortedOrder(t *testing.T) {
	set, err := hostmcp.Discover(codexHome(t, `
[mcp_servers.b]
url = "http://localhost:64342/"

[mcp_servers.a]
url = "http://127.0.0.1:8080/"
`))
	require.NoError(t, err, "two endpoints must resolve")
	require.Equal(t, "127.0.0.1:8080,localhost:64342", set.Label(),
		"the compared label carries the sorted endpoint addresses")
	require.Equal(t, "e0.sock", hostmcp.SocketName(0), "socket names are indexed over the sorted set")
	require.Equal(t, "e1.sock", hostmcp.SocketName(1), "socket names are indexed over the sorted set")
	require.Equal(t, []string{"a -> 127.0.0.1:8080", "b -> localhost:64342"}, set.BannerLines(),
		"the banner follows the same sorted order")
}

func TestDiscoverIgnoresUnknownKeys(t *testing.T) {
	set, err := hostmcp.Discover(codexHome(t, `
[mcp_servers.idea]
url = "http://127.0.0.1:64342/stream"
startup_timeout_sec = 30
tool_timeout_sec = 60

[projects."/home/alex/app"]
trust_level = "trusted"
`))
	require.NoError(t, err, "unknown keys and unrelated tables must be ignored")
	require.Equal(t, []string{"127.0.0.1:64342"}, addresses(set), "the endpoint resolves alongside unknown keys")
}
