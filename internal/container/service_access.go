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
		return fmt.Errorf("decode session default route: %w", err)
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

	rules := [][]string{
		{"INPUT", "-i", device, "-s", source, "-d", "127.0.0.1/32", "-p", "tcp",
			"-m", "conntrack", "--ctstate", "DNAT", "-j", "SNAT", "--to-source", "127.0.0.1"},
		{"PREROUTING", "-i", device, "-s", source, "-m", "addrtype", "--dst-type", "LOCAL", "-p", "tcp",
			"-j", "DNAT", "--to-destination", "127.0.0.1"},
	}
	for _, rule := range rules {
		arguments := append([]string{"-w", "2", "-t", "nat", "-A"}, rule...)
		if output, err := runner.CombinedOutput(ctx, "iptables", arguments...); err != nil {
			return fmt.Errorf("install session NAT %s rule: %w: %s", rule[0], err, strings.TrimSpace(string(output)))
		}
	}
	return nil
}
