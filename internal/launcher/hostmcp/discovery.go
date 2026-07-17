package hostmcp

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// configFileName is the only file this package reads, directly below the resolved Codex home.
const configFileName = "config.toml"

// configFile is a partial decode of the Codex configuration. Only the base mcp_servers table is
// read; every other key, table, and file is left alone.
type configFile struct {
	MCPServers map[string]serverEntry `toml:"mcp_servers"`
}

// serverEntry is a partial decode of one mcp_servers entry.
//
// It deliberately declares only url and enabled. A stdio server's command and args are left
// undecoded rather than typed: they are irrelevant here, and declaring them would make a
// legitimately-shaped entry fail the whole launch if its type did not match this struct.
type serverEntry struct {
	// URL is empty for a command-based stdio server, which needs no network forwarding.
	URL string `toml:"url"`
	// Enabled is a tri-state defaulting to true. Only an explicit false excludes the entry.
	Enabled *bool `toml:"enabled"`
}

// Discover resolves the forwarded endpoint set from an already-resolved Codex home. An empty
// codexHome means the launch resolved none, which is not an error: the endpoint set is empty and no
// forwarder, mount, or relay exists.
//
// Discovery is fail-closed. It never silently drops a configured endpoint, because forwarding
// nothing would present a broken MCP server as a Codex problem.
func Discover(codexHome string) (Set, error) {
	if strings.TrimSpace(codexHome) == "" {
		return Set{}, nil
	}
	path := filepath.Join(codexHome, configFileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		// An absent config.toml is a user with no MCP servers, not a broken launch.
		return Set{}, nil
	}
	if err != nil {
		return Set{}, fmt.Errorf("read Codex configuration %q: %w", path, err)
	}
	return parse(path, data)
}

func parse(path string, data []byte) (Set, error) {
	var decoded configFile
	if _, err := toml.Decode(string(data), &decoded); err != nil {
		// BurntSushi reports the parser's position, which is what makes this diagnostic useful.
		return Set{}, fmt.Errorf("parse Codex configuration %q: %w", path, err)
	}

	// Iterate in name order so a diagnostic naming two servers is stable across runs.
	names := make([]string, 0, len(decoded.MCPServers))
	for name := range decoded.MCPServers {
		names = append(names, name)
	}
	slices.Sort(names)

	byAddress := make(map[string]*Endpoint, len(names))
	order := make([]*Endpoint, 0, len(names))
	for _, name := range names {
		entry := decoded.MCPServers[name]
		endpoint, selected, err := selectEndpoint(name, entry)
		if err != nil {
			return Set{}, err
		}
		if !selected {
			continue
		}
		// Two servers naming one endpoint produce one endpoint carrying both names.
		if existing, found := byAddress[endpoint.Address()]; found {
			existing.Names = append(existing.Names, name)
			continue
		}
		byAddress[endpoint.Address()] = &endpoint
		order = append(order, &endpoint)
	}

	endpoints := make([]Endpoint, 0, len(order))
	for _, endpoint := range order {
		endpoints = append(endpoints, *endpoint)
	}
	sortEndpoints(endpoints)
	if err := rejectCollisions(endpoints); err != nil {
		return Set{}, err
	}
	return Set{Endpoints: endpoints}, nil
}

// selectEndpoint decides whether one configured server becomes a forwarded endpoint. Not selecting
// is silent; a malformed selection is a launch failure.
func selectEndpoint(name string, entry serverEntry) (Endpoint, bool, error) {
	if entry.Enabled != nil && !*entry.Enabled {
		// Forwarding a server the user switched off would widen the boundary for no benefit.
		return Endpoint{}, false, nil
	}
	if strings.TrimSpace(entry.URL) == "" {
		// A command-based stdio server is started by Codex inside the container.
		return Endpoint{}, false, nil
	}
	parsed, err := url.Parse(entry.URL)
	if err != nil {
		return Endpoint{}, false, fmt.Errorf("mcp_servers.%s has an unparsable url: %w", name, err)
	}
	host := strings.ToLower(parsed.Hostname())
	if !isLoopbackHost(host) {
		// A public host, a LAN host, or a Unix socket already works, or is out of scope.
		return Endpoint{}, false, nil
	}
	port, err := endpointPort(parsed)
	if err != nil {
		return Endpoint{}, false, fmt.Errorf("mcp_servers.%s: %w", name, err)
	}
	return Endpoint{Host: host, Port: port, Names: []string{name}}, true, nil
}

// endpointPort resolves the configured port, defaulting to the URL scheme's own. A loopback URL that
// names no port and no scheme with a default is a fail-closed error rather than a guess.
func endpointPort(parsed *url.URL) (int, error) {
	raw := parsed.Port()
	if raw == "" {
		switch strings.ToLower(parsed.Scheme) {
		case "http":
			return 80, nil
		case "https":
			return 443, nil
		default:
			return 0, fmt.Errorf(
				"url %q names a loopback host but has no port and scheme %q has no default port",
				parsed.Redacted(), parsed.Scheme,
			)
		}
	}
	// url.Parse already rejects a non-numeric port, so only the range is left to check.
	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("url %q names a loopback host with an invalid port %q", parsed.Redacted(), raw)
	}
	return port, nil
}
