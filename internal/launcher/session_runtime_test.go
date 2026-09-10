package launcher

import (
	"strings"
	"testing"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/dockercli"
)

// BuildCreateArgs no longer rejects an empty runtime, because the relay sidecar must take the Docker
// default. These tests prove that protection moved to the session's own creation path rather than
// disappearing: nothing else in this project may fall off sysbox-runc.

func TestSessionCreateRequestAlwaysCarriesExplicitSysboxRuntime(t *testing.T) {
	t.Parallel()
	request, err := hostLauncher(1000, 1001, "/home/developer", "").buildCreateRequest(
		testPlan(),
		"agents-safe-mvp:local",
		"agents-safe-aba8b4ca4ff345d5d0443c0c",
		hostMCPPlan{},
		"fingerprint",
		false,
	)
	if err != nil {
		t.Fatalf("buildCreateRequest() error = %v", err)
	}
	if request.Runtime != sysboxRuntime {
		t.Fatalf("session create request runtime = %q, want %q", request.Runtime, sysboxRuntime)
	}
	if request.NetworkMode != "" {
		t.Errorf("the session container never shares a host namespace, got network mode %q", request.NetworkMode)
	}
	if len(request.Command) != 0 {
		t.Errorf("the session container supplies no command so the image default serve is used, got %#v",
			request.Command)
	}

	args, err := dockercli.BuildCreateArgs(request)
	if err != nil {
		t.Fatalf("BuildCreateArgs() error = %v", err)
	}
	if !strings.Contains(strings.Join(args, " "), "--runtime="+sysboxRuntime) {
		t.Fatalf("session argv must carry an explicit runtime: %#v", args)
	}
}

// The shared argv builder is now permissive, which is exactly why the invariant needs its own guard.
func TestBuildCreateArgsAcceptsEmptyRuntimeForTheSidecar(t *testing.T) {
	t.Parallel()
	if _, err := dockercli.BuildCreateArgs(dockercli.CreateRequest{
		Image:   "image",
		Name:    "agents-safe-mcp-key-generation",
		Command: []string{"relay"},
	}); err != nil {
		t.Fatalf("the sidecar must be expressible with the Docker default runtime, got %v", err)
	}
}
