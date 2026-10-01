# Exec Plan: Host Access to Session Services

- Status: in review
- Created: 2026-09-25
- Design: `docs/design-docs/session-service-access.md`
- Scope: `internal/launcher/`, `internal/container/`, `tests/smoke/`, `docs/design-docs/`

## Objective

Let the developer connect from the host to session-local TCP services at the session IP and same port, including
ports published later by nested Docker, without reserving application ports.

## Done Criteria

- A loopback-bound session service accepts host connections through the session IP and sees a loopback peer.
- A wildcard-bound service can bind after startup and remains reachable.
- Nested Docker's published ports retain their normal behavior; unpublished ports remain private.
- Session startup fails clearly when the required network configuration cannot be installed.
- Automated gates pass, or unavailable Sysbox verification is reported explicitly.

## Current Baseline

Session creation uses Docker's default bridge without endpoint sysctls. The supervisor starts nested `dockerd` and
then serves commands, but installs no network translation rules.

## Implementation Decisions

- Enable `route_localnet` only for the session endpoint through Docker's endpoint sysctl option.
- Resolve the actual ingress interface and gateway from the default route inside the container.
- Add NAT rules matching host-gateway TCP traffic to session-local addresses, after nested Docker initialization.
- Leave relay and maintenance containers unchanged; bump the creation fingerprint for persistent-session reuse.

## Phases

### Phase 1: Session Creation
Purpose: Give only project sessions the endpoint setting required for loopback routing.
Status: done
Done when: the session create request carries the setting and old persistent sessions fail reuse safely.

1. Add a failing request/argv test and implement session-only endpoint sysctl encoding.
2. Bump and test the creation fingerprint schema.

### Phase 2: Session NAT
Purpose: Translate host connections without binding any application port.
Status: done
Done when: the supervisor installs validated, host-scoped rules before accepting commands.

1. Add failing parser and rule-command tests, including malformed and multiple-route inputs.
2. Implement route discovery, sysctl verification, and one-time NAT installation.
3. Add supervisor startup and failure-path tests.

### Phase 3: Integration and Documentation
Purpose: Prove and document the user-visible behavior and trust boundary.
Status: done
Done when: the smoke probe covers direct and nested published services, and the docs explain exposure.

1. Add host-to-session smoke cases for direct loopback/wildcard listeners and nested Docker publications.
2. Update the design and operator documentation for the real implementation and local-peer trust risk.
3. Run all required checks and request Claude Opus 5.5 review of the final diff.

## Validation Gates

- `go test ./internal/launcher/... ./internal/container/...` passes.
- `make test`, `make lint`, and `make check-docs` pass.
- `make test-smoke-go` passes on a Sysbox host, or is reported unavailable with a precise reason.
- Claude Opus 5.5 reviews the final changes; important findings are resolved or reported.

## Risks and Constraints

- Nested Docker's publication mechanism is not part of this feature's contract; the smoke test checks the returned
  response through both loopback-bound and wildcard-bound published ports.
- A host connection appears local to the target service and may satisfy loopback-trust rules.
- No new dependency is authorized without owner approval.

## Out of Scope

- Unpublished nested container ports, UDP, IPv6, and remote LAN access.

## Progress Notes

- 2026-09-25: Plan created from the approved design; implementation in progress.
- 2026-09-25: Focused and repository-wide Go checks pass. Ordinary Docker confirms loopback and wildcard TCP
  connections; real Sysbox is unavailable because `sysbox-runc` is not registered on this host.
- 2026-09-25: Claude Opus 5.5 implementation review requested changes, especially nested Docker NAT ordering and
  response-level smoke coverage. Findings are in the ignored feature-review report; Phase 3 remains in progress.
- 2026-09-25: Simplified session rule setup to one append per chain and changed nested smoke probes to read
  application responses; real Sysbox verification remains unavailable on this host.
- 2026-09-25: Claude Opus 5.5 further reduced rule setup to one route lookup and unified response-level smoke probes.
  `make test`, `make lint`, and `make check-docs` pass; the plan stays active pending Sysbox verification.
- 2026-10-01: Sysbox is registered on this host, so the pending gate ran: `make test-smoke-go` passes in full,
  including nested loopback- and wildcard-published ports answering through the session IP. Smoke also proves an
  unpublished nested port stays private and that the session adds no host port binding and leaves host
  `route_localnet` off. Version-7 fingerprint baselines are pinned again, the host-gateway trust scope is stated in
  `docs/security.md`, and the design status is `Implemented`. Review findings F-001, F-004, and the sibling-isolation
  part of F-005 are accepted as debt (TD-6, TD-7); the rest are fixed or rejected on real-host evidence. Moving to
  review for owner acceptance.
