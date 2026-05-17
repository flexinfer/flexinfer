// mcpBridge wraps the (host-dependent) mechanism for calling MCP
// tools from inside a widget iframe. Three host shapes are
// recognised in order of preference:
//
//   1. window.openai.callTool — the OpenAI Apps SDK global. Stable in
//      ChatGPT today.
//   2. window.mcp.callTool — a hypothetical Claude Code Desktop
//      global that the host docs hint at; checked defensively. As of
//      slice 1b-γ the MCP Apps spec does not document an exact global
//      name for widget→host tool calls (the spec covers host→widget
//      notifications and ui/message); when Claude formalises the
//      shape, this is the swap point.
//   3. Hand-rolled postMessage JSON-RPC — last resort for hosts that
//      expose a generic bridge. Returns a rejected promise if no
//      reply arrives within the timeout so callers can surface a
//      friendly error.
//
// When none of the above is present (e.g. a plain browser preview),
// useMockData() returns synthetic fixtures so dev iteration on the
// widget UI doesn't require a live host.

export type CallTool = (name: string, args?: Record<string, unknown>) => Promise<unknown>;

// ToolResultContent matches the MCP Apps `content[]` shape that comes
// back from a tool call (one of text|image|resource); we only consume
// the text variant for relay tools so the type is narrowed here.
export interface ToolTextContent {
  type: "text";
  text: string;
  mimeType?: string;
}

export interface ToolCallResult {
  content: ToolTextContent[];
  isError?: boolean;
  _meta?: Record<string, unknown>;
}

// hostCallTool resolves to whatever host shape we can find. It is
// cached so repeated calls don't re-probe the window object. The
// resolver runs lazily on the first callTool() to give the host time
// to inject its globals before we look.
let resolved: CallTool | "mock" | null = null;

function resolveHostCallTool(): CallTool | "mock" {
  if (resolved) return resolved;
  const w = window as unknown as {
    openai?: { callTool?: CallTool };
    mcp?: { callTool?: CallTool };
  };
  if (typeof w.openai?.callTool === "function") {
    resolved = w.openai.callTool.bind(w.openai);
    return resolved;
  }
  if (typeof w.mcp?.callTool === "function") {
    resolved = w.mcp.callTool.bind(w.mcp);
    return resolved;
  }
  // No host bridge — fall through to mock data so dev preview works.
  resolved = "mock";
  return resolved;
}

// callTool invokes one MCP tool on whatever host bridge is available
// and normalises the result into a ToolCallResult. Mock mode returns
// a synthetic payload built from MOCK_RESPONSES below; tests can
// override via setMockResponse before exercising the hook.
export async function callTool(name: string, args: Record<string, unknown> = {}): Promise<ToolCallResult> {
  const host = resolveHostCallTool();
  if (host === "mock") {
    const mock = MOCK_RESPONSES[name];
    if (mock === undefined) {
      return { content: [{ type: "text", text: `{"error":"no mock for ${name}"}` }], isError: true };
    }
    return { content: [{ type: "text", text: mock, mimeType: "application/json" }] };
  }
  try {
    const raw = (await host(name, args)) as unknown;
    return normaliseToolResult(raw);
  } catch (err) {
    return {
      content: [
        { type: "text", text: `bridge call failed: ${(err as Error).message}` },
      ],
      isError: true,
    };
  }
}

// hostKind exposes which path callTool will take. Useful for the UI
// to render a "preview-only mock data" hint when no host is detected.
export function hostKind(): "openai" | "mcp" | "mock" {
  const host = resolveHostCallTool();
  if (host === "mock") return "mock";
  // Re-probe to discriminate openai vs mcp without keeping more state.
  const w = window as unknown as { openai?: { callTool?: CallTool } };
  return typeof w.openai?.callTool === "function" ? "openai" : "mcp";
}

function normaliseToolResult(raw: unknown): ToolCallResult {
  // Hosts return either the MCP CallToolResult shape directly, or a
  // wrapped envelope. Tolerate both.
  if (raw && typeof raw === "object" && Array.isArray((raw as ToolCallResult).content)) {
    return raw as ToolCallResult;
  }
  return { content: [{ type: "text", text: String(raw ?? "") }] };
}

// envelope wraps a payload in the {ok, data, meta} shape that the
// real HUD mobile API returns via writeMobileJSON. Mocks have to
// match this shape so the hooks' unwrap path is exercised in dev too.
function envelope(data: unknown): string {
  return JSON.stringify({
    ok: true,
    data,
    meta: { request_id: "req_mock", timestamp: new Date().toISOString() },
  });
}

// MOCK_RESPONSES drive the dev preview. Shape mirrors what the real
// HUD returns for each relay path (envelope-wrapped) so the UI can be
// developed faithfully without standing up loomd. The dashboard mock
// is deliberately minimal — enough fields for FleetOverview to render
// every code path but small enough that the bundle size impact is
// negligible.
const MOCK_RESPONSES: Record<string, string> = {
  loom_fleet_get_dashboard: envelope({
    daemon_running: true,
    server_count: 47,
    active_sessions: 3,
    active_agents: 4,
    idle_agents: 1,
    offline_agents: 2,
    updated_at: new Date().toISOString(),
    health: { total_servers: 47, healthy_servers: 44, degraded_servers: 2, down_servers: 1, idle_servers: 0 },
    spawns: { active: 1, total: 12 },
    last_heartbeat: { agent_id: "claude-code", timestamp: new Date().toISOString(), count_1h: 142 },
  }),
  loom_fleet_get_stream: envelope({
    entries: [
      {
        id: "mock-1",
        entry_type: "decision",
        agent_id: "claude-code",
        title: "Picked Combo A (hub-and-spoke) for cross-agent GUI",
        timestamp: new Date(Date.now() - 45_000).toISOString(),
      },
      {
        id: "mock-2",
        entry_type: "finding",
        agent_id: "codex-desktop-019e32a5",
        title: "Codex.app spawns app-server with --listen stdio:// — no external socket",
        timestamp: new Date(Date.now() - 5 * 60_000).toISOString(),
      },
      {
        id: "mock-3",
        entry_type: "task",
        agent_id: "claude-code",
        title: "Ship slice 1b-δ event ticker via /stream relay",
        timestamp: new Date(Date.now() - 12 * 60_000).toISOString(),
      },
      {
        id: "mock-4",
        entry_type: "handoff",
        agent_id: "gemini-cli",
        title: "Mills canary dedupe shipped",
        timestamp: new Date(Date.now() - 60 * 60_000).toISOString(),
      },
    ],
  }),
  loom_fleet_get_handoffs: envelope({
    handoffs: [
      {
        id: "h-mock-1",
        from_agent: "claude-code",
        to_agent: "codex-desktop",
        status: "pending",
        summary: "Finish the gfx906 RALPH slice — runtime fix landed in MR !394",
        context: "Validation passed locally and in CI; next step is rollout + smoke for qwen3-1p7b-tools-radeonvii.",
        created_at: new Date(Date.now() - 18 * 60_000).toISOString(),
      },
      {
        id: "h-mock-2",
        from_agent: "codex-desktop-019e32a5",
        target_agent_id: "claude-code",
        status: "pending",
        summary: "Cross-agent GUI integration plan handoff: ticker UX feedback",
        created_at: new Date(Date.now() - 90 * 60_000).toISOString(),
      },
    ],
    total: 2,
  }),
  loom_fleet_handoff_accept: envelope({
    status: "accepted",
    handoff_id: "h-mock-1",
    result: { ok: true },
  }),
  loom_fleet_handoff_reject: envelope({
    status: "rejected",
    handoff_id: "h-mock-1",
    result: { ok: true },
  }),
};

// setMockResponse is exported so tests (or interactive dev) can swap
// the mock body for one tool. Production code does not call this.
export function setMockResponse(name: string, body: string): void {
  MOCK_RESPONSES[name] = body;
}

// MobileEnvelope describes the {ok, data, meta} wrapper the loom HUD
// mobile API places around every successful response (see
// internal/hud/domain/mobile/auth.go writeMobileJSON). Hooks pass
// raw response text through unwrapEnvelope to get at the payload.
export interface MobileEnvelope<T> {
  ok: boolean;
  data?: T;
  error?: { code?: string; message?: string };
  meta?: { request_id?: string; timestamp?: string };
}

// unwrapEnvelope parses the relay text and returns the inner data
// payload. Accepts an unwrapped body as a fallback so legacy callers
// or future endpoints that bypass writeMobileJSON keep working. An
// envelope with `ok: false` surfaces as an error.
export function unwrapEnvelope<T = unknown>(text: string): { data: T | null; error: string | null } {
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch (err) {
    return { data: null, error: `parse failed: ${(err as Error).message}` };
  }
  if (parsed && typeof parsed === "object" && "ok" in (parsed as object)) {
    const env = parsed as MobileEnvelope<T>;
    if (env.ok === false) {
      const code = env.error?.code ?? "unknown_error";
      const msg = env.error?.message ?? "unspecified HUD error";
      return { data: null, error: `${code}: ${msg}` };
    }
    return { data: (env.data ?? null) as T | null, error: null };
  }
  // Unwrapped fallback: treat the whole parsed value as the payload.
  return { data: parsed as T, error: null };
}
