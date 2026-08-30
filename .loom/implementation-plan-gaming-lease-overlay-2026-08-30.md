# Implementation Plan: Secure One-Command Gaming Lease (P1–P3 + canary tooling)

**Date:** 2026-08-30
**Source:** `.loom/brainstorm-gaming-mode-xbox-steamdeck-2026-08-30.md` (converged recommendation: F5 security canary first, then F1+F2+F3 with the smallest truthful slice of F4)
**Branch:** `claude/flexinfer-gaming-lease-cf7284`
**Status:** implementing

## Riskiest assumption + kill-test

**Load-bearing assumption (product, inherited from the brainstorm):** a Sunshine
build patched against CVE-2026-32253 (GHSA-ph75-mgxh-mv57) can stream reliably on
the exact `cblevins-7900xtx` headless sway/wlroots + AMD VAAPI stack without
reproducing the local `2026.516` stream-start segfault (Sunshine #5018; local
revert `de205c44a`).

**Kill test:** the 30-minute exact-hardware canary in the brainstorm (isolated
runtime with candidate image, LAN-restricted; verify version + renderD128/VAAPI
path; pair Steam Deck 1280x800/60; 15 min motion+input+audio; disconnect/
reconnect; no segfault/black frame/capture switch; unpaired client cannot
authenticate). Requires a human with the Deck — **cannot be run by this session**.

**Failure mode if wrong:** the lease/CLI UX would sit on top of a streaming
runtime that is either vulnerable (old pin) or unusable (patched pin crashes).

**Status:** NOT RUN. **This plan is therefore built to be safe while the
assumption is unproven:** the controller refuses `SetMode(gaming)` unless the
node's runtime image is on an explicit approved list, and that list ships
**empty**. Nothing in this MR re-enables the vulnerable image; the gfx1100
profile stays on the serving digest. The canary is the operator's gate to
populate `gaming.approvedImages`.

**Code-level load-bearing assumption (this MR):** a controller-state overlay can
park and restore every inference workload style on a node (dedicated
Deployments, explicit `minReplicas` warm floors, shared-GPU groups,
runtime-managed models) with zero model-manifest edits. Kill test: fake-client
controller tests exercising park + restore across all four styles (this MR's
test suite), because every style funnels through `desiredReplicasForContext` /
`chooseSharedGroupLeaders`, both already covered by that test idiom. The first
live lease after the canary passes is the end-to-end confirmation.

## Why (current pain, verified)

- The Aug lease (!978 `0de40f610`) hand-edited ~100 lines across 7 files; the
  handback (`9f4358d50`) reverted only 5 files. Six park toggles are **still
  live** today: `qwen35-9b-ablit-rp` parked (minReplicas 0 / ondemand / litellm
  off), 128k workhorse + mtp-32k litellm off (mtp aliases dark ~11 days), twin's
  borrowed serviceLabel still present. This class of bug is exactly what the
  overlay removes. (Reverting those toggles is a **capacity decision**, kept out
  of this MR — see Deferred.)
- The controller fights the runtime idle guard: after a runtime idle-revert it
  re-issues `SetMode(gaming)` (`gamingsession_controller.go:158`).
- `build/Dockerfile.runtime` pins Sunshine `2025.924.154138`, affected by the
  critical auth-bypass advisory; the patched release is known-broken on this
  host. No activation may occur on either image.

## Design decisions

1. **Overlay = GamingSession itself.** No new CRD. A session with desired mode
   `gaming`, not deleted, phase != Expired, and effective deadline in the future
   is "active gaming intent" for `spec.nodeName`. Shared predicate
   `gamingIntentForNode()` in `controllers/`, replacing the inline logic in
   `modelbackfill_controller.go:433` so all consumers agree.
2. **Drain hooks (no manifest rewrites):**
   - `desiredReplicasForContext` returns 0 when the model's
     `nodeSelector[kubernetes.io/hostname]` is under gaming intent — covers
     dedicated Deployments, explicit warm floors, preload warming, and the
     runtime-managed path (both `ensureDeployment` and `reconcileViaRuntime`
     consume it).
   - `handleSharedGPU` synthesizes an unconditional `activeLease` (owner
     `gaming-session/<name>`) when the group's pinned node is under intent, so
     the election parks the whole group with honest `PreemptedBy` attribution
     and demand cannot promote onto the card.
   - Read errors fail OPEN toward serving (same posture as GPU leases).
   - Models with no hostname pin are out of scope for the overlay (documented);
     the runtime rejects loads while in gaming mode, which is today's backstop.
3. **Policy is GitOps; sessions are API objects.** New ConfigMap
   `flexinfer-gaming-policy` (chart-rendered from `.Values.gaming`): 
   `approvedImages` (empty by default → refuse activation), `defaultTTL` (2h),
   `maxTTL` (8h), `drainTimeout` (10m), `requireOwner` (true). Controller reads
   it live each reconcile; missing ConfigMap = built-in secure defaults.
   Sessions can never self-approve an image.
4. **Mandatory TTL without breaking old CRs:** `spec.ttl` (optional duration) +
   existing `spec.expiresAt`; effective deadline = min(expiresAt,
   creation+ttl, creation+maxTTL), where ttl falls back to policy defaultTTL —
   so every session always has a deadline. Controller stamps
   `status.expiresAt`; the intent predicate honors it.
5. **Activation ordering:** policy gate → drain (overlay parks; wait until no
   `flexinfer.ai/model`-labeled pods target the node) → `SetMode(gaming)`
   (runtime unloads its in-process models, then starts Sunshine) → Active.
   New phase `Draining`. Past `drainTimeout` → `DrainComplete=False/
   DrainBlocked` with blocker names; no mode switch.
6. **Conditions** (`status.conditions`, `apimeta.SetStatusCondition` +
   `Semantic.DeepEqual` write gate): `CapabilityReady` (runtime found/ready +
   image approved + owner policy), `DrainComplete` (False reasons `Draining` /
   `DrainBlocked`), `SunshineReady` (observed gaming && !degraded),
   `StreamActive` (from new runtime `clientsActive` telemetry; Unknown on old
   runtime images).
7. **Idle expiry, two layers:** (a) fix the fight — once `ActivatedAt` is set,
   an observed flip back to `inference` that the controller didn't request
   concludes the session (`Expired`, reason `RuntimeReverted`) instead of
   re-asserting gaming; (b) `spec.idleTimeout` — controller tracks
   `status.lastStreamActiveAt` from `clientsActive` and expires the session
   after a quiet interval (only enforced when telemetry is available).
8. **CLI (`flexinfer game`)**: `preflight <node>` (policy/image/runtime/
   hostNetwork/existing-session/drain-surface/label-coverage checks, no
   mutation), `start <node> --for 2h [--owner u] [--idle-timeout 30m] [--wait]`
   (creates `gaming-<node>`), `status [node]`, `stop <node> [--wait]`. Owner
   defaults to the local username. Direct API via existing kubeconfig plumbing.
9. **Runtime telemetry (small):** `GET /api/v1/mode` gains
   `clientsActive` (bool, gaming mode only — reuses the idle guard's
   established-TCP probe). Absent field (old image) → conditions go Unknown,
   never wrong.

## Slices (single MR, commit per slice)

1. **S1 API:** GamingSession `spec.ttl`, `spec.idleTimeout`,
   `spec.drainTimeout`; `status.conditions/expiresAt/lastStreamActiveAt`;
   `Draining` phase; condition type/reason constants; CRD regen (both
   `config/crd/` and `charts/flexinfer/crds/`).
2. **S2 Policy:** `controllers/gaming_policy.go` loader + chart ConfigMap
   template + `values.yaml` defaults + `values-k3s.yaml` section (approved list
   empty, commented with the canary gate).
3. **S3 Overlay:** shared intent predicate; `desiredReplicasForContext` hook;
   shared-GPU synthesized lease; backfill `admissionBlock` refactor; Model
   controller watch on GamingSessions.
4. **S4 Session lifecycle:** policy gate, drain wait + conditions,
   effective-deadline TTL, idle/external-revert conclusion, metrics
   (`flexinfer_gaming_session_active`, drain duration).
5. **S5 Runtime:** `clientsActive` in mode API + controller `StreamActive` /
   `lastStreamActiveAt` plumbing.
6. **S6 CLI:** `game` command group + tests.
7. **S7 Docs/build/cleanup:** runbook rewrite (one-command flow), canary
   procedure doc (kill-test checklist + `build-runtime.sh` `SUNSHINE_VERSION`
   override), Steam Deck / Xbox client doc (runner-up track), delete orphaned
   `deploy/gaming/`, repair the mangled comment block in
   `deploy/kustomization.yaml`.

## Explicitly deferred / out of scope

- **Reverting the six stale !978 toggles** — live capacity decision (orpheus
  voice lanes now warm-pin the card); surfaced separately.
- **Flipping the gfx1100 profile to a gaming-capable image** — gated on the P0
  canary passing; a one-stanza GitOps change afterward.
- Building/canarying the secure Sunshine image itself (operator + hardware).
- F8 companion-inference lane; proxy-side routing changes; Moonlight-UWP
  experiments.

## Rollout safety

Merging is inert: no GamingSession CRs exist in-cluster; the intent predicate
is false everywhere; the policy ships with zero approved images so even a
hand-created session refuses activation with `CapabilityReady=False/
ImageNotApproved`; the runtime change is additive JSON. CRD changes are
optional fields + new status surface.
