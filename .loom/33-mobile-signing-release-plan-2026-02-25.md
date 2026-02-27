# Implementation Plan: Mobile Build, Signing, and Publish

_Date: 2026-02-25_

## Objective

Establish a deterministic path from local dev build to installable distribution, then to full release operations:

1. **Install now** on your iPhone reliably.
2. **Share installs** with internal testers.
3. **Automate full release** (TestFlight + production-ready pipeline).

## Delivery Increments

## Increment A: Install-Now Signing Baseline (Development)

### Goals

- Make personal iPhone install deterministic from repo commands.
- Keep signing config out of committed secrets while still scriptable.

### Changes

1. Add explicit iOS signing env contract in `Makefile` and docs:
- `IOS_DEVELOPMENT_TEAM`
- `IOS_CODE_SIGN_STYLE` (default `Automatic`)
- `IOS_CODE_SIGN_IDENTITY` (default `Apple Development`)

2. Add a device-run target:
- `make mobile-app-run-device`
- Uses `xcodebuild` destination `generic/platform=iOS` for build + local Xcode handoff, or a selected connected device destination when provided.

3. Add signing validation target:
- `make mobile-signing-check`
- Prints resolved Team/profile/signing mode and fails fast when required values are missing.

4. Update runbook with exact install-now path (single command + fallback command list).

### Acceptance

- On a Mac with Xcode account configured, `make mobile-app-run-device` results in app installed on connected iPhone.
- Failures are explicit (missing team/profile/device) and actionable.

## Increment B: Archive + Export (Ad Hoc/TestFlight-ready Artifacts)

### Goals

- Produce reproducible `.xcarchive` and `.ipa` artifacts from CLI.

### Changes

1. Add archive target:
- `make mobile-app-archive`
- Output: `build/mobile/LoomCompanion.xcarchive`

2. Add export targets:
- `make mobile-app-export-ad-hoc`
- `make mobile-app-export-app-store`
- Output: `build/mobile/export/<method>/LoomCompanion.ipa`

3. Add export options templates under `scripts/mobile/`:
- `ExportOptions.ad-hoc.plist`
- `ExportOptions.app-store.plist`

4. Add version/build-number discipline:
- document and script updates to `MARKETING_VERSION` and `CURRENT_PROJECT_VERSION` in a single command flow.

### Acceptance

- Archive + IPA export succeed locally using configured signing assets.
- Artifacts are generated in deterministic paths and can be consumed by upload tooling.

## Increment C: Publish Path (TestFlight First)

### Goals

- Provide a repeatable internal-distribution channel for installs without Xcode.

### Changes

1. Add upload command wrapper:
- `make mobile-testflight-upload`
- Uses one chosen uploader backend (recommendation: fastlane `pilot`; fallback: Apple transport CLI), with credentials from env/keychain.

2. Add release checklist doc:
- app metadata, privacy settings, build notes, tester groups, rollback path.

3. Add manual promote gates:
- Internal testers -> external testers -> production readiness checklist.

### Acceptance

- A CLI-triggered upload appears in App Store Connect TestFlight and can be installed by internal testers.

## Increment D: CI/CD Automation (macOS Runner)

### Goals

- Move iOS packaging from personal machine steps to auditable pipeline jobs.

### Changes

1. Add macOS-tagged CI jobs (new included file):
- `ios:test` (xcodebuild test/build)
- `ios:archive` (tag/manual)
- `ios:export` (tag/manual)
- optional `ios:testflight-upload` (protected/manual)

2. Store signing materials as protected CI variables/secrets:
- ASC API key, cert/profiles (or match repo access), team identifier.

3. Publish IPA/xcarchive as artifacts; gate uploads behind protected branches/tags.

### Acceptance

- Tag pipeline can produce archive/export artifacts without local intervention.
- Upload job is gated and auditable.

## Proposed Task Order (Pragmatic)

1. Increment A (same day)
2. Increment B (next)
3. Increment C (internal distribution)
4. Increment D (CI hardening)

## Risks and Controls

- **Signing drift across machines**: use env contract + explicit checks + deterministic export options.
- **Credential sprawl**: prefer keychain/CI protected vars; never commit secrets.
- **Pipeline fragility**: isolate iOS jobs to macOS runner and keep Linux Go pipeline unchanged.
- **Release confusion**: require version/build checklist and tagged release gates.

## Immediate Operator Runbook (Today)

1. `make mobile-dev`
2. `make mobile-app-open`
3. In Xcode: select iPhone + Team and Run

This remains the fastest install path until Increment A/B targets land.

## Sources

- `apps/loom-companion-ios/project.yml:22-42`
- `Makefile:729-786`
- `Makefile:788-829`
- `scripts/mobile/dev_bootstrap.sh:36-40`
- `scripts/mobile/dev_bootstrap.sh:86-107`
- `docs/MOBILE_COMPANION_IPHONE_TESTING.md:64-87`
- `.gitlab-ci.yml:20-24`
- `.gitlab-ci.yml:390-469`
- `.loom/14-research-mobile-signing-publish-2026-02-25.md`
