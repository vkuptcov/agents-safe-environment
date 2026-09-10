// Package hostmcp resolves the host MCP endpoints a launch forwards into its session container.
//
// It owns reading trusted Codex and Claude host configuration, loopback selection, endpoint
// identity, listener expansion, and collision rejection. It never rewrites the user's configuration
// and never grants host-loopback access from repository-controlled configuration.
package hostmcp

import (
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/vkuptcov/agents-safe-environment/internal/mcpchannel"
)

// AbsentLabel is the diagnostic agents-safe.host-mcp value recorded for a launch that forwards nothing.
const AbsentLabel = "absent"

// localhostName is the one configured host that needs both concrete loopback addresses.
const localhostName = "localhost"

// Endpoint is one host loopback destination the session forwards, identified by the configured host
// exactly as written and lowercased, plus the port. `localhost`, `127.0.0.1`, and `::1` are three
// distinct endpoints and are never folded together: they can name different services, and rewriting
// one to another would answer a question the user did not ask.
type Endpoint struct {
	// Host is the configured loopback host, lowercased and unresolved.
	Host string
	// Port is the configured port, or the URL scheme's default.
	Port int
	// Names are the configured server names that selected this endpoint, sorted. Two servers naming
	// one endpoint produce one Endpoint carrying both names.
	Names []string
}

// Address is the endpoint's canonical host:port form, as the relay dials it.
func (endpoint Endpoint) Address() string {
	return net.JoinHostPort(endpoint.Host, strconv.Itoa(endpoint.Port))
}

// Listen returns the concrete container addresses this endpoint's host requires. A loopback IP
// literal listens on that literal only; only the name localhost expands to both loopback addresses,
// because the container's resolver may answer with either.
func (endpoint Endpoint) Listen() []string {
	port := strconv.Itoa(endpoint.Port)
	if endpoint.Host == localhostName {
		return []string{
			net.JoinHostPort("127.0.0.1", port),
			net.JoinHostPort("::1", port),
		}
	}
	return []string{endpoint.Address()}
}

// SocketName is the endpoint's socket file name, indexed over the sorted endpoint set. It delegates
// to the shared wire contract so the launcher and the relay cannot disagree about a path they both
// depend on across the mount.
func SocketName(index int) string {
	return mcpchannel.SocketName(index)
}

// Set is one launch's canonical, sorted, collision-free endpoint set.
type Set struct {
	// Endpoints are sorted by host then port, which fixes each endpoint's socket index.
	Endpoints []Endpoint
}

// Empty reports whether this launch forwards nothing. An empty set is the zero-cost path: no
// environment variable, no mount, no relay, no listener, and no banner line.
func (set Set) Empty() bool {
	return len(set.Endpoints) == 0
}

// Label returns the diagnostic agents-safe.host-mcp value. It carries the sorted endpoint addresses,
// or AbsentLabel for a launch that forwards nothing.
func (set Set) Label() string {
	if set.Empty() {
		return AbsentLabel
	}
	addresses := make([]string, 0, len(set.Endpoints))
	for _, endpoint := range set.Endpoints {
		addresses = append(addresses, endpoint.Address())
	}
	return strings.Join(addresses, ",")
}

// BannerLines returns one line per endpoint, naming the configured servers and the endpoint they
// reach. A non-empty set is always printed, because this feature widens the security boundary.
func (set Set) BannerLines() []string {
	lines := make([]string, 0, len(set.Endpoints))
	for _, endpoint := range set.Endpoints {
		lines = append(lines, strings.Join(endpoint.Names, ", ")+" -> "+endpoint.Address())
	}
	return lines
}

// sortEndpoints puts the set in its canonical order. The order is load-bearing: it fixes the socket
// index each endpoint is served on, and both containers must agree on it.
func sortEndpoints(endpoints []Endpoint) {
	slices.SortFunc(endpoints, func(first, second Endpoint) int {
		if host := strings.Compare(first.Host, second.Host); host != 0 {
			return host
		}
		return first.Port - second.Port
	})
}

// mergeSets forms one canonical endpoint set from independently parsed product configurations.
// Identical destinations share one relay endpoint and retain every source-qualified server name.
func mergeSets(sets ...Set) (Set, error) {
	byAddress := make(map[string]*Endpoint)
	order := make([]*Endpoint, 0)
	for _, set := range sets {
		for _, candidate := range set.Endpoints {
			if existing, found := byAddress[candidate.Address()]; found {
				existing.Names = append(existing.Names, candidate.Names...)
				continue
			}
			cloned := candidate
			cloned.Names = append([]string(nil), candidate.Names...)
			byAddress[cloned.Address()] = &cloned
			order = append(order, &cloned)
		}
	}
	endpoints := make([]Endpoint, 0, len(order))
	for _, endpoint := range order {
		slices.Sort(endpoint.Names)
		endpoint.Names = slices.Compact(endpoint.Names)
		endpoints = append(endpoints, *endpoint)
	}
	sortEndpoints(endpoints)
	if err := rejectCollisions(endpoints); err != nil {
		return Set{}, err
	}
	return Set{Endpoints: endpoints}, nil
}

// CollisionError reports two endpoints that need one container listener address. They are
// individually valid but select different relay destinations, and one listener cannot serve both.
// Discovery rejects the pair during preflight rather than letting the second bind fail with
// EADDRINUSE and take the whole container down at start.
type CollisionError struct {
	First   Endpoint
	Second  Endpoint
	Address string
}

func (err *CollisionError) Error() string {
	return fmt.Sprintf(
		"host MCP servers %s (%s) and %s (%s) both need container listener %s; "+
			"name both endpoints explicitly or remove one",
		strings.Join(err.First.Names, ", "), err.First.Address(),
		strings.Join(err.Second.Names, ", "), err.Second.Address(),
		err.Address,
	)
}

// rejectCollisions fails when one concrete listener address is claimed by two endpoints. Because
// endpoints are deduplicated by host and port, two distinct endpoints always have different relay
// destinations, so any shared listener address is a genuine contest.
func rejectCollisions(endpoints []Endpoint) error {
	claimed := make(map[string]Endpoint, len(endpoints)*2)
	for _, endpoint := range endpoints {
		for _, address := range endpoint.Listen() {
			if other, found := claimed[address]; found {
				return &CollisionError{First: other, Second: endpoint, Address: address}
			}
			claimed[address] = endpoint
		}
	}
	return nil
}

// isLoopbackHost reports whether a configured host names the loopback the container must reproduce:
// any IP literal in 127.0.0.0/8, the literal ::1, or the name localhost. Any other host is left
// alone, because a public or LAN URL already works through normal outbound access.
func isLoopbackHost(host string) bool {
	if host == localhostName {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

// ParseLabel restores the endpoint set a session container recorded in its agents-safe.host-mcp
// label. It exists for restarting a stopped persistent session whose creation contract the current
// launch does not reproduce: the container still expects exactly these endpoints, in this order,
// so its relay must be rebuilt from the record rather than from the current resolution.
//
// Server names are not recorded, so restored endpoints carry none; only banner lines use them. The
// label order is the sorted order the socket indices depend on, so it is preserved, and the same
// loopback and collision rules that governed creation are re-applied.
func ParseLabel(label string) (Set, error) {
	label = strings.TrimSpace(label)
	if label == "" || label == AbsentLabel {
		return Set{}, nil
	}
	var endpoints []Endpoint
	for _, address := range strings.Split(label, ",") {
		host, portText, err := net.SplitHostPort(address)
		if err != nil {
			return Set{}, fmt.Errorf("recorded host MCP endpoint %q: %w", address, err)
		}
		port, err := strconv.Atoi(portText)
		if err != nil || port < 1 || port > 65535 {
			return Set{}, fmt.Errorf("recorded host MCP endpoint %q has an invalid port", address)
		}
		host = strings.ToLower(host)
		if !isLoopbackHost(host) {
			return Set{}, fmt.Errorf("recorded host MCP endpoint %q is not loopback", address)
		}
		endpoints = append(endpoints, Endpoint{Host: host, Port: port})
	}
	sorted := append([]Endpoint(nil), endpoints...)
	sortEndpoints(sorted)
	if !slices.EqualFunc(sorted, endpoints, func(a, b Endpoint) bool { return a.Address() == b.Address() }) {
		return Set{}, fmt.Errorf("recorded host MCP endpoints %q are not in canonical order", label)
	}
	if err := rejectCollisions(endpoints); err != nil {
		return Set{}, err
	}
	return Set{Endpoints: endpoints}, nil
}
