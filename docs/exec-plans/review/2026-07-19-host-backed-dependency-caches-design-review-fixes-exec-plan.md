# Exec Plan: Host-Backed Dependency Cache Design Review Fixes

- Status: in review
- Created: 2026-07-19
- Design:
  - [`docs/design-docs/host-backed-dependency-caches.md`](../../design-docs/host-backed-dependency-caches.md)
  - [`docs/design-docs/project-launcher-configuration.md`](../../design-docs/project-launcher-configuration.md)
- Scope:
  - host-backed cache initialization, derived sharing policy, overlay, and reuse contracts
  - launcher configuration schema and fingerprint documentation
  - the source design-review artifact

## Objective

Resolve every finding in the host-backed dependency-cache design review before implementation planning begins. Keep
schema, overlay, and active-session reuse ownership in the launcher-configuration design.

## Done Criteria

- Default initialization enables only uv and Go cache kinds with documented concurrent-writer support.
- Maven and writable Gradle require an explicit kind selection.
- Gradle read-only seeds are excluded from the supported contract and documented only as an alternative.
- Omitted and empty cache-list layering semantics are unambiguous.
- The launcher-configuration design owns the proposed schema and fingerprint extension.
- Every review finding has a resolved implementation response.
- `make check-docs` passes.

## Starting Baseline

The initial cache design enabled Maven and Gradle during automatic initialization, defined fingerprint content
outside the owning launcher-configuration design, and left the Gradle policy and overlay presence semantics
incomplete. The first design review recorded five open findings.

## Implementation Decisions

- Treat automatic initialization as consent only for concurrency-safe uv and Go caches; an explicit Maven or Gradle
  kind acknowledges uncoordinated writes.
- Keep the implemented launcher schema at version 1 while documenting the dependency-cache implementation as the
  schema-version-2 extension in the owning design.
- Exclude Gradle read-only seeds from the first-release contract because they require a separate, manually refreshed
  snapshot rather than sharing the live host cache.

## Phases

### Phase 1: Contract Alignment
Purpose: Remove the ownership and product-policy blockers from the two design docs.
Status: done
Done when: initialization, layering, Gradle policy, and fingerprint behavior have one unambiguous owner and contract.

1. Restrict automatic discovery to concurrency-safe kinds and update examples and tests.
2. Remove the unsupported Gradle read-only seed profile and retain it only as a considered alternative.
3. Define omitted and empty list behavior and the presence-aware overlay.
4. Move schema-version and fingerprint-extension ownership into the launcher-configuration design.
5. Keep one unambiguous Gradle profile in the tool matrix.

### Phase 2: Review Closure and Validation
Purpose: Record how each finding was resolved and prove the documentation remains valid.
Status: done
Done when: every finding is resolved, implementation responses cite the changed docs, and documentation checks pass.

1. Update the source design review with fixed statuses and implementation responses.
2. Run the documentation validation gate.
3. Move this plan to `review/` and update the plan catalog.

### Phase 3: Round 2 Clarity Fixes
Purpose: Remove the remaining fingerprint and Gradle concurrency ambiguities from the approved design.
Status: done
Done when: F2-002 through F2-004 are fixed and F2-001 remains recorded as an explicit owner decision.

1. Remove the unreachable read-only flag from the cache-specific canonical entry.
2. State that physical cache binds participate in `mounts` while `dependency_caches` captures tool behavior.
3. Distinguish supported sequential Gradle reuse from unsupported overlapping writers.
4. Update the round-2 review with implementation responses and rerun documentation validation.

### Phase 4: Remove Serialized Sharing Mode
Purpose: Keep the user configuration limited to choices that change behavior.
Status: done
Done when: cache entries contain only `kind` and `source`, and every sharing policy is derived from `kind`.

1. Remove `mode` from the TOML example and typed cache configuration.
2. Derive the target, managed routing, and sharing policy from `kind`.
3. Remove `mode` from the canonical fingerprint entry and update validation and tests.
4. Record the owner decision in the latest review artifact and rerun documentation validation.

### Phase 5: Round 3 Self-Review Fixes
Purpose: Close the remaining tool-routing and diagram accuracy gaps before implementation planning.
Status: done
Done when: managed routing has explicit precedence, Gradle compatibility is tool-owned, and docs agree.

1. Define how the session wrapper composes managed cache settings with image-owned environment values.
2. Remove the unenforceable Gradle version-compatibility gate and test tool-owned mixed-version behavior.
3. Make the mount diagram distinguish configured sources, common defaults, and the creation-time plan.
4. Fix stale catalog and plan terminology, record the review, and rerun documentation validation.

### Phase 6: Preserve Host Cache Paths
Purpose: Make the session filesystem mirror host cache locations instead of introducing launcher-specific targets.
Status: done
Done when: every cache bind targets its normalized configured host path and tool routing points to that same location.

1. Replace `/var/cache/agents-safe/*` targets with path-preserving binds in the design and Mermaid diagram.
2. Derive uv, Go, and Maven routing directly from source; derive Gradle user home from its `caches/` parent.
3. Keep the target in normalized physical mounts without duplicating it in cache-specific fingerprint entries, and
   update validation tests.
4. Record the owner decision in the latest review artifact and rerun documentation validation.

### Phase 7: Separate Host Resolution from Container Routing
Purpose: Make host cache discovery and container tool selection independent, explicit responsibilities.
Status: done
Done when: init resolves an effective host path once and every managed command is routed to its mounted target.

1. Define per-kind host resolution order and limit platform fallbacks to the supported Linux host.
2. Make managed environment variables or the Maven system property authoritative over container defaults.
3. Show resolution, binding, and per-command routing as separate steps in the mount diagram and test plan.
4. Record the owner decision and rerun documentation validation.

## Validation Gates

- `make check-docs` passes.
- `rg -n "\| P[123] \| F-00[1-5] \| Open \|"` against the review returns no matches.
- `rg -n "\| P[123] \| F2-00[1-4] \| Open \|"` against the round-2 review returns no matches.
- `rg -n "\| P[0-3] \| F3-00[1-4] \| Open \|"` against the round-3 review returns no matches.
- A readability scan finds no trailing whitespace or prose lines over 120 characters in changed Markdown files.

## Risks and Constraints

- The launcher-configuration design describes implemented version 1 behavior, so the version 2 cache extension must
  remain clearly conditional until code lands.
- This work changes design documents only; it must not imply that cache runtime behavior is already implemented.

## Out of Scope

- Runtime implementation, tests, and migrations.
- Persistent nested-Docker image or BuildKit caches.
- Operator-managed Gradle read-only seed implementation.

## Progress Notes

- 2026-07-19: Started from design review F-001 through F-005; no runtime code changes are in scope.
- 2026-07-19: Fixed all five findings, updated the source review, and passed `make check-docs`; moved the plan to
  review for owner acceptance.
- 2026-07-19: Removed `read_only_seed` from the supported contract after owner feedback; retained the approach only
  in Alternatives Considered.
- 2026-07-19: Addressed round-2 F2-002 through F2-004; retained F2-001 as the owner's explicit rejected finding.
- 2026-07-20: Removed serialized `mode`; explicit kind selection now carries consent and policy is derived by the
  launcher.
- 2026-07-20: Self-review defined tool-setting precedence, removed the unenforceable Gradle version gate, and made
  the mount topology reflect configured sources and creation-time planning.
- 2026-07-20: Replaced launcher-specific cache targets with same-path binds so container paths mirror the host.
- 2026-07-20: Separated one-time host resolution from per-command container routing; container defaults no longer
  select an attached cache.
