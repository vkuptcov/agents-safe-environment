package container

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// configureSessionServiceAccess exposes session-local TCP ports to the host without binding them.
// Docker is already ready when this runs, so its PREROUTING jump precedes the fallback DNAT rule.
func configureSessionServiceAccess(
	ctx context.Context,
	runner systemCommandRunner,
	readFile func(string) ([]byte, error),
) error {
	output, err := runner.CombinedOutput(ctx, "ip", "-j", "-4", "route", "show", "default")
	if err != nil {
		return fmt.Errorf("inspect session default route: %w: %s", err, strings.TrimSpace(string(output)))
	}
	var routes []struct {
		Gateway string `json:"gateway"`
		Device  string `json:"dev"`
	}
	if err := json.Unmarshal(output, &routes); err != nil {
		// The runner only offers combined output, so anything ip writes to stderr lands in the
		// document. Carrying the raw bytes keeps that cause visible instead of a bare parse error.
		return fmt.Errorf("decode session default route: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if len(routes) != 1 || routes[0].Gateway == "" || routes[0].Device == "" {
		return fmt.Errorf("service access requires one default IPv4 route via a gateway, got %s", output)
	}
	device, source := routes[0].Device, routes[0].Gateway+"/32"

	sysctlPath := "/proc/sys/net/ipv4/conf/" + device + "/route_localnet"
	setting, err := readFile(sysctlPath)
	if err != nil {
		return fmt.Errorf("read session route_localnet: %w", err)
	}
	if strings.TrimSpace(string(setting)) != "1" {
		return fmt.Errorf("route_localnet is not enabled on %q; Docker endpoint sysctl is required", device)
	}

	rules := []struct {
		name string
		argv []string
	}{
		// route_localnet makes the kernel accept any frame arriving on this device for 127.0.0.0/8
		// and deliver it to a loopback-only listener. That acceptance is independent of the NAT
		// rules below, so without this guard any container sharing the host bridge can reach the
		// session's loopback services, which the martian-destination check used to drop. The
		// translated host flow survives because conntrack has already given it the gateway source.
		{"filter INPUT loopback guard", []string{
			"-t", "filter", "-I", "INPUT", "1",
			"-i", device, "!", "-s", source, "-d", "127.0.0.0/8", "-j", "DROP"}},
		{"nat INPUT source translation", []string{
			"-t", "nat", "-A", "INPUT",
			"-i", device, "-s", source, "-d", "127.0.0.1/32", "-p", "tcp",
			"-m", "conntrack", "--ctstate", "DNAT", "-j", "SNAT", "--to-source", "127.0.0.1"}},
		{"nat PREROUTING destination translation", []string{
			"-t", "nat", "-A", "PREROUTING",
			"-i", device, "-s", source, "-m", "addrtype", "--dst-type", "LOCAL", "-p", "tcp",
			"-j", "DNAT", "--to-destination", "127.0.0.1"}},
	}
	for _, rule := range rules {
		arguments := append([]string{"-w", "2"}, rule.argv...)
		if output, err := runner.CombinedOutput(ctx, "iptables", arguments...); err != nil {
			return fmt.Errorf("install session %s rule: %w: %s", rule.name, err, strings.TrimSpace(string(output)))
		}
	}
	return nil
}
