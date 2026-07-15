# Flash-Loader Tmpfs Reuse Iteration Plan

## Review

- Roadmap milestone: post-Wan 2.1 gfx1100 warm-start reliability follow-up
- Spec sections: `docs/user/caching.md` flash-loader behavior and
  `cmd/flexinfer-flash-loader/main.go` incremental-copy contract
- Prior decisions to preserve: atomic destination replacement, size-based reuse,
  excluded-path filtering, optional integrity verification, and 5%/10 MiB
  preflight headroom for bytes that will actually be written

## Align

- Slice name: reuse an existing flash-loader tmpfs destination
- Scope in:
  - calculate preflight capacity from missing or size-mismatched files
  - allow a fully reusable destination to proceed without free-space headroom
  - report reusable bytes/files in startup logs
  - add focused regression coverage and user-facing behavior documentation
- Scope out:
  - content-hash validation or changes to size-based reuse
  - pruning destination-only files
  - controller/CRD changes
  - BuildKit cache and extraction observability
- Acceptance criteria:
  - a matching destination needs zero additional tmpfs bytes
  - a partial destination charges only files that must be copied atomically
  - an empty destination retains the existing headroom check
  - flash-loader unit tests and repository quality checks pass
- Dependencies/blockers: none; the merged MR !840 head is available locally as
  `origin/master` even though GitLab was briefly unreachable during fetch

## Riskiest assumption + kill-test

**Load-bearing assumption**: flash-loader's existing size-equality rule remains
the intended reuse contract, so matching destination files require no writes or
additional tmpfs capacity.

**Kill test**: populate a destination with every discovered file at the matching
size, make the capacity probe report zero available blocks, and require the
incremental plan to report zero copy bytes while the preflight succeeds. Then
change one file size and omit another; the plan must charge exactly those two
source sizes.

**Failure mode if the assumption is wrong**: persistent warm destinations still
fail near capacity, or the loader silently reuses artifacts that the product
contract requires it to recopy.

**Status**: passed 2026-07-15 via `TestRequiredCopyBytes_ExistingDestination`,
`TestRequiredCopyBytes_PartialDestination`, and
`TestCheckAvailableSpace_NoCopyNeeded`.

## Land

- Planned file areas:
  - `cmd/flexinfer-flash-loader/main.go`
  - `cmd/flexinfer-flash-loader/main_test.go`
  - `docs/user/caching.md`
- Implementation steps:
  1. Add regression coverage for full and partial destination reuse.
  2. Compute incremental write bytes before the preflight space check.
  3. Document and verify the reuse behavior.

## Prove

- Tests to run: targeted flash-loader regression tests, then
  `go test ./cmd/flexinfer-flash-loader`
- Lint/static checks: `gofmt`, `go vet ./cmd/flexinfer-flash-loader`, repository
  pre-commit hooks when configured
- CI checks: GitLab branch pipeline through terminal state

### Local verdict: PASS

- The regression tests initially failed to compile because the incremental
  capacity planner did not exist, then passed after the implementation.
- `go test ./cmd/flexinfer-flash-loader`,
  `go test -race ./cmd/flexinfer-flash-loader`, `go vet` for the package,
  `go test ./...`, and canonical `make test` all passed.
- `make test` regenerated unrelated CRD/deepcopy output with a newer local
  `controller-gen`; those mechanical deltas were removed and the final diff is
  limited to the flash-loader slice.
- The devbox quality gate was invoked, but its sandbox image remained in the
  build phase for more than five minutes and returned no check result. The
  completed local gate above and the branch pipeline are the authoritative
  proof for this slice.

## Handoff/Harvest

- Docs to update: `docs/user/caching.md`
- Agent-context entries: root cause, incremental capacity decision, validation
  and merge evidence
- Next-slice candidate: improve BuildKit cache/extraction observability
