# MCP Inventory

## Why

Capture the available MCP servers/resources/templates so planning and implementation can use the right tools without guesswork, with explicit runtime-mode detection and delegation rationale.

## Checklist

- [ ] Detect runtime mode (loom-mode vs direct per-server)
- [ ] List MCP servers
- [ ] Summarize tool counts by server
- [ ] Capture paged deep-dive evidence
- [ ] Record any auth/permission constraints
- [ ] Record delegation plan used and why

## Inventory (paste outputs)

### Runtime Mode Detection

- [ ] Detection method and evidence
- [ ] Loom-mode detected: `yes/no`
- [ ] If loom-mode:
  - [ ] Output of `read_mcp_resource("loom", "loom://config")`
  - [ ] Output of `read_mcp_resource("loom", "loom://servers")`
  - [ ] Output of `read_mcp_resource("loom", "loom://tools/index")`
- [ ] If non-loom mode:
  - [ ] Output of `list_mcp_resources`
  - [ ] Output of `list_mcp_resource_templates`

### Server Inventory

- [ ] Server list
- [ ] Notes per server (capabilities, stability, notable constraints)

### Tool Inventory Summary (Counts by Server)

- [ ] Total tools
- [ ] Count by server
- [ ] Inventory source (`loom://tools/*` or per-server resources/templates)

### Paged Deep-Dive Capture

- [ ] Page strategy (`pageSize`, page ranges)
- [ ] Evidence links/snippets for sampled or full page coverage
- [ ] Server-specific deep dives (`loom://tools/server/{server}/page/{page}` or equivalent)

### Auth/Permissions Constraints

- [ ] Required env vars/tokens
- [ ] Denied/blocked calls and observed error signatures
- [ ] Platform/tooling constraints that affect execution

### Delegation Plan Used and Why

- [ ] Delegation strategy (none/parallel shards/subagents/phased checkpoints)
- [ ] Why this strategy was chosen
- [ ] Merge/reconciliation notes and residual risk

## Notes

- Tool selection guidance, limitations, and any “gotchas”.
