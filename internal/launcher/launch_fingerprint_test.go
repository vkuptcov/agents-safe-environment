package launcher

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/hostmcp"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
)

func TestCreationFingerprintCoversOnlyCreationTimeFields(t *testing.T) {
	plan := testPlan()
	endpoints := hostmcp.Set{Endpoints: []hostmcp.Endpoint{{Host: "localhost", Port: 8080, Names: []string{"idea"}}}}
	base := mustCreationFingerprint(t, plan, "image:one", false, false, endpoints)

	mutations := []struct {
		name          string
		plan          launchplan.Plan
		image         string
		imageOverride bool
		noHostMCP     bool
		endpoints     hostmcp.Set
	}{
		{name: "image reference", plan: plan, image: "image:two", endpoints: endpoints},
		{name: "image override", plan: plan, image: "image:one", imageOverride: true, endpoints: endpoints},
		{name: "mount", plan: changedMountPlan(plan), image: "image:one", endpoints: endpoints},
		{name: "host MCP policy", plan: plan, image: "image:one", noHostMCP: true, endpoints: endpoints},
		{name: "endpoint", plan: plan, image: "image:one", endpoints: hostmcp.Set{Endpoints: []hostmcp.Endpoint{{Host: "localhost", Port: 8081}}}},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			if got := mustCreationFingerprint(t, mutation.plan, mutation.image, mutation.imageOverride, mutation.noHostMCP, mutation.endpoints); got == base {
				t.Fatalf("fingerprint = %q, want change from %q", got, base)
			}
		})
	}

	commandOnly := plan
	commandOnly.WorkingDir = "/another/invocation/directory"
	commandOnly.Roles = []string{"unrelated"}
	commandOnly.Provenance = nil
	if got := mustCreationFingerprint(t, commandOnly, "image:one", false, false,
		hostmcp.Set{Endpoints: []hostmcp.Endpoint{{Host: "localhost", Port: 8080, Names: []string{"renamed"}}}}); got != base {
		t.Fatalf("non-creation fields changed fingerprint = %q, want %q", got, base)
	}
}

func TestDockerLaunchRejectsFingerprintMismatchBeforePreflight(t *testing.T) {
	plan := simplePlan()
	labels := matchingLabels(t, plan, 1000)
	labels[launchConfigLabel] = strings.Repeat("f", 64)
	runner := &fakeCommandRunner{outputs: []commandResult{{
		output: inspectionJSON(t, strings.Repeat("b", 64), true, "running", labels),
	}}}
	err := testDocker(runner).Launch(context.Background(), plan, "image", []string{"true"}, launchplan.Options{})
	if err == nil || !strings.Contains(err.Error(), "creation fingerprint") || !strings.Contains(err.Error(), "finish the active session") {
		t.Fatalf("Launch() error = %v, want fingerprint mismatch", err)
	}
	var mismatch *launchConfigMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("Launch() error = %T, want launchConfigMismatchError", err)
	}
	if len(runner.combinedCalls) != 1 || len(runner.runCalls) != 0 {
		t.Fatalf("mismatch reached Docker lifecycle: combined %#v run %#v", runner.combinedCalls, runner.runCalls)
	}
}

func mustCreationFingerprint(t *testing.T, plan launchplan.Plan, image string, imageOverride, noHostMCP bool, endpoints hostmcp.Set) string {
	t.Helper()
	fingerprint, err := creationFingerprint(plan, image, imageOverride, noHostMCP, endpoints)
	if err != nil {
		t.Fatal(err)
	}
	return fingerprint
}

func changedMountPlan(plan launchplan.Plan) launchplan.Plan {
	changed := plan
	changed.Mounts = append([]launchplan.BindMount(nil), plan.Mounts...)
	changed.Mounts[0].Source = "/different/source"
	return changed
}
