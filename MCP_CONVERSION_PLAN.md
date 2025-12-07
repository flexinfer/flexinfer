# MCP Server Conversion Plan

## Overview

This document tracks the migration of non-Go MCP servers to native Go implementations in `services/loom-core/`.

## Current Go MCP Servers (Complete)

| Server | Binary | Status |
|--------|--------|--------|
| tavily | `mcp-tavily` | Done |
| github | `mcp-github` | Done |
| cloudflare | `mcp-cloudflare` | Done |
| k8s_apps_k3s | `mcp-k8s-ops` | Done |
| longhorn_k3s | `mcp-k8s-ops` | Done |
| ops_mcp | `mcp-ops` | Done |
| server_mgmt | `mcp-server-mgmt` | Done |
| asus_router | `mcp-asus-router` | Done |
| time | `mcp-time` | Done |
| git | `mcp-git` | Done |
| git_worktree | `mcp-git-worktree` | Done |
| prometheus | `mcp-prometheus` | Done |
| loki | `mcp-loki` | Done |
| grafana | `mcp-grafana` | Done |
| morph_embeddings | `mcp-morph-embeddings` | Done |
| qdrant | `mcp-qdrant` | Done |
| gitlab | `mcp-gitlab` | Done |
| memory | `mcp-memory` | Done |
| sequentialthinking | `mcp-sequentialthinking` | Done |
| k8s_harvester_infra | `mcp-k8s-ops` | Done (uses same binary as k8s_apps_k3s with different KUBECONFIG) |
| zep | `mcp-zep` | Done |
| morph_fast_apply | `mcp-morph-fast-apply` | Done |
| youtube | `mcp-youtube` | Done |
| godot_debug | `mcp-godot` | Done |

## Non-Go MCP Servers (Keeping as-is)

### Skipped (Proprietary Backend)

| Server | Current Implementation | Notes |
|--------|----------------------|-------|
| **context7** | npx `@upstash/context7-mcp` | Requires Upstash proprietary backend API - keep as npx |

### Skipped (Complex)

| Server | Current Implementation | Notes |
|--------|----------------------|-------|
| **puppeteer** | npx `@modelcontextprotocol/server-puppeteer` | Complex browser automation - keep as npx |

## Conversion Template

For each conversion, create:

```
services/loom-core/cmd/mcp-<name>/
├── main.go          # MCP server entry point
└── handlers.go      # Tool implementations (optional, can be in main.go)
```

### Minimal MCP Server Structure

```go
package main

import (
    "github.com/crb2nu/loom/pkg/mcp"
)

func main() {
    server := mcp.NewServer("mcp-<name>", "<description>")

    // Register tools
    server.RegisterTool("tool_name", "description", handleToolName)

    // Run stdio server
    if err := server.Run(); err != nil {
        log.Fatal(err)
    }
}

func handleToolName(params map[string]any) (any, error) {
    // Implementation
    return result, nil
}
```

## Registry Updates

After converting a server, update `mcp/context/registry.yaml`:

```yaml
- name: <server_name>
  common:
    command: "${repo}/services/loom-core/bin/mcp-<name>"
    args: []
```

Remove the npx/python fallbacks from targets if Go version is complete.

## Timeline

1. **Phase 1**: gitlab, memory, sequentialthinking ✓ COMPLETE
2. **Phase 2**: k8s_harvester_infra ✓ COMPLETE (context7 skipped - proprietary backend)
3. **Phase 3**: zep, morph_fast_apply ✓ COMPLETE
4. **Phase 4**: youtube, godot_debug ✓ COMPLETE

**CONVERSION COMPLETE** - 26 Go MCP servers. Only puppeteer (browser automation) and context7 (proprietary) remain as npx.

## Testing Checklist

For each converted server:
- [ ] Implements all tools from original
- [ ] Returns compatible JSON-RPC responses
- [ ] Handles errors gracefully
- [ ] Has unit tests in `*_test.go`
- [ ] Works with loom daemon pool
- [ ] Registry updated with Go binary path
