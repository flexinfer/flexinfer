<script lang="ts">
  import { router } from '../stores/router.svelte.ts';
  import { spawnStore } from '../stores/spawn.svelte.ts';
  import type { SpawnState } from '../stores/spawn.svelte.ts';
  import StatusDot from '../widgets/StatusDot.svelte';

  // Telemetry shape mirrors internal/hud/bridge/spawn_telemetry.go SpawnTelemetry.
  // Only the fields used by this skeleton are listed; the rest land in slices 13c-13f.
  interface SpawnTokenUsage {
    input_tokens: number;
    output_tokens: number;
    cache_creation_tokens: number;
    cache_read_tokens: number;
  }

  interface SpawnTelemetry {
    external_session_id?: string;
    turn_count: number;
    total_cost_usd: number;
    token_usage: SpawnTokenUsage;
    last_message?: string;
    stop_reason?: string;
  }

  let loading = $state(false);
  let error = $state<string | null>(null);
  let spawn = $state<SpawnState | null>(null);
  let telemetry = $state<SpawnTelemetry | null>(null);

  async function loadDetail(spawnId: string): Promise<void> {
    loading = true;
    error = null;
    try {
      const [spawnRes, telRes] = await Promise.all([
        fetch(`/api/agent/spawn/${encodeURIComponent(spawnId)}`),
        fetch(`/api/agent/spawn/${encodeURIComponent(spawnId)}/telemetry`),
      ]);

      if (!spawnRes.ok) {
        throw new Error(`spawn lookup failed: HTTP ${spawnRes.status}`);
      }
      const spawnData = await spawnRes.json();
      spawn = spawnData as SpawnState;

      if (telRes.ok) {
        const telData = await telRes.json();
        telemetry = (telData?.telemetry ?? null) as SpawnTelemetry | null;
      } else {
        telemetry = null;
      }
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
      spawn = null;
      telemetry = null;
    } finally {
      loading = false;
    }
  }

  $effect(() => {
    const id = router.detail;
    if (id) {
      loadDetail(id);
    } else {
      spawn = null;
      telemetry = null;
      error = null;
    }
  });

  function statusToDot(status: string): 'healthy' | 'idle' | 'degraded' | 'down' {
    switch (status) {
      case 'running': return 'healthy';
      case 'creating': return 'degraded';
      case 'failed': return 'down';
      default: return 'idle';
    }
  }

  function statusColor(status: string): string {
    switch (status) {
      case 'running': return 'var(--color-success, #22c55e)';
      case 'creating': return 'var(--color-info, #3b82f6)';
      case 'completed': return 'var(--color-muted, #6b7280)';
      case 'failed': return 'var(--color-error, #ef4444)';
      case 'stopped': return 'var(--color-warn, #f59e0b)';
      default: return 'var(--color-muted, #6b7280)';
    }
  }

  function formatCost(usd: number | undefined): string {
    const v = typeof usd === 'number' ? usd : 0;
    return `$${v.toFixed(4)}`;
  }

  async function handleStop(): Promise<void> {
    if (!spawn) return;
    await spawnStore.stop(spawn.spawn_id);
    // Refetch to reflect the new state.
    await loadDetail(spawn.spawn_id);
  }

  function handleBack(): void {
    router.back();
  }

  let canStop = $derived(
    spawn !== null && (spawn.status === 'running' || spawn.status === 'creating')
  );
</script>

<div class="panel spawn-detail-panel">
  <div class="detail-header">
    <button class="back-button" onclick={handleBack} aria-label="Back to spawns list">
      &larr; Back
    </button>
    {#if spawn}
      <div class="header-meta">
        <span class="header-project">{spawn.request.project}</span>
        <span class="header-agent-id">{spawn.agent_id}</span>
      </div>
    {/if}
  </div>

  {#if loading && !spawn}
    <div class="detail-loading">Loading spawn detail...</div>
  {:else if error}
    <div class="detail-error">{error}</div>
  {:else if spawn}
    <div class="detail-card status-card">
      <div class="status-row">
        <StatusDot status={statusToDot(spawn.status)} />
        <span class="status-text" style="color: {statusColor(spawn.status)}">{spawn.status}</span>
        {#if telemetry?.external_session_id}
          <span class="external-session-id" title="External session id">
            session: {telemetry.external_session_id}
          </span>
        {/if}
      </div>
      <div class="metrics-row">
        <div class="metric">
          <span class="metric-label">Turns</span>
          <span class="metric-value">{telemetry?.turn_count ?? 0}</span>
        </div>
        <div class="metric">
          <span class="metric-label">Cost</span>
          <span class="metric-value">{formatCost(telemetry?.total_cost_usd)}</span>
        </div>
        {#if telemetry?.stop_reason}
          <div class="metric">
            <span class="metric-label">Stop reason</span>
            <span class="metric-value">{telemetry.stop_reason}</span>
          </div>
        {/if}
      </div>
    </div>

    <div class="detail-card">
      <div class="card-label">Task</div>
      <div class="task-text">{spawn.request.task_description}</div>
    </div>

    {#if telemetry?.last_message}
      <div class="detail-card">
        <div class="card-label">Last message</div>
        <div class="last-message">{telemetry.last_message}</div>
      </div>
    {/if}

    {#if spawn.error}
      <div class="detail-card error-card">
        <div class="card-label">Error</div>
        <div class="error-text">{spawn.error}</div>
      </div>
    {/if}

    <!-- TODO: slice 13c telemetry tabs (tools / files / errors / usage) -->

    {#if canStop}
      <div class="actions-row">
        <button class="stop-button" onclick={handleStop}>Stop spawn</button>
      </div>
    {/if}
  {:else}
    <div class="detail-empty">No spawn selected.</div>
  {/if}
</div>

<style>
  .spawn-detail-panel {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
  }

  .detail-header {
    display: flex;
    align-items: center;
    gap: var(--space-3);
  }

  .back-button {
    padding: 4px var(--space-2);
    background: transparent;
    border: 1px solid var(--border);
    border-radius: var(--radius-xs);
    color: var(--fg-secondary);
    font-size: var(--text-sm);
    font-family: var(--font-mono);
    cursor: pointer;
    transition: border-color var(--transition-fast), color var(--transition-fast);
  }

  .back-button:hover {
    border-color: var(--border-active);
    color: var(--fg-primary);
  }

  .header-meta {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .header-project {
    font-weight: 600;
    font-size: var(--text-base);
    color: var(--fg-primary);
    font-family: var(--font-mono);
  }

  .header-agent-id {
    font-size: var(--text-xs);
    color: var(--fg-dim);
    font-family: var(--font-mono);
  }

  .detail-card {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    padding: var(--space-3);
    background: var(--bg-secondary);
    border-radius: var(--radius-md);
    border: 1px solid var(--border);
  }

  .status-card {
    gap: var(--space-3);
  }

  .status-row {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }

  .status-text {
    font-size: var(--text-sm);
    font-weight: 600;
    text-transform: lowercase;
  }

  .external-session-id {
    font-size: var(--text-xs);
    color: var(--fg-dim);
    font-family: var(--font-mono);
    margin-left: var(--space-2);
  }

  .metrics-row {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-4);
  }

  .metric {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .metric-label {
    font-size: var(--text-xs);
    color: var(--fg-dim);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide, 0.05em);
  }

  .metric-value {
    font-size: var(--text-sm);
    color: var(--fg-primary);
    font-family: var(--font-mono);
    font-variant-numeric: tabular-nums;
  }

  .card-label {
    font-size: var(--text-xs);
    color: var(--fg-dim);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide, 0.05em);
  }

  .task-text {
    font-size: var(--text-sm);
    color: var(--fg-primary);
    font-family: var(--font-mono);
    white-space: pre-wrap;
    word-break: break-word;
    line-height: 1.4;
  }

  .last-message {
    font-size: var(--text-sm);
    color: var(--fg-secondary);
    font-family: var(--font-mono);
    white-space: pre-wrap;
    word-break: break-word;
    line-height: 1.4;
    max-height: 16rem;
    overflow-y: auto;
  }

  .error-card {
    border-color: rgba(255, 61, 113, 0.3);
  }

  .error-text {
    font-size: var(--text-sm);
    color: var(--error);
    font-family: var(--font-mono);
    white-space: pre-wrap;
    word-break: break-word;
  }

  .actions-row {
    display: flex;
    justify-content: flex-end;
    gap: var(--space-2);
  }

  .stop-button {
    padding: var(--space-1) var(--space-3);
    font-size: var(--text-sm);
    background: transparent;
    border: 1px solid rgba(255, 61, 113, 0.3);
    color: var(--error);
    border-radius: var(--radius-xs);
    cursor: pointer;
    font-family: var(--font-mono);
    transition: background var(--transition-fast), border-color var(--transition-fast);
  }

  .stop-button:hover {
    background: var(--error-dim);
    border-color: var(--error);
  }

  .detail-loading,
  .detail-empty {
    padding: var(--space-3);
    color: var(--fg-secondary);
    font-size: var(--text-sm);
    font-family: var(--font-mono);
  }

  .detail-error {
    padding: var(--space-3);
    color: var(--error);
    font-size: var(--text-sm);
    font-family: var(--font-mono);
    background: var(--bg-secondary);
    border: 1px solid rgba(255, 61, 113, 0.3);
    border-radius: var(--radius-md);
  }
</style>
