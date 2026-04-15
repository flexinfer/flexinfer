<script lang="ts">
  import { router } from '../stores/router.svelte.ts';
  import { spawnStore } from '../stores/spawn.svelte.ts';
  import type { SpawnState, SpawnTelemetry } from '../stores/spawn.svelte.ts';
  import { fleetStore } from '../stores/fleet.svelte.ts';
  import { traceStore } from '../stores/traces.svelte.ts';
  import { adminFetch, labsAuthStore } from '../stores/labsAuth.svelte.ts';
  import { eventStore } from '../stores/events.svelte.ts';
  import { relativeTime } from '../utils/format.ts';
  import { formatTraceDuration, traceBreakdown, traceStatusVariant } from '../utils/traces.ts';
  import StatusDot from '../widgets/StatusDot.svelte';
  import Badge from '../widgets/Badge.svelte';
  import BudgetBar from '../widgets/BudgetBar.svelte';
  import ActivityTab from './SpawnTelemetry/ActivityTab.svelte';
  import ToolsTab from './SpawnTelemetry/ToolsTab.svelte';
  import FilesTab from './SpawnTelemetry/FilesTab.svelte';
  import ErrorsTab from './SpawnTelemetry/ErrorsTab.svelte';
  import UsageTab from './SpawnTelemetry/UsageTab.svelte';
  import LabsAccessBar from './shared/LabsAccessBar.svelte';
  import ConfirmDialog from './shared/ConfirmDialog.svelte';

  type TabId = 'activity' | 'tools' | 'files' | 'errors' | 'usage';

  const tabs: Array<{ id: TabId; label: string }> = [
    { id: 'activity', label: 'Activity' },
    { id: 'tools', label: 'Tools' },
    { id: 'files', label: 'Files' },
    { id: 'errors', label: 'Errors' },
    { id: 'usage', label: 'Usage' },
  ];
  const tracePollingOwner = Symbol('SpawnDetailPanelTraces');

  let loading = $state(false);
  let error = $state<string | null>(null);
  let spawn = $state<SpawnState | null>(null);
  let telemetry = $state<SpawnTelemetry | null>(null);
  let activeTab = $state<TabId>('activity');
  let eventUnsubs = $state<Array<() => void>>([]);
  let hasAdminToken = $derived(labsAuthStore.hasToken);
  let actionError = $derived(spawnStore.error);

  async function loadDetail(spawnId: string): Promise<void> {
    loading = true;
    error = null;
    try {
      const spawnRes = await fetch(`/api/agent/spawn/${encodeURIComponent(spawnId)}`);

      if (!spawnRes.ok) {
        throw new Error(`spawn lookup failed: HTTP ${spawnRes.status}`);
      }
      const spawnData = await spawnRes.json();
      spawn = spawnData as SpawnState;

      if (labsAuthStore.hasToken) {
        const telRes = await adminFetch(`/api/agent/spawn/${encodeURIComponent(spawnId)}/telemetry`, {
          requireToken: true,
          action: 'Loading spawn telemetry',
        });
        if (telRes.ok) {
          const telData = await telRes.json();
          telemetry = (telData?.telemetry ?? null) as SpawnTelemetry | null;
        } else {
          telemetry = null;
        }
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
    spawnStore.startPolling(10000);
    traceStore.startPolling(15000, tracePollingOwner);
    return () => {
      spawnStore.stopPolling();
      traceStore.stopPolling(tracePollingOwner);
    };
  });

  $effect(() => {
    const id = router.detail;
    eventUnsubs.forEach((unsub) => unsub());
    eventUnsubs = [];
    if (id) {
      loadDetail(id);
      const lifecycleEvents = [
        'agent.spawn.building',
        'agent.spawn.running',
        'agent.spawn.completed',
        'agent.spawn.failed',
        'agent.spawn.stopped',
      ];
      eventUnsubs = [
        ...lifecycleEvents.map((type) => eventStore.on(type, (event) => {
          const spawnId = typeof event.data?.spawn_id === 'string' ? event.data.spawn_id : '';
          if (spawnId === id) {
            loadDetail(id);
          }
        })),
        eventStore.on('agent.spawn.telemetry.delta', (event) => {
          const spawnId = typeof event.data?.spawn_id === 'string' ? event.data.spawn_id : '';
          if (spawnId !== id) return;
          const tokenUsage = (event.data?.token_usage as Record<string, unknown> | undefined) ?? {};
          telemetry = {
            external_session_id: telemetry?.external_session_id,
            model_usage: telemetry?.model_usage,
            turn_count: Number(event.data?.turn_count ?? telemetry?.turn_count ?? 0),
            total_cost_usd: Number(event.data?.total_cost_usd ?? telemetry?.total_cost_usd ?? 0),
            cost_estimated: Boolean(event.data?.cost_estimated ?? telemetry?.cost_estimated ?? false),
            token_usage: {
              input_tokens: Number(tokenUsage.input_tokens ?? telemetry?.token_usage?.input_tokens ?? 0),
              output_tokens: Number(tokenUsage.output_tokens ?? telemetry?.token_usage?.output_tokens ?? 0),
              cache_creation_tokens: Number(tokenUsage.cache_creation_tokens ?? telemetry?.token_usage?.cache_creation_tokens ?? 0),
              cache_read_tokens: Number(tokenUsage.cache_read_tokens ?? telemetry?.token_usage?.cache_read_tokens ?? 0),
            },
            stop_reason: typeof event.data?.stop_reason === 'string' ? event.data.stop_reason : telemetry?.stop_reason,
            last_message: typeof event.data?.last_message === 'string' ? event.data.last_message : telemetry?.last_message,
          };
        }),
      ];
    } else {
      spawn = null;
      telemetry = null;
      error = null;
    }

    return () => {
      eventUnsubs.forEach((unsub) => unsub());
      eventUnsubs = [];
    };
  });

  function statusToDot(status: string): 'healthy' | 'idle' | 'degraded' | 'down' {
    switch (status) {
      case 'running': return 'healthy';
      case 'building': return 'degraded';
      case 'creating': return 'degraded';
      case 'failed': return 'down';
      default: return 'idle';
    }
  }

  function statusColor(status: string): string {
    switch (status) {
      case 'running': return 'var(--color-success, #22c55e)';
      case 'building': return 'var(--color-info, #60a5fa)';
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

  function formatTurns(n: number): string {
    return Number.isFinite(n) ? String(Math.floor(n)) : '0';
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
    spawn !== null && (spawn.status === 'running' || spawn.status === 'creating' || spawn.status === 'building')
  );

  let messageInput = $state('');
  let sendingMessage = $state(false);
  let showStopConfirm = $state(false);
  let showInterruptConfirm = $state(false);
  let showDeleteConfirm = $state(false);

  let isMultiTurn = $derived(
    spawn !== null && spawn.request.multi_turn === true
  );

  async function handleSendMessage(): Promise<void> {
    if (!spawn || !messageInput.trim() || sendingMessage) return;
    sendingMessage = true;
    const ok = await spawnStore.sendMessage(spawn.spawn_id, messageInput.trim());
    if (ok) {
      messageInput = '';
      await loadDetail(spawn.spawn_id);
    }
    sendingMessage = false;
  }

  async function handleInterrupt(): Promise<void> {
    if (!spawn) return;
    await spawnStore.interrupt(spawn.spawn_id);
  }

  async function handleDelete(): Promise<void> {
    if (!spawn) return;
    const ok = await spawnStore.delete(spawn.spawn_id);
    if (ok) router.back();
  }

  let canDelete = $derived(
    spawn !== null && spawn.status !== 'running' && spawn.status !== 'creating' && spawn.status !== 'building'
  );

  // Cross-reference: find the fleet session linked to this spawn's agent_id.
  let linkedSession = $derived(
    spawn ? fleetStore.sessionForAgent(spawn.agent_id) : undefined
  );
  let spawnTraceEntries = $derived.by(() => {
    const agentId = (spawn?.agent_id ?? '').trim();
    if (!agentId) return [];
    return (traceStore.entries ?? []).filter((entry) => (entry.agent_id ?? '') === agentId).slice(0, 5);
  });
  let traceError = $derived(traceStore.error);
  let traceLoading = $derived(traceStore.loading);

  /** Compute a human-readable duration string from a session's started_at. */
  let sessionDuration = $derived.by(() => {
    if (!linkedSession?.started_at) return '';
    const start = new Date(linkedSession.started_at).getTime();
    const end = linkedSession.ended_at ? new Date(linkedSession.ended_at).getTime() : Date.now();
    const diffMs = Math.max(0, end - start);
    const secs = Math.floor(diffMs / 1000);
    if (secs < 60) return `${secs}s`;
    const mins = Math.floor(secs / 60);
    if (mins < 60) return `${mins}m ${secs % 60}s`;
    const hrs = Math.floor(mins / 60);
    return `${hrs}h ${mins % 60}m`;
  });

  function navigateToSession(): void {
    if (linkedSession) {
      router.navigate('agents', 'fleet', linkedSession.id);
    }
  }

  function navigateToTraces(): void {
    if (spawn?.agent_id) {
      router.navigate('activity', 'traces', spawn.agent_id);
      return;
    }
    router.navigate('activity', 'traces');
  }
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

  <LabsAccessBar compact hint="Add the Labs token here to enable protected spawn controls and telemetry tabs." />

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
        {#if linkedSession}
          <button class="session-link-chip" onclick={navigateToSession} title="View linked agent session">
            {linkedSession.active ? '\u25CF' : '\u25CB'} Session
          </button>
        {/if}
        {#if spawn?.agent_id}
          <button class="session-link-chip" onclick={navigateToTraces} title="View recent traces for this agent">
            {'\u25A6'} Traces
          </button>
        {/if}
      </div>
      <div class="metrics-row">
        {#if !spawn.request.max_turns}
          <div class="metric">
            <span class="metric-label">Turns</span>
            <span class="metric-value">{telemetry?.turn_count ?? 0}</span>
          </div>
        {/if}
        {#if !spawn.request.max_cost_usd}
          <div class="metric">
            <span class="metric-label">Cost</span>
            <span class="metric-value">
              {telemetry?.cost_estimated ? '~' : ''}{formatCost(telemetry?.total_cost_usd)}
            </span>
          </div>
        {/if}
        {#if telemetry?.stop_reason}
          <div class="metric">
            <span class="metric-label">Stop reason</span>
            <span class="metric-value">{telemetry.stop_reason}</span>
          </div>
        {/if}
      </div>
      {#if spawn.request.max_cost_usd || spawn.request.max_turns}
        <div class="budget-bars">
          {#if spawn.request.max_cost_usd}
            <BudgetBar
              label="Cost"
              current={telemetry?.total_cost_usd ?? 0}
              max={spawn.request.max_cost_usd}
              formatValue={formatCost}
              costEstimated={telemetry?.cost_estimated ?? false}
            />
          {/if}
          {#if spawn.request.max_turns}
            <BudgetBar
              label="Turns"
              current={telemetry?.turn_count ?? 0}
              max={spawn.request.max_turns}
              formatValue={formatTurns}
            />
          {/if}
        </div>
      {/if}
    </div>

    {#if linkedSession}
      <div class="detail-card session-context-card">
        <div class="card-label">Session Context</div>
        <div class="session-context-row">
          <div class="session-ctx-item">
            <span class="session-ctx-label">Namespace</span>
            <span class="session-ctx-value">{linkedSession.namespace || '---'}</span>
          </div>
          <div class="session-ctx-item">
            <span class="session-ctx-label">Entries</span>
            <span class="session-ctx-value">{linkedSession.entry_count ?? 0}</span>
          </div>
          <div class="session-ctx-item">
            <span class="session-ctx-label">Duration</span>
            <span class="session-ctx-value">{sessionDuration}</span>
          </div>
          <div class="session-ctx-item">
            <span class="session-ctx-label">Started</span>
            <span class="session-ctx-value">{relativeTime(linkedSession.started_at)}</span>
          </div>
        </div>
        <button class="session-navigate-button" onclick={navigateToSession}>
          View session detail
        </button>
      </div>
    {/if}

    {#if spawn?.agent_id}
      <div class="detail-card trace-context-card">
        <div class="trace-card-header">
          <div class="card-label">Recent Traces</div>
          <button class="session-navigate-button trace-open-button" onclick={navigateToTraces}>
            Open full traces
          </button>
        </div>
        {#if traceLoading && spawnTraceEntries.length === 0}
          <div class="trace-preview-empty">Loading recent traces...</div>
        {:else if traceError}
          <div class="trace-preview-empty trace-preview-error">{traceError}</div>
        {:else if !traceStore.enabled}
          <div class="trace-preview-empty">Trace stream unavailable.</div>
        {:else if spawnTraceEntries.length === 0}
          <div class="trace-preview-empty">No recent traces for this agent yet.</div>
        {:else}
          <div class="trace-preview-list">
            {#each spawnTraceEntries as trace, index (`${trace.timestamp}-${trace.server}-${trace.tool}-${index}`)}
              <div class="trace-preview-row">
                <div class="trace-preview-top">
                  <div class="trace-preview-id">
                    <span class="trace-preview-time">{new Date(trace.timestamp).toLocaleTimeString()}</span>
                    <span class="trace-preview-server">{trace.server}</span>
                    <span class="trace-preview-tool">{trace.tool}</span>
                  </div>
                  <div class="trace-preview-badges">
                    <span class="trace-preview-duration">{formatTraceDuration(trace.duration_ms)}</span>
                    <Badge text={trace.status} variant={traceStatusVariant(trace.status)} />
                  </div>
                </div>
                <div class="trace-preview-meta">
                  {#if trace.pipeline_stage}
                    <span class="trace-preview-chip">{trace.pipeline_stage}</span>
                  {/if}
                  {#if traceBreakdown(trace)}
                    <span class="trace-preview-chip trace-preview-breakdown">{traceBreakdown(trace)}</span>
                  {/if}
                </div>
                {#if trace.error}
                  <div class="trace-preview-error">{trace.error}</div>
                {/if}
              </div>
            {/each}
          </div>
        {/if}
      </div>
    {/if}

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

    {#if actionError}
      <div class="detail-card error-card">
        <div class="error-header">
          <div class="card-label">Action error</div>
          <button class="error-dismiss" onclick={() => spawnStore.clearError()}>Dismiss</button>
        </div>
        <div class="error-text">{actionError}</div>
      </div>
    {/if}

    {#if isMultiTurn && spawn.status === 'running'}
      <div class="detail-card multi-turn-card">
        <div class="card-label">Follow-up message</div>
        <textarea
          class="message-input"
          bind:value={messageInput}
          placeholder="Type a message to the agent..."
          rows="3"
        ></textarea>
        <div class="actions-row">
          <button class="send-button" onclick={handleSendMessage} disabled={!hasAdminToken || sendingMessage || !messageInput.trim()}>
            {sendingMessage ? 'Sending...' : 'Send'}
          </button>
        </div>
      </div>
    {/if}

    <div class="telemetry-tabs">
      <div class="tab-strip" role="tablist" aria-label="Spawn telemetry">
        {#each tabs as tab}
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === tab.id}
            class="tab-button"
            class:active={activeTab === tab.id}
            onclick={() => (activeTab = tab.id)}
          >
            {tab.label}
          </button>
        {/each}
      </div>
      <div class="tab-body">
        {#if activeTab === 'activity'}
          <ActivityTab spawnId={spawn.spawn_id} />
        {:else if activeTab === 'tools'}
          <ToolsTab spawnId={spawn.spawn_id} />
        {:else if activeTab === 'files'}
          <FilesTab spawnId={spawn.spawn_id} />
        {:else if activeTab === 'errors'}
          <ErrorsTab spawnId={spawn.spawn_id} />
        {:else if activeTab === 'usage'}
          <UsageTab {telemetry} />
        {/if}
      </div>
    </div>

    {#if canStop || canDelete}
      <div class="actions-row">
        {#if canStop}
          {#if isMultiTurn && spawn.status === 'running'}
            <button class="interrupt-button" onclick={() => (showInterruptConfirm = true)} disabled={!hasAdminToken}>Interrupt</button>
          {/if}
          <button class="stop-button" onclick={() => (showStopConfirm = true)} disabled={!hasAdminToken}>Stop spawn</button>
        {/if}
        {#if canDelete}
          <button class="delete-button" onclick={() => (showDeleteConfirm = true)} disabled={!hasAdminToken}>Remove from history</button>
        {/if}
      </div>
    {/if}

    <ConfirmDialog
      open={showStopConfirm}
      title="Stop spawn?"
      message="This will terminate the running agent. The spawn cannot be resumed after stopping."
      confirmLabel="Stop"
      variant="danger"
      onConfirm={() => { showStopConfirm = false; handleStop(); }}
      onCancel={() => (showStopConfirm = false)}
    />
    <ConfirmDialog
      open={showInterruptConfirm}
      title="Interrupt spawn?"
      message="This sends an interrupt signal to the agent. The agent will attempt to finish its current action and stop gracefully."
      confirmLabel="Interrupt"
      variant="warn"
      onConfirm={() => { showInterruptConfirm = false; handleInterrupt(); }}
      onCancel={() => (showInterruptConfirm = false)}
    />
    <ConfirmDialog
      open={showDeleteConfirm}
      title="Remove spawn from history?"
      message="This permanently removes the spawn record and its telemetry. This cannot be undone."
      confirmLabel="Remove"
      variant="danger"
      onConfirm={() => { showDeleteConfirm = false; handleDelete(); }}
      onCancel={() => (showDeleteConfirm = false)}
    />
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

  .session-link-chip {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 2px 8px;
    margin-left: var(--space-2);
    background: transparent;
    border: 1px solid var(--border);
    border-radius: var(--radius-full);
    color: var(--accent);
    font-size: var(--text-xs);
    font-family: var(--font-mono);
    cursor: pointer;
    transition: border-color var(--transition-fast), background var(--transition-fast);
  }

  .session-link-chip:hover {
    border-color: var(--accent);
    background: rgba(129, 240, 254, 0.08);
  }

  .trace-card-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-2);
    flex-wrap: wrap;
  }

  .metrics-row {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-4);
  }

  .budget-bars {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    margin-top: var(--space-1);
  }

  .telemetry-tabs {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .tab-strip {
    display: flex;
    gap: var(--space-1);
    border-bottom: 1px solid var(--border);
    padding-bottom: 2px;
  }

  .tab-button {
    padding: var(--space-1) var(--space-3);
    background: transparent;
    border: 1px solid transparent;
    border-bottom: none;
    border-radius: var(--radius-xs) var(--radius-xs) 0 0;
    color: var(--fg-secondary);
    font-size: var(--text-sm);
    font-family: var(--font-mono);
    cursor: pointer;
    transition: border-color var(--transition-fast), color var(--transition-fast), background var(--transition-fast);
  }

  .tab-button:hover {
    color: var(--fg-primary);
    background: var(--bg-secondary);
  }

  .tab-button.active {
    color: var(--fg-primary);
    border-color: var(--border);
    background: var(--bg-secondary);
    border-bottom: 1px solid var(--bg-secondary);
    margin-bottom: -1px;
  }

  .tab-body {
    display: flex;
    flex-direction: column;
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

  .error-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }

  .error-dismiss {
    padding: 3px 8px;
    border-radius: var(--radius-xs);
    border: 1px solid rgba(255, 61, 113, 0.3);
    background: transparent;
    color: var(--fg-secondary);
    font-size: var(--text-xs);
    font-family: var(--font-mono);
    cursor: pointer;
    transition: color var(--transition-fast), border-color var(--transition-fast);
  }

  .error-dismiss:hover {
    color: var(--fg-primary);
    border-color: var(--fg-secondary);
  }

  .error-text {
    font-size: var(--text-sm);
    color: var(--error);
    font-family: var(--font-mono);
    white-space: pre-wrap;
    word-break: break-word;
  }

  .multi-turn-card {
    gap: var(--space-2);
  }

  .message-input {
    width: 100%;
    background: transparent;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    padding: var(--space-2);
    color: var(--fg-primary);
    font-family: var(--font-sans);
    font-size: var(--text-sm);
    resize: vertical;
    min-height: 4rem;
    transition: border-color var(--transition-fast);
  }

  .message-input:focus {
    outline: none;
    border-color: var(--accent);
  }

  .send-button,
  .interrupt-button {
    padding: var(--space-1) var(--space-3);
    font-size: var(--text-sm);
    background: transparent;
    border-radius: var(--radius-xs);
    cursor: pointer;
    font-family: var(--font-mono);
    transition: background var(--transition-fast), border-color var(--transition-fast);
  }

  .send-button {
    border: 1px solid rgba(129, 240, 254, 0.3);
    color: var(--accent);
  }

  .send-button:hover:not(:disabled) {
    background: rgba(129, 240, 254, 0.1);
    border-color: var(--accent);
  }

  .send-button:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .interrupt-button {
    border: 1px solid rgba(245, 158, 11, 0.3);
    color: var(--color-warn, #f59e0b);
  }

  .interrupt-button:hover {
    background: rgba(245, 158, 11, 0.1);
    border-color: var(--color-warn, #f59e0b);
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

  .delete-button {
    padding: var(--space-1) var(--space-3);
    font-size: var(--text-sm);
    background: transparent;
    border: 1px solid var(--border);
    color: var(--fg-secondary);
    border-radius: var(--radius-xs);
    cursor: pointer;
    font-family: var(--font-mono);
    margin-left: auto;
    transition: background var(--transition-fast), border-color var(--transition-fast), color var(--transition-fast);
  }

  .delete-button:hover {
    border-color: rgba(255, 61, 113, 0.3);
    color: var(--error);
    background: rgba(255, 61, 113, 0.06);
  }

  .delete-button:disabled {
    opacity: 0.4;
    cursor: not-allowed;
  }

  .session-context-card {
    gap: var(--space-3);
    border-color: rgba(129, 240, 254, 0.15);
  }

  .trace-context-card {
    gap: var(--space-3);
  }

  .session-context-row {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-3);
  }

  .session-ctx-item {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .session-ctx-label {
    font-size: var(--text-xs);
    color: var(--fg-dim);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide, 0.05em);
  }

  .session-ctx-value {
    font-size: var(--text-sm);
    color: var(--fg-primary);
    font-family: var(--font-mono);
  }

  .session-navigate-button {
    align-self: flex-start;
    padding: 4px var(--space-3);
    background: transparent;
    border: 1px solid rgba(129, 240, 254, 0.25);
    border-radius: var(--radius-xs);
    color: var(--accent);
    font-size: var(--text-xs);
    font-family: var(--font-mono);
    cursor: pointer;
    transition: border-color var(--transition-fast), background var(--transition-fast);
  }

  .session-navigate-button:hover {
    border-color: var(--accent);
    background: rgba(129, 240, 254, 0.08);
  }

  .trace-open-button {
    padding-inline: var(--space-2);
  }

  .trace-preview-list {
    display: grid;
    gap: var(--space-2);
  }

  .trace-preview-row {
    padding: 10px 12px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border-subtle);
    background: color-mix(in srgb, var(--bg-primary) 65%, transparent);
  }

  .trace-preview-top,
  .trace-preview-id,
  .trace-preview-badges,
  .trace-preview-meta {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
    align-items: center;
  }

  .trace-preview-top {
    justify-content: space-between;
  }

  .trace-preview-time,
  .trace-preview-server,
  .trace-preview-duration,
  .trace-preview-chip {
    font-family: var(--font-mono);
    font-size: var(--text-xs);
  }

  .trace-preview-server {
    color: var(--fg-secondary);
  }

  .trace-preview-tool {
    font-size: var(--text-sm);
    color: var(--fg-primary);
    font-weight: 600;
  }

  .trace-preview-meta {
    margin-top: 8px;
  }

  .trace-preview-chip {
    padding: 2px 6px;
    border-radius: var(--radius-sm);
    background: var(--bg-tertiary);
    border: 1px solid var(--border-subtle);
    color: var(--fg-secondary);
  }

  .trace-preview-breakdown {
    white-space: normal;
  }

  .trace-preview-empty {
    padding: 10px 12px;
    border-radius: var(--radius-sm);
    border: 1px dashed var(--border-subtle);
    color: var(--fg-dim);
    font-size: var(--text-sm);
    background: color-mix(in srgb, var(--bg-primary) 68%, transparent);
  }

  .trace-preview-error {
    color: var(--error);
    font-size: var(--text-sm);
    line-height: 1.4;
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
