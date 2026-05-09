# Agent Engram Schema

Engrams extend recipes with prerequisite links, tier-graded proof contracts, and a tech-tree DAG so an agent can reason about what's already established in a codebase before introducing new patterns.

A recipe is just a Tier-1 engram with no prerequisites; the `agent_recipe_*` tools still work and continue to write into the same storage as `agent_engram_add`.

## Identity

Each engram has a stable URI of the form:

```
engram://<family>/<slug>
```

- `family`: kebab-case logical group (e.g. `atomic-file-write`, `retry-jitter`).
- `slug`: within-family unique segment, commonly the language (e.g. `go`, `python`).

Both segments must match `^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`. Same problem in different languages = same family, different slug.

## Schema

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `title` | string | Yes | Short descriptive title |
| `problem` | string | Yes | Problem this engram solves |
| `solution` | string | Yes | Step-by-step solution with code |
| `proof` | string | Yes | Evidence it works (see Tier contract below) |
| `tier` | int | No | 1 (idiom) / 2 (composite) / 3 (system). Default 1 |
| `family` | string | No | Logical group. Defaults to slug(title) |
| `slug` | string | No | Within-family unique slug. Defaults to language or `default` |
| `prerequisites` | string[] | No | Engram URIs this depends on |
| `language` | string | No | Programming language |
| `scope` | enum | No | `project` (default) / `workspace` / `universal` |
| `tags` | string[] | No | Extra free-form tags |

The service also tracks (read-only): `proof_status` (`unverified`/`verified`/`stale`/`failing`), `unlocked_in` (repos+branches where the proof has run green), and `last_verified` (timestamp).

## Tier / Proof Contract

| Tier | Examples | Required proof |
|------|----------|----------------|
| **1 — Idiom** | Error wrapping with `%w`, retry-with-jitter formula, structured logging keys | File reference (e.g. `pkg/x/y.go:42-58`) — anything goes |
| **2 — Composite** | Connection pool with healthcheck, debounce queue, idempotency-key write path | Must include a `command:` line (a runnable test) |
| **3 — System** | Saga coordinator, distributed tracing wiring, agent-handoff protocol | Must include `command:` AND (`benchmark:` OR `dashboard:`) |

`agent_engram_add` rejects writes that violate the contract. The check is by literal substring match in the proof string, so format your proof as `command: <runnable test>` (and `benchmark: ...` / `dashboard: ...` for tier 3).

## Cycles

`agent_engram_add` runs a DFS over each declared prerequisite's transitive prereqs. If the new URI appears anywhere in the resulting graph, the write is rejected. Self-prereqs are rejected directly.

## Storage

Engrams are stored as long-term memory items with `category="engram"`, the URI in tag form `uri:engram://family/slug`, and structured metadata under `engram_*` keys. Legacy recipe items (`category="recipe"`) remain in the same hierarchy and are surfaced by both recipe and engram recall.

## Tools

| Tool | Purpose |
|------|---------|
| `agent_engram_add` | Add an engram (validates tier/proof, rejects cycles, rejects duplicate URIs) |
| `agent_engram_recall` | Recall by query; optionally pull in transitive prerequisites at depth N |
| `agent_engram_list` | List engrams filtered by tier, family, language, scope, proof_status |
| `agent_engram_graph` | Adjacency list for the prerequisite graph rooted at a URI; direction `down` walks prereqs, `up` walks dependents |
| `agent_engram_verify` | Verify an engram's proof. File-ref proofs check existence + line range; URL proofs run a HEAD request; command proofs are skipped (devbox sandbox deferred). Updates `proof_status`, `last_verified`, and (on success) appends the repo to `unlocked_in`. Pass `uri` for one engram or `all=true` to sweep the workspace. |

## Verification

`agent_engram_verify` is the bridge between the proof contract and reality. It dispatches based on the proof string:

- **file_ref** (`pkg/foo/bar.go:42-58` or just `pkg/foo/bar.go`) — checks the file exists relative to `repo_root` (defaults to cwd) and that any line range fits the file's length.
- **url** (`https://...`) — runs a HEAD request with a 5-second timeout; 2xx/3xx is `verified`, anything else is `failing`.
- **command** (`command: go test ./...`) — currently returns `status=skipped` with a clear reason. Running arbitrary commands needs the devbox sandbox; that work is tracked as a follow-up.

After verification, the service updates `proof_status` (`verified`/`stale`/`failing`/unchanged-on-skip), refreshes `last_verified`, refreshes the `engram-status:*` tag, and on a successful verify appends the `repo` argument (defaulting to the basename of cwd, worktree-aware) to `unlocked_in` so other agents can see "this engram has been proven against repo X recently."

The HUD exposes a summary at `GET /api/engrams/summary` returning `{total, by_status, by_tier}` for the catalog view.

## When to Add an Engram vs. a Recipe

- A **recipe** is fine when the solution is self-contained, language-specific, and has no significant prerequisites in the codebase. (E.g. a memorable Go pluralization helper.)
- Reach for an **engram** when (a) the solution combines or assumes other patterns, (b) the codebase needs to consistently apply a chain of related solutions, or (c) the proof is more than a static file reference.

## Examples

### Tier 1 — file-reference proof

```
agent_engram_add(
  title="Error wrap with %w",
  problem="Bubbling errors lose context unless wrapped with %w",
  solution="Use fmt.Errorf(\"context: %w\", err) at every boundary that adds info",
  proof="pkg/agentcontext/svc_engrams.go:60-65",
  family="error-wrap",
  slug="go",
  language="go",
  tier=1
)
```

### Tier 2 — runnable test proof + prerequisite

```
agent_engram_add(
  title="Atomic file write via tempfile + rename",
  problem="Concurrent readers see partial content during os.WriteFile",
  solution="Write to <name>.tmp then os.Rename; atomic on POSIX filesystems",
  proof="command: go test ./pkg/skills -run TestWriteFileAtomic_NoPartialReads -v",
  family="atomic-file-write",
  slug="go",
  language="go",
  tier=2,
  prerequisites=["engram://error-wrap/go"]
)
```

### Tier 3 — runnable test + observable artifact

```
agent_engram_add(
  title="Distributed tracing with OTel + W3C propagation",
  problem="Need end-to-end request tracing across MCP proxy hops",
  solution="OTLP exporter + propagation.TraceContext{}; cmd-level tracer init",
  proof="command: go test ./pkg/mcpotel -v\nbenchmark: go test ./pkg/mcpotel -bench=. -benchmem",
  family="distributed-tracing",
  slug="go",
  language="go",
  tier=3,
  prerequisites=["engram://error-wrap/go"]
)
```
