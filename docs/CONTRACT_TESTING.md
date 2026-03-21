# Contract Testing (Golden Files)

Golden-file contract tests prevent silent API drift between loom-core and its
sibling consumers (loom VS Code extension, loom-zed).

## How It Works

Each contract test serializes a representative struct to JSON and compares the
output against a checked-in `.golden` file. When a field is added, renamed, or
removed, the golden file diff surfaces the change in code review.

```text
internal/contracts/
├── golden_test.go          # Contract test functions
└── testdata/
    ├── mobile_envelope.golden
    ├── mobile_dashboard.golden
    ├── dto_session_info.golden
    └── ...                 # 18 golden files total
```

## Covered Surfaces

| Category | Golden Files | What They Guard |
|----------|-------------|-----------------|
| Mobile API envelope | `mobile_envelope`, `mobile_envelope_error` | Response wrapper shape (`ok`, `data`, `error`, `meta`) |
| Mobile endpoints | `mobile_dashboard`, `mobile_agents`, `mobile_sessions`, `mobile_tasks` | Dashboard, agent list, session list, task list payloads |
| SSE events | `sse_events` | Real-time event payloads (`hud.fleet`, `hud.health`, `hud.session.*`, `hud.heartbeat`) |
| Bridge DTOs | `dto_session_info`, `dto_task_info`, `dto_presence_info`, `dto_entity_*`, `dto_relation_info`, `dto_context_inspect_request`, `dto_nudge_queue_policy`, `dto_heartbeat_request` | Serialization shape of types in `internal/hud/bridge/` |

## Running Contract Tests

```bash
# Verify golden files match (CI mode — fails on drift)
make ci-contracts

# Run directly with go test
go test -v -count=1 -run 'Contract$' ./internal/contracts/...

# Update golden files after an intentional change
go test -v ./internal/contracts/... -update-golden
```

## Workflow: Making a Breaking Change

1. **Change the Go struct** in `internal/hud/bridge/` or the test fixture in
   `internal/contracts/golden_test.go`.

2. **Run tests to see the diff**:
   ```bash
   make ci-contracts
   ```
   The test output shows the JSON mismatch so you can verify the change is
   intentional.

3. **Accept the change** by updating golden files:
   ```bash
   go test ./internal/contracts/... -update-golden
   ```

4. **Commit the updated `.golden` files** alongside the code change. The diff in
   the golden files is the contract changelog — reviewers (and sibling repo
   maintainers) can see exactly which fields changed.

5. **Notify sibling consumers**. Any golden file change may require updates in:
   - **loom** (VS Code extension) — TypeScript interfaces in `src/types/`
   - **loom-zed** (Zed extension) — Rust structs mirroring the DTOs

## Workflow: Consumer Integration (loom / loom-zed)

Sibling repos should track loom-core contract changes to stay aligned:

### For loom (TypeScript / VS Code)

1. When reviewing loom-core MRs, check `internal/contracts/testdata/` diffs.
2. Map changed fields to the corresponding TypeScript interfaces.
3. Update the interface and any dependent code before upgrading the loom-core
   dependency.

### For loom-zed (Rust / Zed)

1. Same review process — check golden file diffs.
2. Update Rust structs (typically `serde`-derived) to match new shapes.
3. Run the Zed extension test suite before releasing.

### Recommended Sibling CI Check

Sibling repos can pull the golden files and validate their own types against
them at CI time:

```bash
# In loom or loom-zed CI pipeline:
# 1. Fetch golden files from loom-core (same branch or latest main)
# 2. Parse the JSON and assert your local types can round-trip it
```

This catches drift early — before runtime errors surface in production.

## Adding a New Contract

1. Add a test function in `golden_test.go` following the naming convention
   `Test<Name>Contract`.

2. Construct a representative instance of the type with realistic field values
   (use fixed timestamps like `2025-01-15T10:30:00Z` for determinism).

3. Call `assertGolden(t, "<golden_file_name>", marshalIndent(t, value))`.

4. Generate the golden file:
   ```bash
   go test ./internal/contracts/... -update-golden -run TestNewContract
   ```

5. Commit the new `.golden` file.

## CI Integration

The `ci-contracts` target runs as part of the full CI pipeline (`make ci`):

```text
ci: ci-lint → ci-build → ci-test → ci-contracts → ci-security
```

It runs separately from `ci-test` so contract failures are clearly
distinguishable from unit/integration test failures in CI output.

## Design Decisions

- **Replicated structs**: Mobile envelope types are replicated in the test file
  rather than imported from `internal/hud`. This is intentional — the test
  validates the wire format independently, so refactoring internal types
  doesn't silently change the contract.

- **`-update-golden` flag**: Opt-in update prevents accidental acceptance.
  CI never passes this flag.

- **`assert.JSONEq`**: Comparison ignores key ordering, so struct field
  reordering in Go doesn't cause false positives.

- **Trailing newline**: `marshalIndent` appends `\n` to satisfy
  end-of-file-fixer pre-commit hooks.
