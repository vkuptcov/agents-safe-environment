package codexinstall

import (
	"reflect"
	"testing"
)

func TestLabelsExactSetAndOrder(t *testing.T) {
	t.Parallel()
	identity := Identity{HostUID: 1000, Protocol: 3, Target: Target{Architecture: ArchitectureARM64}}
	want := []Label{
		{Key: "codex-safe.managed", Value: "true"},
		{Key: "codex-safe.resource", Value: "codex-installation"},
		{Key: "codex-safe.host-uid", Value: "1000"},
		{Key: "codex-safe.codex-target", Value: "linux-arm64"},
		{Key: "codex-safe.codex-store-protocol", Value: "3"},
	}
	got := identity.Labels()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Labels() = %#v, want %#v", got, want)
	}
}

func TestLabelsVaryWithIdentity(t *testing.T) {
	t.Parallel()
	first := Identity{HostUID: 1000, Protocol: 1, Target: Target{Architecture: ArchitectureAMD64}}
	second := Identity{HostUID: 1001, Protocol: 1, Target: Target{Architecture: ArchitectureAMD64}}
	if reflect.DeepEqual(first.Labels(), second.Labels()) {
		t.Fatalf("labels for different host UIDs must differ")
	}
}
