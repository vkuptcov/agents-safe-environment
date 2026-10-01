package container

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

type serviceAccessRunner struct {
	routes string
	calls  []string
}

func (runner *serviceAccessRunner) CombinedOutput(_ context.Context, name string, arguments ...string) ([]byte, error) {
	key := name + " " + strings.Join(arguments, " ")
	runner.calls = append(runner.calls, key)
	switch {
	case key == "ip -j -4 route show default":
		return []byte(runner.routes), nil
	case strings.HasPrefix(key, "iptables "):
		return nil, nil
	default:
		return nil, fmt.Errorf("unexpected command: %s", key)
	}
}

const serviceAccessRoute = `[{"dst":"default","gateway":"172.17.0.1","dev":"eth0"}]`

func routeLocalnet(value string) func(string) ([]byte, error) {
	return func(string) ([]byte, error) { return []byte(value), nil }
}

func TestServiceAccessInstallsHostScopedRules(t *testing.T) {
	runner := &serviceAccessRunner{routes: serviceAccessRoute}
	readFile := func(path string) ([]byte, error) {
		if path != "/proc/sys/net/ipv4/conf/eth0/route_localnet" {
			t.Fatalf("read unexpected path %q", path)
		}
		return []byte("1\n"), nil
	}
	if err := configureSessionServiceAccess(context.Background(), runner, readFile); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"ip -j -4 route show default",
		"iptables -w 2 -t nat -A INPUT -i eth0 -s 172.17.0.1/32 -d 127.0.0.1/32 -p tcp -m conntrack --ctstate DNAT -j SNAT --to-source 127.0.0.1",
		"iptables -w 2 -t nat -A PREROUTING -i eth0 -s 172.17.0.1/32 -m addrtype --dst-type LOCAL -p tcp -j DNAT --to-destination 127.0.0.1",
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls =\n%q\nwant\n%q", runner.calls, want)
	}
}

func TestServiceAccessRejectsAmbiguousRoute(t *testing.T) {
	runner := &serviceAccessRunner{
		routes: `[{"dst":"default","gateway":"172.17.0.1","dev":"eth0"},{"dst":"default","gateway":"10.0.0.1","dev":"eth1"}]`,
	}
	err := configureSessionServiceAccess(context.Background(), runner, routeLocalnet("1"))
	if err == nil || !strings.Contains(err.Error(), "one default") {
		t.Fatalf("error = %v, want ambiguous route diagnostic", err)
	}
}

func TestServiceAccessRejectsMissingRouteLocalnet(t *testing.T) {
	runner := &serviceAccessRunner{routes: serviceAccessRoute}
	err := configureSessionServiceAccess(context.Background(), runner, routeLocalnet("0"))
	if err == nil || !strings.Contains(err.Error(), "route_localnet") {
		t.Fatalf("error = %v, want route_localnet diagnostic", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("installed rules before verifying route_localnet: %q", runner.calls)
	}
}
