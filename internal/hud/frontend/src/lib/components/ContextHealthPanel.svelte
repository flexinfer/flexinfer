<script>
  import { contextHealthStore } from '../stores/contextHealth.svelte.ts';
  import { toastStore } from '../stores/toasts.svelte.ts';
  import { formatNumber } from '../utils/format.ts';
  import PanelShell from './shared/PanelShell.svelte';
  import Gauge from '../widgets/Gauge.svelte';
  import Badge from '../widgets/Badge.svelte';
  import MetricCard from './shared/MetricCard.svelte';

  $effect(() => {
    contextHealthStore.startPolling(10000);
    return () => { contextHealthStore.stopPolling(); };
  });

  let agents = $derived(contextHealthStore.agents ?? []);
  let systemHealth = $derived(contextHealthStore.systemHealth);
  let totalBudget = $derived(contextHealthStore.totalBudget);
  let totalUsed = $derived(contextHealthStore.totalUsed);
  let compactionQueue = $derived(contextHealthStore.compactionQueue);
  let loading = $derived(contextHealthStore.loading);

  // Health score to variant mapping.
  function healthVariant(score) {
    if (score >= 80) return 'success';
    if (score >= 50) return 'warning';
    return 'error';
  }

  function healthLabel(score) {
    if (score >= 80) return 'Healthy';
    if (score >= 50) return 'Warning';
    return 'Critical';
  }

  function utilizationColor(util) {
    if (util >= 0.8) return 'var(--error)';
    if (util >= 0.6) return 'var(--warning)';
    return 'var(--info)';
  }

  async function triggerCompact(sessionId) {
    const ok = await contextHealthStore.compact(sessionId);
    if (ok) {
      toastStore.success('Compaction triggered');
    } else {
      toastStore.error('Compaction failed');
    }
  }

  function isCompacting(sessionId) {
    return contextHealthStore.compacting.has(sessionId);
  }
</script>

<PanelShell
  title="Context Health"
  icon="&#x1F9E0;"
  count={agents.length}
  {loading}
  empty={agents.length === 0 && !loading}
  emptyMessage="No active agent sessions"
  emptyHint="Context health data appears when agents have active sessions"
>
  {#snippet header()}
    <div class="health-summary">
      <div class="system-gauge">
        <div class="gauge-circle" class:healthy={systemHealth >= 80} class:warning={systemHealth >= 50 && systemHealth < 80} class:critical={systemHealth < 50}>
          <span class="gauge-value">{systemHealth}</span>
          <span class="gauge-unit">/100</span>
        </div>
        <div class="gauge-meta">
          <Badge text={healthLabel(systemHealth)} variant={healthVariant(systemHealth)} />
          <span class="meta-label">System Health</span>
        </div>
      </div>

      <div class="summary-metrics">
        <MetricCard label="Budget Used" value="{formatNumber(totalUsed)} / {formatNumber(totalBudget)}" />
        <MetricCard label="Compaction Queue" value="{compactionQueue}" />
        <MetricCard label="Active Agents" value="{agents.length}" />
      </div>

      {#if totalBudget > 0}
        <Gauge value={totalUsed} max={totalBudget} label="Overall Budget" />
      {/if}
    </div>
  {/snippet}

  <div class="agents-list">
    {#each agents as agent (agent.agent_id)}
      <div class="agent-card">
        <div class="agent-header">
          <div class="agent-info">
            <span class="agent-id">{agent.agent_id}</span>
            <Badge text={healthLabel(agent.health_score)} variant={healthVariant(agent.health_score)} />
            {#if agent.compaction_needed}
              <Badge text="Compact" variant="warning" />
            {/if}
          </div>
          <span class="health-score" style:color={utilizationColor(agent.budget_utilization)}>
            {agent.health_score}
          </span>
        </div>

        <div class="agent-meta">
          <span class="meta-item" title="Namespace">{agent.namespace || '---'}</span>
          <span class="meta-item" title="Last entry age">{agent.last_entry_age}</span>
          {#if agent.stale_entries > 0}
            <span class="meta-item stale" title="Stale entries">{agent.stale_entries} stale</span>
          {/if}
        </div>

        <Gauge
          value={agent.tokens_used}
          max={agent.token_budget}
          label="{formatNumber(agent.tokens_used)} / {formatNumber(agent.token_budget)} tokens"
          color={utilizationColor(agent.budget_utilization)}
        />

        {#if agent.recommendation}
          <div class="recommendation">{agent.recommendation}</div>
        {/if}

        <div class="agent-actions">
          {#if agent.compaction_needed}
            <button
              class="compact-btn"
              onclick={() => triggerCompact(agent.session_id)}
              disabled={isCompacting(agent.session_id)}
            >
              {isCompacting(agent.session_id) ? 'Compacting...' : 'Compact'}
            </button>
          {/if}
        </div>
      </div>
    {/each}
  </div>
</PanelShell>

<style>
  .health-summary {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    padding-bottom: var(--space-2);
    border-bottom: 1px solid var(--border);
    margin-bottom: var(--space-2);
  }

  .system-gauge {
    display: flex;
    align-items: center;
    gap: var(--space-3);
  }

  .gauge-circle {
    display: flex;
    align-items: baseline;
    justify-content: center;
    width: 64px;
    height: 64px;
    border-radius: 50%;
    border: 3px solid var(--success);
    flex-shrink: 0;
    align-items: center;
  }

  .gauge-circle.warning {
    border-color: var(--warning);
  }

  .gauge-circle.critical {
    border-color: var(--error);
  }

  .gauge-value {
    font-size: var(--text-xl);
    font-weight: 700;
    font-family: var(--font-mono);
    color: var(--fg-primary);
  }

  .gauge-unit {
    font-size: var(--text-xs);
    color: var(--fg-muted);
    font-family: var(--font-mono);
  }

  .gauge-meta {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
  }

  .meta-label {
    font-size: var(--text-xs);
    color: var(--fg-muted);
    text-transform: uppercase;
    letter-spacing: 0.5px;
  }

  .summary-metrics {
    display: grid;
    grid-template-columns: repeat(3, 1fr);
    gap: var(--space-2);
  }

  .agents-list {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .agent-card {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    padding: var(--space-3);
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    position: relative;
    transition: border-color var(--transition-fast);
  }

  .agent-card::before {
    content: '';
    position: absolute;
    inset: 0;
    border-radius: inherit;
    background: var(--surface-highlight);
    pointer-events: none;
  }

  .agent-card:hover {
    border-color: color-mix(in srgb, var(--info) 30%, var(--border));
  }

  .agent-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }

  .agent-info {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }

  .agent-id {
    font-size: var(--text-sm);
    font-weight: 600;
    color: var(--fg-primary);
    font-family: var(--font-mono);
  }

  .health-score {
    font-size: var(--text-lg);
    font-weight: 700;
    font-family: var(--font-mono);
  }

  .agent-meta {
    display: flex;
    gap: var(--space-3);
    font-size: var(--text-xs);
    color: var(--fg-muted);
  }

  .meta-item.stale {
    color: var(--warning);
  }

  .recommendation {
    font-size: var(--text-xs);
    color: var(--warning);
    background: var(--warning-dim);
    padding: var(--space-1) var(--space-2);
    border-radius: var(--radius-sm);
    border-left: 2px solid var(--warning);
  }

  .agent-actions {
    display: flex;
    gap: var(--space-2);
    justify-content: flex-end;
  }

  .compact-btn {
    padding: var(--space-1) var(--space-3);
    font-size: var(--text-xs);
    font-weight: 500;
    background: var(--warning-dim);
    color: var(--warning);
    border: 1px solid rgba(255, 184, 48, 0.25);
    border-radius: var(--radius-sm);
    cursor: pointer;
    transition: background var(--transition-fast), box-shadow var(--transition-fast);
  }

  .compact-btn:hover:not(:disabled) {
    background: rgba(255, 184, 48, 0.18);
    box-shadow: 0 0 8px var(--glow-warning);
  }

  .compact-btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
</style>
