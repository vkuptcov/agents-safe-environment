package launcher

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/hostmcp"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
)

const (
	launchConfigLabel         = "codex-safe.launch-config"
	launchConfigSchemaVersion = 3
)

// launchFingerprintInput is deliberately an ordered struct: maps and TOML bytes would make an
// otherwise-identical creation contract depend on incidental encoding details.
type launchFingerprintInput struct {
	SchemaVersion    int                          `json:"schema_version"`
	ImageReference   string                       `json:"image_reference"`
	ImageOverride    bool                         `json:"image_override"`
	Mounts           []fingerprintMount           `json:"mounts"`
	NoHostMCP        bool                         `json:"no_host_mcp"`
	HostMCPEndpoints []string                     `json:"host_mcp_endpoints"`
	DependencyCaches []fingerprintDependencyCache `json:"dependency_caches"`
}

type fingerprintDependencyCache struct {
	Kind           string `json:"kind"`
	PhysicalSource string `json:"physical_source"`
	EnvironmentKey string `json:"environment_key"`
}

type fingerprintMount struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only"`
}

// creationFingerprint returns the versioned creation-time identity of one requested session. It
// intentionally excludes command argv, working directory, logical roles, comments, image IDs, and
// the random host-MCP generation path.
func creationFingerprint(
	plan launchplan.Plan,
	image string,
	imageOverride bool,
	noHostMCP bool,
	endpoints hostmcp.Set,
) (string, error) {
	mounts := make([]fingerprintMount, 0, len(plan.Mounts))
	for _, mount := range plan.Mounts {
		mounts = append(mounts, fingerprintMount{
			Source: mount.Source, Target: mount.Target, ReadOnly: mount.ReadOnly,
		})
	}
	caches := make([]fingerprintDependencyCache, 0, len(plan.DependencyCaches))
	for _, cache := range plan.DependencyCaches {
		environmentKey, err := cache.EnvironmentKey()
		if err != nil {
			return "", err
		}
		caches = append(caches, fingerprintDependencyCache{
			Kind: string(cache.Kind), PhysicalSource: cache.Source, EnvironmentKey: environmentKey,
		})
	}

	orderedEndpoints := append([]hostmcp.Endpoint(nil), endpoints.Endpoints...)
	sort.Slice(orderedEndpoints, func(first, second int) bool {
		if orderedEndpoints[first].Host != orderedEndpoints[second].Host {
			return orderedEndpoints[first].Host < orderedEndpoints[second].Host
		}
		return orderedEndpoints[first].Port < orderedEndpoints[second].Port
	})
	addresses := make([]string, 0, len(orderedEndpoints))
	for _, endpoint := range orderedEndpoints {
		addresses = append(addresses, endpoint.Address())
	}

	encoded, err := json.Marshal(launchFingerprintInput{
		SchemaVersion:    launchConfigSchemaVersion,
		ImageReference:   image,
		ImageOverride:    imageOverride,
		Mounts:           mounts,
		NoHostMCP:        noHostMCP,
		HostMCPEndpoints: addresses,
		DependencyCaches: caches,
	})
	if err != nil {
		return "", fmt.Errorf("encode launch fingerprint: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

type launchConfigMismatchError struct {
	projectRoot string
	running     string
	requested   string
}

func (err *launchConfigMismatchError) Error() string {
	return fmt.Sprintf(
		"a managed session for worktree %q has creation fingerprint %q, but this launch resolved %q; "+
			"finish the active session before retrying, then relaunch",
		err.projectRoot, err.running, err.requested,
	)
}
