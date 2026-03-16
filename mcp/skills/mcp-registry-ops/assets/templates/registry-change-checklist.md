# Registry Change Checklist

## Change Summary

- What is changing:
- Why:

## Registry Edits

- [ ] Updated `mcp/context/registry.yaml`
- [ ] Updated `common` first; used `targets.*` only for platform-specific overrides
- [ ] Updated `always_allow` (auto-approve) lists intentionally

## Generate + Validate

- [ ] Generated configs (all targets)
- [ ] Generated hub manifests (if relevant)
- [ ] Validated generated configs (no plaintext secrets)

## Smoke Test

- [ ] Restarted / reloaded clients as needed
- [ ] Verified a representative tool call per changed server
- [ ] Reloaded daemon after sync: `loom reload`
- [ ] Confirmed expected tools in aggregated proxy surface via `loom tools list`
- [ ] Executed at least one read-only `agent_context` call through `loom tools call` (if context tools changed)
- [ ] Executed reversible Qdrant collection lifecycle smoke (create/get/delete_collection) (if vector tools changed)
