// liveSessions store — Phase 3 of the spectator plan
// (`.loom/99-implementation-plan-agent-telemetry-spectator-2026-05-04.md`).
//
// Subscribes to the daemon EventBus events emitted by Phase 2.x:
//   - session.start / session.end          (from agentcontext + hooks)
//   - tool.call.start / tool.call.end      (from spawn telemetry + hooks)
//   - agent.status.change                  (from presence transitions)
//
// Data model: a per-agent session map keyed by `session_id`, each carrying
// a fixed-size ring buffer of the last `RECENT_CALLS_PER_SESSION` tool calls.
// All bookkeeping is event-driven — no polling — so the store stays cheap
// even when many sessions are active.
//
// Event payloads are best-effort; missing fields fall back to safe defaults
// rather than crashing the renderer (the producer side at
// `cmd/loom/cmd_agent_event_emit.go` and `internal/hud/bridge/spawn_telemetry.go`
// guarantees redaction at TierPublic before publication).

import { eventStore, type SSEEvent } from './events.svelte.ts';

/** Maximum tool calls retained per session in the ring buffer. */
export const RECENT_CALLS_PER_SESSION = 20;

/** A session entry stays "recently ended" this many ms before disappearing. */
const ENDED_RETENTION_MS = 30_000;

export type AgentStatus = 'active' | 'idle' | 'offline' | 'expired' | 'unknown';

export interface ToolCall {
  call_id: string;
  tool_name: string;
  /** Server name for MCP-routed tools; empty for builtin/native tools. */
  server_name?: string;
  args_redacted?: Record<string, unknown>;
  /** Set on tool.call.end — undefined while the call is in flight. */
  duration_ms?: number;
  exit_code?: number;
  result_summary?: string;
  error?: string;
  status?: string;
  /** Wall-clock string from the producer (ISO-8601). */
  started_at?: string;
  ended_at?: string;
  /** True until tool.call.end arrives. */
  in_flight: boolean;
  /** Backfilled activity is not always a literal MCP tool call. */
  source?: 'tool' | 'context' | 'event' | 'trace';
}

export interface LiveSession {
  session_id: string;
  agent_id: string;
  agent_status: AgentStatus;
  /** Most recent first. Capped at RECENT_CALLS_PER_SESSION. */
  recent_calls: ToolCall[];
  /** Wall-clock when this session entry was first seen by the store. */
  first_seen: number;
  /** Wall-clock of most recent activity (call/start/status change). */
  last_activity: number;
  /** Set when a session.end event arrives; entry is reaped after ENDED_RETENTION_MS. */
  ended_at?: number;
}

class LiveSessionsStore {
  /** Keyed by session_id. Sessions appear when any event mentions them. */
  sessions = $state<Map<string, LiveSession>>(new Map());

  /**
   * Monotonic event counter. Used by the card to render a "last update X ago"
   * indicator and by tests to assert events flowed through.
   */
  eventCount = $state(0);

  private unsubs: Array<() => void> = [];
  private reapTimer: ReturnType<typeof setInterval> | null = null;

  /** Connect to the SSE event stream (idempotent). */
  connect() {
    if (this.unsubs.length > 0) return;

    this.unsubs.push(eventStore.on('session.start', (e) => this.onSessionStart(e)));
    this.unsubs.push(eventStore.on('session.end', (e) => this.onSessionEnd(e)));
    this.unsubs.push(eventStore.on('agent.status.change', (e) => this.onStatusChange(e)));
    this.unsubs.push(eventStore.on('tool.call.start', (e) => this.onToolCallStart(e)));
    this.unsubs.push(eventStore.on('tool.call.end', (e) => this.onToolCallEnd(e)));

    if (!this.reapTimer) {
      this.reapTimer = setInterval(() => this.reapEnded(), 5_000);
    }

    // Seed from currently-active sessions so the panel isn't empty when an
    // operator opens the HUD mid-flight. SSE events take over and overlay
    // status/calls as they arrive; getOrCreate dedups on session_id so a
    // backfill entry and a later session.start for the same id collapse.
    void this.seedFromActiveSessions();
  }

  disconnect() {
    for (const u of this.unsubs) u();
    this.unsubs = [];
    if (this.reapTimer) {
      clearInterval(this.reapTimer);
      this.reapTimer = null;
    }
  }

  /** Active sessions sorted by most-recent activity desc. */
  get visibleSessions(): LiveSession[] {
    return Array.from(this.sessions.values())
      .filter((s) => s.ended_at === undefined || Date.now() - s.ended_at < ENDED_RETENTION_MS)
      .sort((a, b) => b.last_activity - a.last_activity);
  }

  get activeSessionCount(): number {
    return this.visibleSessions.filter((s) => s.ended_at === undefined).length;
  }

  /** In-flight tool calls across all visible sessions. */
  get inFlightCallCount(): number {
    let n = 0;
    for (const s of this.visibleSessions) {
      for (const c of s.recent_calls) {
        if (c.in_flight) n++;
      }
    }
    return n;
  }

  /** Reset state — used by tests. */
  reset() {
    this.sessions = new Map();
    this.eventCount = 0;
  }

  /**
   * Backfill the panel with currently-active sessions from `/api/fleet`.
   * Called once on `connect()`; runs in the background and silently no-ops
   * on failure. SSE events arriving during the fetch are not lost because
   * getOrCreate dedups by session_id and the latest activity timestamp wins.
   *
   * Exposed for tests; production code calls it via connect().
   */
  async seedFromActiveSessions(): Promise<void> {
    try {
      const res = await globalThis.fetch('/api/fleet');
      if (!res.ok) return;
      const data = (await res.json()) as { sessions?: Array<Record<string, unknown>> };
      const sessions = data.sessions ?? [];
      let added = 0;
      const backfills: Array<Promise<void>> = [];
      for (const s of sessions) {
        const sid = stringField(s, 'id');
        const status = stringField(s, 'status');
        const ended = stringField(s, 'ended_at');
        if (!sid || status !== 'active' || ended) continue;
        // Skip sessions an SSE event already populated — those have richer
        // state (recent_calls, agent_status). Don't clobber.
        if (this.sessions.has(sid)) continue;
        const aid = stringField(s, 'agent_id');
        const startedMs = Date.parse(stringField(s, 'started_at')) || Date.now();
        const session: LiveSession = {
          session_id: sid,
          agent_id: aid,
          agent_status: 'unknown',
          recent_calls: [],
          first_seen: startedMs,
          last_activity: startedMs,
        };
        this.sessions.set(sid, session);
        backfills.push(this.backfillSessionActivity(session));
        added++;
      }
      if (added > 0) this.touch();
      if (backfills.length > 0) {
        await Promise.allSettled(backfills);
      }
    } catch {
      // Best-effort: SSE will populate as turns happen.
    }
  }

  private touch() {
    this.eventCount++;
    // Replace the map reference so $state reactivity picks up the change.
    this.sessions = new Map(this.sessions);
  }

  private async backfillSessionActivity(session: LiveSession): Promise<void> {
    if (!session.session_id || session.recent_calls.length > 0) return;
    try {
      const params = new URLSearchParams({ limit: '8' });
      if (session.agent_id) params.set('agent_id', session.agent_id);
      const res = await globalThis.fetch(
        `/api/sessions/${encodeURIComponent(session.session_id)}/trace?${params.toString()}`,
      );
      if (!res.ok) return;
      const trace = (await res.json()) as Record<string, unknown>;
      const calls = traceActivityToCalls(trace);
      const current = this.sessions.get(session.session_id);
      if (!current || calls.length === 0 || current.recent_calls.length > 0) return;
      const recent = calls.slice(0, RECENT_CALLS_PER_SESSION);
      const latest = latestCallTime(recent);
      this.sessions.set(session.session_id, {
        ...current,
        recent_calls: recent,
        last_activity: latest > 0 ? Math.max(current.last_activity, latest) : current.last_activity,
      });
      if (!current.agent_id && stringField(trace, 'agent_id')) {
        this.sessions.get(session.session_id)!.agent_id = stringField(trace, 'agent_id');
      }
      this.touch();
    } catch {
      // Best-effort: live SSE activity will still populate the card.
    }
  }

  private getOrCreate(sessionID: string, agentID: string): LiveSession {
    const existing = this.sessions.get(sessionID);
    if (existing) {
      // Late-arriving agent_id wins over the empty placeholder we may have
      // created when the first event for this session lacked one.
      if (!existing.agent_id && agentID) {
        existing.agent_id = agentID;
      }
      return existing;
    }
    const now = Date.now();
    const fresh: LiveSession = {
      session_id: sessionID,
      agent_id: agentID,
      agent_status: 'unknown',
      recent_calls: [],
      first_seen: now,
      last_activity: now,
    };
    this.sessions.set(sessionID, fresh);
    return fresh;
  }

  private onSessionStart(e: SSEEvent) {
    const sid = stringField(e.data, 'session_id');
    const aid = stringField(e.data, 'agent_id');
    if (!sid) return;
    const session = this.getOrCreate(sid, aid);
    session.last_activity = Date.now();
    session.ended_at = undefined; // re-opened
    this.touch();
  }

  private onSessionEnd(e: SSEEvent) {
    const sid = stringField(e.data, 'session_id');
    if (!sid) return;
    const session = this.sessions.get(sid);
    if (!session) return; // never seen — ignore
    session.ended_at = Date.now();
    session.last_activity = Date.now();
    this.touch();
  }

  private onStatusChange(e: SSEEvent) {
    const aid = stringField(e.data, 'agent_id');
    const status = stringField(e.data, 'status') as AgentStatus;
    if (!aid || !status) return;
    // agent.status.change is keyed on agent_id, not session_id. Update every
    // session belonging to this agent.
    for (const s of this.sessions.values()) {
      if (s.agent_id === aid) {
        s.agent_status = status;
        s.last_activity = Date.now();
      }
    }
    this.touch();
  }

  private onToolCallStart(e: SSEEvent) {
    const sid = stringField(e.data, 'session_id');
    const aid = stringField(e.data, 'agent_id');
    const callID = stringField(e.data, 'call_id');
    const toolName = stringField(e.data, 'tool_name');
    if (!sid || !callID) return;
    const session = this.getOrCreate(sid, aid);
    const call: ToolCall = {
      call_id: callID,
      tool_name: toolName,
      server_name: stringField(e.data, 'server_name') || undefined,
      args_redacted:
        (e.data.args_redacted as Record<string, unknown>) ?? undefined,
      started_at: stringField(e.data, 'started_at') || undefined,
      in_flight: true,
      source: 'tool',
    };
    // Push at front (most recent first) and trim the tail.
    session.recent_calls.unshift(call);
    if (session.recent_calls.length > RECENT_CALLS_PER_SESSION) {
      session.recent_calls.length = RECENT_CALLS_PER_SESSION;
    }
    session.last_activity = Date.now();
    this.touch();
  }

  private onToolCallEnd(e: SSEEvent) {
    const sid = stringField(e.data, 'session_id');
    const callID = stringField(e.data, 'call_id');
    if (!sid) return;
    const session = this.sessions.get(sid);
    if (!session) return;
    const idx = callID ? session.recent_calls.findIndex((c) => c.call_id === callID) : -1;
    if (idx >= 0) {
      const call = session.recent_calls[idx];
      call.in_flight = false;
      const dur = numberField(e.data, 'duration_ms');
      if (dur !== undefined) call.duration_ms = dur;
      const ec = numberField(e.data, 'exit_code');
      if (ec !== undefined) call.exit_code = ec;
      call.result_summary = stringField(e.data, 'result_summary') || undefined;
      call.error = stringField(e.data, 'error') || undefined;
      call.status = stringField(e.data, 'status') || undefined;
      call.ended_at = stringField(e.data, 'ended_at') || undefined;
    } else {
      // No matching start — could be a coarse codex.turn event without prior
      // start. Synthesize a closed entry so the user sees activity.
      const tool = stringField(e.data, 'tool_name') || 'unknown';
      const synthetic: ToolCall = {
        call_id: callID || `synthetic-${Date.now()}`,
        tool_name: tool,
        duration_ms: numberField(e.data, 'duration_ms'),
        exit_code: numberField(e.data, 'exit_code'),
        result_summary: stringField(e.data, 'result_summary') || undefined,
        error: stringField(e.data, 'error') || undefined,
        status: stringField(e.data, 'status') || undefined,
        ended_at: stringField(e.data, 'ended_at') || undefined,
        in_flight: false,
        source: 'tool',
      };
      session.recent_calls.unshift(synthetic);
      if (session.recent_calls.length > RECENT_CALLS_PER_SESSION) {
        session.recent_calls.length = RECENT_CALLS_PER_SESSION;
      }
    }
    session.last_activity = Date.now();
    this.touch();
  }

  private reapEnded() {
    let dirty = false;
    const now = Date.now();
    for (const [sid, s] of this.sessions) {
      if (s.ended_at !== undefined && now - s.ended_at >= ENDED_RETENTION_MS) {
        this.sessions.delete(sid);
        dirty = true;
      }
    }
    if (dirty) this.touch();
  }
}

export const liveSessionsStore = new LiveSessionsStore();

// --- Internal helpers ---

function stringField(data: Record<string, unknown>, key: string): string {
  const v = data?.[key];
  return typeof v === 'string' ? v : '';
}

function numberField(data: Record<string, unknown>, key: string): number | undefined {
  const v = data?.[key];
  if (typeof v === 'number' && Number.isFinite(v)) return v;
  return undefined;
}

function traceActivityToCalls(trace: Record<string, unknown>): ToolCall[] {
  const out: ToolCall[] = [];
  for (const raw of arrayField(trace, 'traces')) {
    const item = objectField(raw);
    const server = stringField(item, 'server');
    const tool = stringField(item, 'tool');
    if (!tool && !server) continue;
    const ts = stringField(item, 'timestamp');
    out.push({
      call_id: stableBackfillID('trace', item, out.length),
      tool_name: tool || server || 'trace',
      server_name: server || undefined,
      duration_ms: numberField(item, 'duration_ms'),
      error: stringField(item, 'error') || undefined,
      status: stringField(item, 'status') || undefined,
      result_summary: stringField(item, 'target') || stringField(item, 'pipeline_stage') || undefined,
      started_at: ts || undefined,
      ended_at: ts || undefined,
      in_flight: false,
      source: 'trace',
    });
  }
  for (const raw of arrayField(trace, 'events')) {
    const item = objectField(raw);
    const eventType = stringField(item, 'event_type');
    if (!eventType) continue;
    const ts = stringField(item, 'timestamp');
    out.push({
      call_id: stableBackfillID('event', item, out.length),
      tool_name: eventType,
      result_summary: eventSummary(item),
      started_at: ts || undefined,
      ended_at: ts || undefined,
      in_flight: false,
      source: 'event',
    });
  }
  for (const raw of arrayField(trace, 'entries')) {
    const item = objectField(raw);
    const entryType = stringField(item, 'entry_type') || 'context';
    const title = stringField(item, 'title');
    const content = stringField(item, 'content');
    const ts = stringField(item, 'timestamp');
    out.push({
      call_id: stableBackfillID('context', item, out.length),
      tool_name: entryType,
      result_summary: title || truncateSummary(content),
      started_at: ts || undefined,
      ended_at: ts || undefined,
      in_flight: false,
      source: 'context',
    });
  }
  return out
    .sort((a, b) => callTime(b) - callTime(a))
    .filter((call, idx, calls) => calls.findIndex((c) => c.call_id === call.call_id) === idx);
}

function arrayField(data: Record<string, unknown>, key: string): unknown[] {
  const v = data?.[key];
  return Array.isArray(v) ? v : [];
}

function objectField(v: unknown): Record<string, unknown> {
  return v && typeof v === 'object' ? (v as Record<string, unknown>) : {};
}

function eventSummary(item: Record<string, unknown>): string | undefined {
  const data = objectField(item.data);
  return (
    stringField(data, 'tool_name') ||
    stringField(data, 'status') ||
    stringField(data, 'summary') ||
    undefined
  );
}

function stableBackfillID(prefix: string, item: Record<string, unknown>, fallback: number): string {
  const id =
    stringField(item, 'id') ||
    stringField(item, 'call_id') ||
    [stringField(item, 'timestamp'), stringField(item, 'server'), stringField(item, 'tool'), stringField(item, 'event_type'), stringField(item, 'entry_type')]
      .filter(Boolean)
      .join(':');
  return `${prefix}-${id || fallback}`;
}

function truncateSummary(s: string): string | undefined {
  if (!s) return undefined;
  const singleLine = s.replace(/\s+/g, ' ').trim();
  return singleLine.length > 140 ? `${singleLine.slice(0, 137)}...` : singleLine;
}

function latestCallTime(calls: ToolCall[]): number {
  return calls.reduce((latest, call) => Math.max(latest, callTime(call)), 0);
}

function callTime(call: ToolCall): number {
  return Date.parse(call.ended_at || call.started_at || '') || 0;
}
