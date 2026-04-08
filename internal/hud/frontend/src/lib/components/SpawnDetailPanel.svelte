<script lang="ts">
  import { router } from '../stores/router.svelte.ts';
  import { spawnStore } from '../stores/spawn.svelte.ts';
  import type { SpawnState, SpawnTelemetry } from '../stores/spawn.svelte.ts';
  import StatusDot from '../widgets/StatusDot.svelte';
  import BudgetBar from '../widgets/BudgetBar.svelte';
  import ToolsTab from './SpawnTelemetry/ToolsTab.svelte';
  import FilesTab from './SpawnTelemetry/FilesTab.svelte';
  import ErrorsTab from './SpawnTelemetry/ErrorsTab.svelte';
  import UsageTab from './SpawnTelemetry/UsageTab.svelte';

  type TabId = 'tools' | 'files' | 'errors' | 'usage';

  const tabs: Array<{ id: TabId; label: string }> = [
    { id: 'tools', label: 'Tools' },
    { id: 'files', label: 'Files' },
    { id: 'errors', label: 'Errors' },
    { id: 'usage', label: 'Usage' },
  ];

  let loading = $state(false);
  let error = $state<string | null>(null);
  let spawn = $state<SpawnState | null>(null);
  let telemetry = $state<SpawnTelemetry | null>(null);
  let activeTab = $state<TabId>('tools');

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
    spawn !== null && (spawn.status === 'running' || spawn.status === 'creating')
  );

  let messageInput = $state('');
  let sendingMessage = $state(false);

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
          <button class="send-button" onclick={handleSendMessage} disabled={sendingMessage || !messageInput.trim()}>
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
        {#if activeTab === 'tools'}
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

    {#if canStop}
      <div class="actions-row">
        {#if isMultiTurn && spawn.status === 'running'}
          <button class="interrupt-button" onclick={handleInterrupt}>Interrupt</button>
        {/if}
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
