# Loom Context Pack

## Quick Links

- Workspace snapshot: `00-workspace-snapshot.md`
- MCP inventory: `00-mcp-inventory.md`
- Research (mobile companion): `10-research.md`
- Research addendum (mobile roadmap/features, external): `13-research-mobile-roadmap-features-2026-02-19.md`
- Research addendum (mobile signing/distribution): `14-research-mobile-signing-publish-2026-02-25.md`
- Product spec (mobile companion): `20-product-spec.md`
- Implementation plan (mobile companion): `30-implementation-plan.md`
- Implementation plan addendum (build/sign/publish): `33-mobile-signing-release-plan-2026-02-25.md`
- Mobile API draft: `../docs/MOBILE_COMPANION_API.md`
- Mobile security draft: `../docs/MOBILE_COMPANION_SECURITY.md`
- Historical roadmap mapping: `31-gap-to-backlog-map.md`
- Mobile backlog mapping: `32-mobile-gap-to-backlog-map.md`
- Decisions: `40-decisions.md`
- Worklog: `50-worklog.md`

## Current Goal

Close the iOS release-operability gap so Loom Companion is:
1. installable on your iPhone with deterministic signing steps,
2. distributable to internal testers,
3. ready for a full TestFlight -> production release pipeline.

## Near-Term Success Criteria

- Build/signing prerequisites are explicit and scriptable.
- Archive/export/upload workflow is decomposed into executable increments.
- CI requirements for macOS-based iOS release jobs are scoped without disrupting existing Go CI.
- Planning artifacts are source-backed and implementation-ready.

## Risks

- iOS signing identity/provisioning can drift across developer machines.
- CI currently has no iOS lane, so release-quality packaging is manual.
- Publishing path choice (ad hoc vs TestFlight-first) can introduce process churn if not locked early.

## Notes

- Context pack refreshed on 2026-02-25.
- MCP resource/template introspection was unavailable in this runtime; CLI fallback inventory was used.
- This planning slice focuses on iOS build/sign/publish operations readiness.

## Sources

- `.loom/00-mcp-inventory.md`
- `.loom/14-research-mobile-signing-publish-2026-02-25.md`
- `.loom/33-mobile-signing-release-plan-2026-02-25.md`
