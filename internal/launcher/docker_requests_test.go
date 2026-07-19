package launcher

import (
	"reflect"
	"strings"
	"testing"

	"github.com/vkuptcov/agents-safe-environment/internal/launcher/dockercli"
	"github.com/vkuptcov/agents-safe-environment/internal/launcher/launchplan"
)

func TestCreateRequestUsesOnlyResolvedPhysicalMounts(t *testing.T) {
	t.Parallel()
	plan := testPlan()
	request, err := hostLauncher(1000, 1001, "/home/developer", "").buildCreateRequest(
		plan, "image", "codex-safe-test", hostMCPPlan{}, "fingerprint",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(request.Mounts, dockerMounts(plan.Mounts)) {
		t.Fatalf("mounts = %#v, want resolved plan %#v", request.Mounts, plan.Mounts)
	}
	fingerprintFound := false
	for _, label := range request.Labels {
		if label.Key == codexHomeLabel && label.Value != "/home/developer/.codex" {
			t.Fatalf("Codex label = %q, want resolved source", label.Value)
		}
		if label.Key == launchConfigLabel && label.Value == "fingerprint" {
			fingerprintFound = true
		}
	}
	if !fingerprintFound {
		t.Fatalf("labels = %#v, want %s", request.Labels, launchConfigLabel)
	}
}

func dockerMounts(mounts []launchplan.BindMount) []dockercli.Mount {
	result := make([]dockercli.Mount, 0, len(mounts))
	for _, mount := range mounts {
		result = append(result, dockercli.Mount{Source: mount.Source, Target: mount.Target, ReadOnly: mount.ReadOnly})
	}
	return result
}

func TestExecRequestUsesResolvedCodexTarget(t *testing.T) {
	t.Parallel()
	plan := testPlan()
	plan.Provenance[3].Mount.Target = "/container/codex"
	plan.Mounts[3].Target = "/container/codex"
	request, err := hostLauncher(1000, 1001, "/home/developer", "").buildExecRequest(
		plan, []string{"true"}, strings.Repeat("a", 64),
	)
	if err != nil {
		t.Fatal(err)
	}
	if want := "CODEX_HOME=/container/codex"; !containsKeyValue(request.Environment, want) {
		t.Fatalf("environment = %#v, want %q", request.Environment, want)
	}
}

func containsKeyValue(values []dockercli.KeyValue, want string) bool {
	for _, value := range values {
		if value.Key+"="+value.Value == want {
			return true
		}
	}
	return false
}
