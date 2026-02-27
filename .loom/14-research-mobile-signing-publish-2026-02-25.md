# Research: Loom Companion Build, Signing, and Distribution

_Date: 2026-02-25_

## Summary

The repo is ready for local simulator/device development but does not yet define a release-grade iOS signing/distribution pipeline. The fastest path to "installable now" is Development signing to your own iPhone from Xcode. The fastest path to shareable installs is TestFlight (internal testers), with ad-hoc export as an optional intermediate.

## Current State (Evidence)

1. iOS app target is defined and configured for automatic signing, but no fixed team is committed.
- `CODE_SIGN_STYLE: Automatic` is set in the app target config (`apps/loom-companion-ios/project.yml:32`).
- Bundle IDs are set for app/framework (`apps/loom-companion-ios/project.yml:17`, `apps/loom-companion-ios/project.yml:31`).
- No `DEVELOPMENT_TEAM` key exists in committed `project.yml`/`project.pbxproj` (`rg -n "DEVELOPMENT_TEAM" ...` returned no matches).

2. Local mobile developer workflow is strong.
- Preflight + simulator + project sync/open targets exist (`Makefile:729`, `Makefile:734`, `Makefile:746`, `Makefile:754`).
- `make mobile-dev` already handles token bootstrap, HUD restart, and prints connection values (`Makefile:794`, `scripts/mobile/dev_bootstrap.sh:36-40`, `scripts/mobile/dev_bootstrap.sh:154-157`).

3. Current CI is Go-focused and has no iOS build/sign/archive jobs.
- CI stages are only `lint`, `build`, `test`, `deploy` (`.gitlab-ci.yml:20-24`).
- Release artifacts are Go binaries only (`.gitlab-ci.yml:390-467`).

4. iPhone runbook is present but manual-signing-centric.
- Physical device flow is documented with "Set your Team under Signing" in Xcode (`docs/MOBILE_COMPANION_IPHONE_TESTING.md:70-74`).

## Distribution Options

### Option A: Development Signing (Install on your iPhone now)

- How: Xcode Run to connected device under Apple Development team.
- Pros: fastest, no CI required.
- Cons: not distributable to others; tied to local machine/account trust.
- Recommended use: immediate personal install/dogfooding.

### Option B: Ad Hoc IPA Export

- How: archive + export with ad-hoc provisioning profile and UDID list.
- Pros: installable outside Xcode to a fixed device set.
- Cons: UDID/profile management overhead; more manual than TestFlight.
- Recommended use: short-lived pre-TestFlight sharing with a small fixed device pool.

### Option C: TestFlight Internal Distribution

- How: archive + upload to App Store Connect; distribute to internal testers.
- Pros: best install UX for ongoing team testing; no UDID collection.
- Cons: requires Apple Developer Program + App Store Connect setup.
- Recommended use: primary "installable for team" channel.

### Option D: App Store Public Release

- How: production metadata/review + release gating after TestFlight hardening.
- Pros: full release channel.
- Cons: requires compliance, policy, and release management maturity.
- Recommended use: post-TestFlight stabilization milestone.

## Recommended Path

1. **Now (same day):** lock Development signing path and device-install runbook so you can always install quickly.
2. **Next (1-2 days):** implement deterministic archive/export make targets and signing config inputs.
3. **Then (2-4 days):** wire TestFlight upload path (manual first, CI second).
4. **Later:** production App Store release workflow with versioning gates and release checklist.

## Key Gaps to Close

- No committed, environment-driven signing configuration contract (team/profile/export method).
- No archive/export automation targets in `Makefile`.
- No iOS CI lane on a macOS runner.
- No release documentation for version/build-number bump + upload + promote flow.

## Sources

- `apps/loom-companion-ios/project.yml:17`
- `apps/loom-companion-ios/project.yml:31-32`
- `Makefile:729-765`
- `Makefile:788-795`
- `scripts/mobile/dev_bootstrap.sh:36-40`
- `scripts/mobile/dev_bootstrap.sh:154-157`
- `.gitlab-ci.yml:20-24`
- `.gitlab-ci.yml:390-467`
- `docs/MOBILE_COMPANION_IPHONE_TESTING.md:70-74`
- Command: `xcodebuild -project apps/loom-companion-ios/LoomCompanion.xcodeproj -scheme LoomCompanion -showBuildSettings -configuration Release -destination 'generic/platform=iOS' | rg 'DEVELOPMENT_TEAM|PROVISIONING_PROFILE_SPECIFIER'`
- Command: `rg -n "DEVELOPMENT_TEAM" apps/loom-companion-ios/project.yml apps/loom-companion-ios/LoomCompanion.xcodeproj/project.pbxproj`
