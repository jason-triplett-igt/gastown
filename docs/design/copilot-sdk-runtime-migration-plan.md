# Copilot SDK Runtime Migration Plan

## Status: Draft Plan
Created: 2026-03-24

## Problem Statement

Gastown already has a partial `copilot-external` path that uses the GitHub Copilot
SDK as a session runtime, but the broader system still carries Copilot-specific
runtime glue in adapters, owner processes, and role managers. The goal is to
displace as much of the Copilot execution substrate as possible without replacing
Gastown's control plane.

This plan focuses on replacing how Copilot-backed workers run, not how Gastown
coordinates durable work.

## Goal

Standardize Copilot-backed workers on Copilot SDK sessions, hooks, persistence,
and structured worker signaling while preserving Gastown's:

- beads work tracking
- convoys and lane orchestration
- worktree and branch management
- role semantics (`mayor`, `witness`, `deacon`, `refinery`, `polecat`)
- watchdog, scheduler, escalation, and refinery behavior

## Non-Goals

- Replacing beads with Copilot SDK
- Replacing convoys with SDK sub-agents
- Replacing worktree-based isolation for polecats
- Replacing Gastown mail, watchdog, scheduler, or refinery policy
- Migrating every runtime vendor; this plan is for Copilot-backed roles first

## Target Architecture

- Gastown remains the control plane
- Copilot SDK becomes the execution plane for Copilot-backed workers
- `.runtime/session-bindings/` remains the source of truth for role/session mapping
- `tmux` remains available for non-Copilot runtimes and human-facing workflows
- Copilot owner processes evolve toward the worker boundary in
  `docs/design/factory-worker-api.md`

## Migration Strategy

### Phase 1: Stabilize `copilot-external`

Treat `copilot-external` as the canonical Copilot runtime path for background
roles. Harden start, resume, lookup, close, binding persistence, and owner
recovery.

Primary code paths:

- `internal/runtime/external_copilot.go`
- `internal/runtime/adapter.go`
- `internal/cmd/external_copilot_owner.go`

Exit criteria:

- background roles restart cleanly against existing runtime session IDs
- owner restarts do not strand Copilot sessions
- bindings in `.runtime/session-bindings/` remain authoritative

### Phase 2: Move Copilot Role Behavior To SDK Hooks

Re-express Copilot role setup using SDK-native hooks rather than tmux-era glue.

Target mappings:

- `gt prime`-style startup context -> `onSessionStart`
- guardrails and tool policy -> `onPreToolUse`
- auditing and result handling -> `onPostToolUse`
- cleanup and metrics -> `onSessionEnd`

Exit criteria:

- Copilot background roles receive startup context through SDK hooks
- tool authorization runs through hook decisions
- Copilot roles no longer depend on prompt scraping or startup nudges for core setup

### Phase 3: Introduce A Stable Worker Boundary

Implement the Copilot owner loop as the first worker behind the contract sketched
in `docs/design/factory-worker-api.md`.

Initial worker surfaces:

- lifecycle
- prompt delivery
- authorization
- health
- telemetry

Exit criteria:

- Gastown can observe Copilot worker state from structured signals
- prompt delivery becomes structural rather than terminal injection
- health and telemetry do not require terminal inference

### Phase 4: Migrate Roles By Risk

Recommended order:

1. `witness`
2. `refinery`
3. `deacon`
4. one low-risk `polecat` lane

Leave `mayor` and interactive crew flows out of early migration.

Exit criteria:

- each migrated role reaches parity for start, resume, status, stop, and recovery
- no migration changes convoy, beads, worktree, or refinery semantics

### Phase 5: Consolidate And Delete

After parity is proven, make `copilot-external` the default Copilot path and
delete Copilot-only legacy runtime glue that is no longer needed.

Delete candidates:

- Copilot-specific prompt scraping
- Copilot-specific readiness hacks
- Copilot-only tmux plumbing superseded by SDK-native lifecycle handling

Keep candidates:

- shared tmux support for non-Copilot runtimes
- Gastown orchestration layers

## Concrete Spike Plan

Each spike is a time-boxed experiment with explicit tests and a binary result:
promote, revise, or stop.

### Spike 1: Map Copilot Runtime Seams

Purpose:

- inventory Copilot-specific runtime seams before behavior changes

Scope:

- `internal/runtime/adapter.go`
- `internal/runtime/external_copilot.go`
- `internal/cmd/external_copilot_owner.go`

Tests:

- `TestTmuxSessionAdapterStartUsesExternalConnector`
- `TestTmuxSessionAdapterResumeUsesExternalConnector`
- `TestTmuxSessionAdapterLookupUsesExternalConnector`
- `TestExternalBindingMetadataRoundTrip`

Acceptance criteria:

- external `start`, `resume`, and `lookup` paths are covered
- session binding metadata survives round-trip save/load
- every known Copilot-specific seam is represented by coverage or an explicit gap

### Spike 2: External Owner Restart Preserves Session Binding

Purpose:

- prove owner-process death does not strand a Copilot session

Scope:

- `internal/runtime/external_copilot.go`
- `internal/runtime/external_owner.go`
- `internal/cmd/external_copilot_owner.go`

Tests:

- `TestExternalOwnerStartWritesStatusAndBinding`
- `TestExternalOwnerResumeReusesRuntimeSessionID`
- `TestExternalOwnerLookupForManagedBinding`
- `TestExternalOwnerResetDoesNotLoseRuntimeBinding`

Acceptance criteria:

- binding survives owner restart
- runtime session ID remains stable across restart
- `Lookup` works for owner-managed sessions
- close/reset cleanup is deterministic

### Spike 3: Background Role Parity On `copilot-external`

Purpose:

- make `witness`, `refinery`, and `deacon` behave as first-class external roles

Scope:

- `internal/witness/manager.go`
- `internal/refinery/manager.go`
- `internal/deacon/manager.go`

Tests:

- `TestWitnessStartsExternalCopilotSession`
- `TestWitnessResumesStoredExternalBinding`
- `TestRefineryStartsExternalCopilotSession`
- `TestRefineryResumesStoredExternalBinding`
- `TestDeaconStartsExternalCopilotSession`
- `TestDeaconResumesStoredExternalBinding`

Acceptance criteria:

- each role starts with `role_agents[role]=copilot-external`
- each role resumes from stored binding
- invalid runtime session IDs fail cleanly
- provider and binding metadata remain correct

### Spike 4: Hook-Native Priming

Purpose:

- move Copilot startup context into SDK hooks

Scope:

- hook bridge near `internal/runtime/`
- reuse of existing prime/context assembly

Tests:

- `TestSessionStartHookBuildsPrimeContext`
- `TestResumeSessionDoesNotDoubleInjectPrimeContext`
- `TestRoleContextIncludesIssueRoleAndRigMetadata`

Acceptance criteria:

- external Copilot roles receive startup context via SDK hooks
- resumed sessions do not get duplicate priming unless explicitly intended
- tmux nudge fallback is not required for Copilot startup context

### Spike 5: Hook-Native Tool Authorization

Purpose:

- move Copilot tool policy into SDK hook decisions

Scope:

- `internal/toolpolicy/`
- external owner hook plumbing

Tests:

- `TestPreToolHookAllowsApprovedTool`
- `TestPreToolHookDeniesBlockedTool`
- `TestPreToolHookUsesRoleSpecificPolicy`
- `TestPostToolHookRecordsAuditData`

Acceptance criteria:

- allow/deny decisions come from hook output
- role-specific tool policy is enforced
- post-tool auditing is covered
- failures fail closed

### Spike 6: Worker Lifecycle Bridge

Purpose:

- replace terminal inference with structured lifecycle signals

Scope:

- `internal/runtime/boundary.go`
- `internal/runtime/events.go`
- Copilot worker lifecycle implementation

Tests:

- `TestWorkerLifecycleStartedReadyIdleStopped`
- `TestWorkerLifecycleBusyTransition`
- `TestWorkerHealthReflectsLatestLifecycleState`
- `TestMalformedLifecycleEventFailsClosed`

Acceptance criteria:

- Gastown can consume `started`, `ready`, `busy`, `idle`, and `stopped`
- health is derived from worker state rather than tmux heuristics
- malformed lifecycle data fails closed

### Spike 7: Structured Prompt Delivery

Purpose:

- replace Copilot prompt injection with explicit request/response flow

Scope:

- owner request/response queue in `internal/runtime/`

Tests:

- `TestExternalOwnerSendRequestRoundTrip`
- `TestExternalOwnerAskRequestReturnsContent`
- `TestExternalOwnerRequestClaimIsAtomic`
- `TestExternalOwnerRejectsUnsupportedRequestKind`
- `TestExternalOwnerProcessesRequestsFIFO`

Acceptance criteria:

- `send` and `ask` round-trip through structured request state
- requests are processed once
- queue ordering is deterministic
- errors are surfaced structurally

### Spike 8: Single Polecat Pilot Lane

Purpose:

- prove one low-risk polecat can use Copilot SDK without breaking worktree flow

Scope:

- `internal/polecat/session_manager.go`
- related polecat manager tests

Tests:

- `TestSessionManagerResumeBoundExternalSession`
- `TestPolecatExternalBindingPreservesIssueAndWorkdir`
- `TestPolecatResumeFailureDoesNotCorruptBookkeeping`
- `TestPolecatExternalSessionUsesStoredRuntimeSessionID`

Acceptance criteria:

- one polecat lane can sling, resume, and continue on external Copilot
- issue ID, workdir, provider, and runtime session ID remain intact
- failure paths do not corrupt bookkeeping

## Test Strategy

### Unit tests

- translation logic
- binding metadata
- tool policy mapping
- request/response queue behavior
- lifecycle state transitions

### Component tests

- adapter + fake external connector
- binding store persistence
- owner-loop state transitions

### Role-manager tests

- `witness`
- `refinery`
- `deacon`
- `polecat`

### Integration-lite tests

- filesystem-backed owner request flow
- structured lifecycle and prompt delivery without a real Copilot backend

## Recommended Test Runs

Run only the affected packages during spike work:

```bash
cd gastown && go test ./internal/runtime
cd gastown && go test ./internal/witness ./internal/refinery ./internal/deacon
cd gastown && go test ./internal/polecat
```

## Decision Rules

Use the following cut line throughout the migration:

- If a feature is about one Copilot session's behavior, prefer Copilot SDK
- If a feature is about many agents, many issues, many branches, or town-wide
  coordination, keep it in Gastown

## Expected Outcome

If successful, Gastown should be able to displace most of its Copilot-specific
runtime plumbing while keeping its orchestration model intact.

Practical expectation:

- high displacement for Copilot execution runtime
- low displacement for town orchestration
- gradual deletion of Copilot-only legacy glue after parity is proven
