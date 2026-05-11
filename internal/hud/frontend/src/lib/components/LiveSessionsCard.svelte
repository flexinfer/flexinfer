<!--
  LiveSessionsCard — Phase 3 of the spectator plan
  (`.loom/99-implementation-plan-agent-telemetry-spectator-2026-05-04.md`).

  Shows every active agent session and the trailing handful of tool calls
  per session. Subscribes to `liveSessionsStore`, which itself listens to
  the SSE event stream for session.start/end, agent.status.change, and
  tool.call.start/end.

  Renders a compact collapsed view by default; clicking a row expands to a
  scrollable modal-ish detail panel showing the session's full ring buffer.
-->

<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { liveSessionsStore, type LiveSession, type ToolCall } from '../stores/liveSessions.svelte.ts';
  import EmptyState from './shared/EmptyState.svelte';

  // agentCount lets the empty state distinguish "no agents connected at all"
  // from "agents connected but idle between turns". Without this, operators
  // who saw N live agents in the footer + zero sessions assumed something
  // was wrong with the spectator pipeline (it was just idle).
  let { agentCount = 0 }: { agentCount?: number } = $props();

  let expandedSessionID: string | null = $state(null);

  onMount(() => {
    liveSessionsStore.connect();
  });

  onDestroy(() => {
    // We intentionally don't disconnect — the store is process-wide and other
    // panels may also subscribe. Disconnect happens on App teardown.
  });

  let sessions = $derived(liveSessionsStore.visibleSessions);
  let activeCount = $derived(liveSessionsStore.activeSessionCount);
  let inFlightCount = $derived(liveSessionsStore.inFlightCallCount);

  function toggleExpand(sid: string) {
    expandedSessionID = expandedSessionID === sid ? null : sid;
  }

  function statusClass(status: string): string {
    switch (status) {
      case 'active':
        return 'status-active';
      case 'idle':
        return 'status-idle';
      case 'offline':
      case 'expired':
        return 'status-offline';
      default:
        return 'status-unknown';
    }
  }

  function formatToolName(call: ToolCall): string {
    if (call.server_name) return `${call.server_name}.${call.tool_name}`;
    return call.tool_name;
  }

  function formatDuration(ms: number | undefined): string {
    if (ms === undefined) return '—';
    if (ms < 1000) return `${Math.round(ms)}ms`;
    return `${(ms / 1000).toFixed(1)}s`;
  }

  function callBadgeClass(call: ToolCall): string {
    if (call.in_flight) return 'badge-pending';
    if (call.error) return 'badge-error';
    if (call.exit_code !== undefined && call.exit_code !== 0) return 'badge-error';
    return 'badge-ok';
  }
</script>

<section class="live-sessions-card" data-testid="live-sessions-card">
  <header class="card-header">
    <h3>Live Sessions</h3>
    <div class="header-meta">
      <span class="meta-pill" data-testid="active-count">
        <span class="meta-num">{activeCount}</span> active
      </span>
      {#if inFlightCount > 0}
        <span class="meta-pill meta-pill-emphasis" data-testid="in-flight-count">
          <span class="meta-num">{inFlightCount}</span> in flight
        </span>
      {/if}
    </div>
  </header>

  {#if sessions.length === 0}
    {#if agentCount > 0}
      <EmptyState
        compact
        heading="{agentCount} agent{agentCount === 1 ? '' : 's'} connected, no active sessions"
        description="Agents are online but idle between turns. Sessions appear when a CLI emits a SessionStart hook."
      />
    {:else}
      <EmptyState
        compact
        heading="No active sessions yet"
        description="Sessions appear when a CLI emits a SessionStart hook."
      />
    {/if}
  {:else}
    <ul class="session-list">
      {#each sessions as session (session.session_id)}
        {@const expanded = expandedSessionID === session.session_id}
        {@const collapsed = expanded ? session.recent_calls : session.recent_calls.slice(0, 4)}
        <li class="session-row" data-testid="session-row" data-session-id={session.session_id}>
          <button
            class="session-row-button"
            type="button"
            aria-expanded={expanded}
            onclick={() => toggleExpand(session.session_id)}
          >
            <span class="agent-id">
              <span class={`status-dot ${statusClass(session.agent_status)}`} aria-hidden="true"></span>
              {session.agent_id || '(unknown agent)'}
            </span>
            <span class="session-id">{session.session_id.slice(0, 8)}</span>
            {#if session.ended_at}
              <span class="ended-pill">ended</span>
            {/if}
            <span class="call-count">
              {session.recent_calls.length} call{session.recent_calls.length === 1 ? '' : 's'}
            </span>
          </button>

          {#if collapsed.length === 0}
            <p class="row-empty">No tool calls yet for this session.</p>
          {:else}
            <ul class="call-list" class:expanded>
              {#each collapsed as call (call.call_id)}
                <li class="call-row">
                  <span class={`badge ${callBadgeClass(call)}`} aria-hidden="true"></span>
                  <span class="tool-name" title={formatToolName(call)}>{formatToolName(call)}</span>
                  <span class="dur">{formatDuration(call.duration_ms)}</span>
                  {#if call.error}
                    <span class="err" title={call.error}>{call.error}</span>
                  {:else if call.result_summary}
                    <span class="summary" title={call.result_summary}>{call.result_summary}</span>
                  {:else if call.in_flight}
                    <span class="summary muted">running…</span>
                  {/if}
                </li>
              {/each}
            </ul>
            {#if !expanded && session.recent_calls.length > 4}
              <p class="more-hint">…and {session.recent_calls.length - 4} more — click to expand</p>
            {/if}
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
</section>

<style>
  .live-sessions-card {
    background: var(--surface-1, #14161a);
    border: 1px solid var(--border-subtle, #262a31);
    border-radius: var(--radius-md, 8px);
    padding: var(--space-md, 12px) var(--space-lg, 16px);
  }

  .card-header {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    margin-bottom: var(--space-sm, 8px);
  }

  .card-header h3 {
    margin: 0;
    font-size: var(--font-size-lg, 14px);
    font-weight: 600;
  }

  .header-meta {
    display: flex;
    gap: var(--space-xs, 6px);
  }

  .meta-pill {
    background: var(--surface-2, #1c1f24);
    border: 1px solid var(--border-subtle, #262a31);
    border-radius: 999px;
    padding: 1px 8px;
    font-size: 11px;
    color: var(--text-muted, #8a929b);
  }

  .meta-pill-emphasis {
    border-color: var(--accent-amber, #d29922);
    color: var(--accent-amber, #d29922);
  }

  .meta-num {
    color: var(--text-default, #e6edf3);
    font-weight: 600;
  }

  .session-list,
  .call-list {
    list-style: none;
    padding: 0;
    margin: 0;
  }

  .session-row {
    border-top: 1px solid var(--border-subtle, #262a31);
    padding-top: var(--space-sm, 8px);
    margin-top: var(--space-sm, 8px);
  }

  .session-row:first-child {
    border-top: none;
    margin-top: 0;
    padding-top: 0;
  }

  .session-row-button {
    display: flex;
    gap: var(--space-sm, 8px);
    align-items: center;
    width: 100%;
    background: none;
    border: none;
    padding: 4px 0;
    color: inherit;
    font: inherit;
    text-align: left;
    cursor: pointer;
  }

  .session-row-button:focus-visible {
    outline: 2px solid var(--accent-blue, #58a6ff);
    outline-offset: 2px;
  }

  .agent-id {
    display: inline-flex;
    align-items: center;
    gap: var(--space-xs, 6px);
    font-weight: 600;
    flex: 1 1 auto;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .session-id {
    font-family: var(--font-mono, monospace);
    color: var(--text-muted, #8a929b);
    font-size: 11px;
  }

  .ended-pill {
    background: var(--surface-2, #1c1f24);
    border-radius: 4px;
    padding: 1px 6px;
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--text-muted, #8a929b);
  }

  .call-count {
    color: var(--text-muted, #8a929b);
    font-size: 11px;
  }

  .status-dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    display: inline-block;
    background: var(--text-muted, #6e7681);
  }

  .status-dot.status-active {
    background: var(--accent-green, #3fb950);
    box-shadow: 0 0 6px rgba(63, 185, 80, 0.5);
  }

  .status-dot.status-idle {
    background: var(--accent-amber, #d29922);
  }

  .status-dot.status-offline {
    background: var(--text-muted, #6e7681);
  }

  .status-dot.status-unknown {
    background: var(--surface-3, #30363d);
  }

  .call-list {
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding-left: 16px;
    margin-top: 4px;
    font-size: 12px;
  }

  .call-list.expanded {
    max-height: 320px;
    overflow-y: auto;
  }

  .call-row {
    display: grid;
    grid-template-columns: 8px minmax(0, 1fr) 56px minmax(0, 2fr);
    align-items: center;
    gap: var(--space-xs, 6px);
  }

  .badge {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    display: inline-block;
  }

  .badge-ok {
    background: var(--accent-green, #3fb950);
  }

  .badge-error {
    background: var(--accent-red, #f85149);
  }

  .badge-pending {
    background: var(--accent-blue, #58a6ff);
    animation: pulse 1.5s ease-in-out infinite;
  }

  @keyframes pulse {
    0%,
    100% {
      opacity: 0.4;
    }
    50% {
      opacity: 1;
    }
  }

  .tool-name {
    font-family: var(--font-mono, monospace);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .dur {
    color: var(--text-muted, #8a929b);
    font-variant-numeric: tabular-nums;
    text-align: right;
  }

  .summary,
  .err {
    color: var(--text-muted, #8a929b);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .summary.muted {
    font-style: italic;
  }

  .err {
    color: var(--accent-red, #f85149);
  }

  .row-empty,
  .more-hint {
    margin: 4px 0 0 0;
    font-size: 11px;
    color: var(--text-muted, #8a929b);
    padding-left: 16px;
  }
</style>
