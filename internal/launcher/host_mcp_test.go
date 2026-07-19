package launcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/dockercli"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/hostmcp"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/projectenv"
)

func testChannel(t *testing.T) hostmcp.Channel {
	t.Helper()
	return hostmcp.Channel{
		Parent:     "/run/user/1000/codex-safe/key",
		Generation: "/run/user/1000/codex-safe/key/g-abc123",
		Name:       "g-abc123",
	}
}

func oneEndpointSet(t *testing.T) hostmcp.Set {
	t.Helper()
	set, err := hostmcp.Discover(writeCodexHome(t, `
[mcp_servers.idea]
url = "http://127.0.0.1:64342/stream"
`))
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	return set
}

// The sidecar's create request is the security surface this feature adds. Every field here is
// asserted because the sidecar gains the host network namespace and nothing else may slip in.
func TestBuildSidecarRequestIsFullyConfined(t *testing.T) {
	t.Parallel()
	docker := hostLauncher(1000, 1001, "/home/developer", "")
	channel := testChannel(t)
	set := oneEndpointSet(t)

	request := docker.buildSidecarRequest(
		sidecarName("key", channel), "sha256:img", channel, set, "/sources/app",
	)
	args, err := dockercli.BuildCreateArgs(request)
	if err != nil {
		t.Fatalf("BuildCreateArgs() error = %v", err)
	}
	joined := strings.Join(args, " ")

	for _, required := range []string{
		"--network=host",
		"--user 1000:1001",
		"--read-only",
		"--cap-drop=ALL",
		"--security-opt=no-new-privileges",
		"--rm",
	} {
		if !strings.Contains(joined, required) {
			t.Errorf("sidecar args missing %q: %#v", required, args)
		}
	}
	for _, forbidden := range []string{"--runtime", "--privileged", "docker.sock", "--workdir"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("sidecar args contain forbidden %q: %#v", forbidden, args)
		}
	}

	// Exactly one mount: the project runtime parent, so the sidecar can remove its generation.
	if got := strings.Count(joined, "--mount"); got != 1 {
		t.Fatalf("sidecar must receive exactly one mount, got %d: %#v", got, args)
	}
	if !strings.Contains(joined, "source="+channel.Parent+",target="+hostmcp.SidecarTarget) {
		t.Errorf("the sidecar's only mount must be the runtime parent: %#v", args)
	}

	// A role marker distinct from codex-safe.managed, which stays reserved for session containers.
	assertLabel(t, args, hostMCPSidecarLabel, "true")
	if strings.Contains(joined, managedLabel+"=true") {
		t.Error("the sidecar must not carry codex-safe.managed=true")
	}
	assertLabel(t, args, hostMCPImageLabel, "sha256:img")
	assertLabel(t, args, hostMCPLabel, set.Label())

	// The command selects relay through the image entrypoint.
	if args[len(args)-len(set.RelayCommand(channel, sidecarInitialLeaseTimeout))] != "relay" {
		t.Errorf("the sidecar command must begin with relay: %#v", args)
	}
}

func TestSidecarNameEmbedsTheGeneration(t *testing.T) {
	t.Parallel()
	channel := testChannel(t)
	name := sidecarName("projkey", channel)
	if name != "codex-safe-mcp-projkey-g-abc123" {
		t.Fatalf("sidecar name = %q", name)
	}
	// Two attempts with different generations compute different sidecar names, so both creates
	// succeed and the session container name stays the sole arbiter.
	other := sidecarName("projkey", hostmcp.Channel{Name: "g-different"})
	if name == other {
		t.Fatal("distinct generations must yield distinct sidecar names")
	}
}

func TestBuildCreateRequestOmitsHostMCPForAnEmptySet(t *testing.T) {
	t.Parallel()
	request, err := hostLauncher(1000, 1001, "/home/developer", "").buildCreateRequest(
		testPlan(), "image", "codex-safe-aba8b4ca4ff345d5d0443c0c", hostMCPPlan{}, "fingerprint",
	)
	if err != nil {
		t.Fatalf("buildCreateRequest() error = %v", err)
	}
	for _, environment := range request.Environment {
		if environment.Key == hostMCPEnv {
			t.Fatalf("an empty set must add no %s environment variable", hostMCPEnv)
		}
	}
	for _, mount := range request.Mounts {
		if mount.Target == hostmcp.SessionTarget {
			t.Fatal("an empty set must add no host MCP mount")
		}
	}
	// The diagnostic label records `absent` for a session that forwards nothing.
	assertLabel := func(want string) {
		for _, label := range request.Labels {
			if label.Key == hostMCPLabel && label.Value == want {
				return
			}
		}
		t.Fatalf("expected %s=%s", hostMCPLabel, want)
	}
	assertLabel(hostmcp.AbsentLabel)
}

func TestBuildCreateRequestAddsHostMCPForANonEmptySet(t *testing.T) {
	t.Parallel()
	set := oneEndpointSet(t)
	forwarding := hostMCPPlan{set: set, channel: testChannel(t), candidate: true}
	request, err := hostLauncher(1000, 1001, "/home/developer", "").buildCreateRequest(
		testPlan(), "image", "codex-safe-aba8b4ca4ff345d5d0443c0c", forwarding, "fingerprint",
	)
	if err != nil {
		t.Fatalf("buildCreateRequest() error = %v", err)
	}

	var env string
	for _, environment := range request.Environment {
		if environment.Key == hostMCPEnv {
			env = environment.Value
		}
	}
	if !strings.Contains(env, "/run/codex-safe-host-mcp/e0.sock") {
		t.Fatalf("%s must carry the session socket view, got %q", hostMCPEnv, env)
	}
	mounted := false
	for _, mount := range request.Mounts {
		if mount.Source == forwarding.channel.Generation && mount.Target == hostmcp.SessionTarget {
			mounted = true
		}
	}
	if !mounted {
		t.Fatal("a non-empty set must mount the generation directory at the session target")
	}
	// The channel label is recorded so a reusing launcher can adopt it.
	found := false
	for _, label := range request.Labels {
		if label.Key == hostMCPChannelLabel && label.Value == forwarding.channel.Generation {
			found = true
		}
	}
	if !found {
		t.Fatalf("the channel label must record %q", forwarding.channel.Generation)
	}
}

// planHostMCP with --no-host-mcp must not read config.toml at all: a malformed one that would fail
// discovery is proof the read never happened.
func TestPlanHostMCPWithNoHostMCPPerformsNoConfigRead(t *testing.T) {
	t.Parallel()
	codexHome := writeCodexHome(t, "this is not valid TOML [[[")
	attempt := &launchAttempt{
		docker:     hostLauncher(1000, 1000, "/home/developer", ""),
		plan:       testPlanWithHostMCP(codexHome),
		projectKey: "key",
		noHostMCP:  true,
	}
	if err := attempt.planHostMCP(); err != nil {
		t.Fatalf("--no-host-mcp must skip discovery entirely, but got %v", err)
	}
	if !attempt.hostMCP.set.Empty() {
		t.Fatal("--no-host-mcp must resolve an empty set")
	}
}

// Without the flag, the same malformed config.toml must fail the launch, proving the flag is what
// suppressed the read above rather than discovery silently ignoring the file.
func TestPlanHostMCPWithoutFlagReadsConfig(t *testing.T) {
	t.Parallel()
	codexHome := writeCodexHome(t, "this is not valid TOML [[[")
	attempt := &launchAttempt{
		docker:     hostLauncher(1000, 1000, "/home/developer", ""),
		plan:       testPlanWithHostMCP(codexHome),
		projectKey: "key",
	}
	if err := attempt.planHostMCP(); err == nil {
		t.Fatal("a malformed config.toml must fail discovery when the flag is absent")
	}
}

func TestPlanHostMCPDefersChannelAllocationUntilAfterFingerprint(t *testing.T) {
	validCodexHome := writeCodexHome(t, `
[mcp_servers.idea]
url = "http://127.0.0.1:64342/stream"
`)
	attempt := &launchAttempt{
		docker:     hostLauncher(1000, 1000, "/home/developer", ""),
		plan:       testPlanWithHostMCP(validCodexHome),
		projectKey: "key",
	}
	if err := attempt.planHostMCP(); err != nil {
		t.Fatalf("planHostMCP() error = %v", err)
	}
	if attempt.hostMCP.set.Empty() || attempt.hostMCP.candidate || attempt.hostMCP.channel.Generation != "" {
		t.Fatalf("host MCP plan = %#v, want endpoints without a candidate channel", attempt.hostMCP)
	}
}

func testPlanWithHostMCP(codexHome string) launchplan.Plan {
	plan := testPlan()
	for index := range plan.Provenance {
		if plan.Provenance[index].Mount.Target == "/home/developer/.codex" {
			plan.Provenance[index].Mount.Source = codexHome
			plan.Mounts[index].Source = codexHome
		}
	}
	plan.Roles = append(plan.Roles, projectenv.RoleHostMCPChannel)
	return plan
}

func writeCodexHome(t *testing.T, config string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return dir
}
