# Agent Recipe Schema

> **Note:** Recipes are now Tier-1 entries in the [engram tech tree](../../agent-engrams/references/engram-schema.md).
> The `agent_recipe_*` tools still work and write into the same storage as `agent_engram_add`.
> When you need prerequisites or a stronger proof contract, prefer `agent_engram_add` directly.

Recipes are structured, proven solutions to specific problem classes.

## Schema

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `title` | string | Yes | Short descriptive title |
| `problem` | string | Yes | Problem description |
| `solution` | string | Yes | Step-by-step solution with code |
| `proof` | string | Yes | Evidence it works (file:line, test cmd, URL) |
| `tags` | string[] | No | Categorization tags |
| `language` | string | No | Programming language |
| `scope` | enum | No | project, workspace, universal |

## Proof Requirements

Every recipe MUST have proof. Valid proof types:

1. **File reference**: `pkg/auth/jwt.go:42-58` -- points to working code
2. **Test command**: `go test ./pkg/auth -run TestJWTValidation -v` -- runnable verification
3. **URL**: `https://...` -- external documentation

## When to Add Recipes

- You solved a problem that took significant debugging
- You found a non-obvious pattern or workaround
- A solution required consulting multiple sources
- The same problem class is likely to recur

## Example

```
agent_recipe_add(
  title="Fix stale database pool connections in Go",
  problem="Database connections go stale after idle timeout, causing first-query failures",
  solution="Set db.SetConnMaxLifetime(5*time.Minute) and db.SetConnMaxIdleTime(30*time.Second)",
  proof="go test ./internal/db -run TestPoolReconnect -v",
  tags=["database", "connection-pool", "reliability"],
  language="go",
  scope="universal"
)
```
