package launcher

import (
	"testing"

	"github.com/vkuptcov/agents-safe-environment/internal/container"
)

// The design's timeout ordering is a correctness rule, not a tuning choice: violating it either
// fails every cold start or livelocks recovery permanently. This is the one place that sees all four
// values -- serve's retry and pre-lease deadline live in internal/container, the sidecar and
// readiness timeouts live here -- so it pins the whole inequality in a single assertion.
//
//	max(cold-start bound, retry) < sidecar initial-lease < launcher readiness
//	cold-start bound = launcher session-create budget + serve pre-lease deadline
func TestHostMCPTimeoutInequalityHolds(t *testing.T) {
	t.Parallel()
	coldStart := sessionCreateTimeout + container.PreLeaseDeadline

	if container.LeaseRetryInterval >= sidecarInitialLeaseTimeout {
		t.Errorf("serve retry %s must be under the sidecar initial-lease timeout %s",
			container.LeaseRetryInterval, sidecarInitialLeaseTimeout)
	}
	if coldStart >= sidecarInitialLeaseTimeout {
		t.Errorf("cold-start bound %s (=%s session-create + %s pre-lease) must be under the initial-lease timeout %s",
			coldStart, sessionCreateTimeout, container.PreLeaseDeadline, sidecarInitialLeaseTimeout)
	}
	if sidecarInitialLeaseTimeout >= readinessTimeout {
		t.Errorf("sidecar initial-lease timeout %s must be under the launcher readiness timeout %s",
			sidecarInitialLeaseTimeout, readinessTimeout)
	}
}
