<script lang="ts">
  import EmptyState from './shared/EmptyState.svelte';

  interface Alert {
    id: string;
    rule_id: string;
    rule_name: string;
    severity: string;
    title: string;
    message: string;
    pipeline: { id: number; project: string; ref: string; status: string; url?: string };
    fired_at: string;
    acked_at?: string;
    acked_by?: string;
    autofix_id?: string;
  }

  interface AlertRule {
    id: string;
    name: string;
    enabled: boolean;
    severity: string;
    condition: { type: string; threshold?: number; duration?: number };
    cooldown: number;
  }

  interface Proposal {
    id: string;
    description: string;
    strategy: string;
    confidence: number;
    requires_approval: boolean;
    created_at: string;
  }

  interface Execution {
    id: string;
    proposal_id: string;
    status: string;
    agent_id?: string;
    result?: string;
    started_at: string;
    completed_at?: string;
  }

  let alerts = $state<Alert[]>([]);
  let rules = $state<AlertRule[]>([]);
  let proposals = $state<Proposal[]>([]);
  let executions = $state<Execution[]>([]);
  let loading = $state(true);
  let error = $state('');
  let activeTab = $state<'alerts' | 'rules' | 'autofix'>('alerts');

  let pollTimer: ReturnType<typeof setInterval> | undefined;

  async function fetchAll() {
    try {
      const [alertsRes, rulesRes, proposalsRes, executionsRes] = await Promise.all([
        fetch('/api/alerts?limit=50'),
        fetch('/api/alerts/rules'),
        fetch('/api/autofix/proposals'),
        fetch('/api/autofix/executions'),
      ]);

      if (alertsRes.ok) {
        const data = await alertsRes.json();
        alerts = data.alerts ?? [];
      }
      if (rulesRes.ok) {
        const data = await rulesRes.json();
        rules = data.rules ?? [];
      }
      if (proposalsRes.ok) {
        const data = await proposalsRes.json();
        proposals = data.proposals ?? [];
      }
      if (executionsRes.ok) {
        const data = await executionsRes.json();
        executions = data.executions ?? [];
      }
      error = '';
    } catch (e) {
      error = e instanceof Error ? e.message : 'Failed to fetch alerts';
    } finally {
      loading = false;
    }
  }

  async function ackAlert(alertId: string) {
    try {
      await fetch(`/api/alerts/${alertId}/ack`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ acked_by: 'hud-user' }),
      });
      await fetchAll();
    } catch (e) {
      console.error('Failed to ack alert:', e);
    }
  }

  async function toggleRule(ruleId: string) {
    const updated = rules.map(r => ({
      ...r,
      enabled: r.id === ruleId ? !r.enabled : r.enabled,
    }));
    try {
      await fetch('/api/alerts/rules', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ rules: updated }),
      });
      rules = updated;
    } catch (e) {
      console.error('Failed to update rules:', e);
    }
  }

  async function approveProposal(proposalId: string) {
    try {
      await fetch(`/api/autofix/proposals/${proposalId}/approve`, { method: 'POST' });
      await fetchAll();
    } catch (e) {
      console.error('Failed to approve proposal:', e);
    }
  }

  async function rejectProposal(proposalId: string) {
    try {
      await fetch(`/api/autofix/proposals/${proposalId}/reject`, { method: 'POST' });
      await fetchAll();
    } catch (e) {
      console.error('Failed to reject proposal:', e);
    }
  }

  function severityColor(severity: string): string {
    switch (severity) {
      case 'critical': return 'var(--color-error, #ef4444)';
      case 'warning': return 'var(--color-warn, #f59e0b)';
      case 'info': return 'var(--color-info, #3b82f6)';
      default: return 'var(--color-muted, #6b7280)';
    }
  }

  function statusColor(status: string): string {
    switch (status) {
      case 'running': return 'var(--color-info, #3b82f6)';
      case 'succeeded': return 'var(--color-success, #22c55e)';
      case 'failed': return 'var(--color-error, #ef4444)';
      case 'rejected': return 'var(--color-warn, #f59e0b)';
      default: return 'var(--color-muted, #6b7280)';
    }
  }

  function formatTime(iso: string): string {
    const d = new Date(iso);
    const diff = (Date.now() - d.getTime()) / 1000;
    if (diff < 60) return `${Math.floor(diff)}s ago`;
    if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
    if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
    return `${Math.floor(diff / 86400)}d ago`;
  }

  let unackedCount = $derived(alerts.filter(a => !a.acked_at).length);
  let pendingProposals = $derived(proposals.filter(p => p.requires_approval));
  let runningExecutions = $derived(executions.filter(e => e.status === 'running'));

  $effect(() => {
    fetchAll();
    pollTimer = setInterval(fetchAll, 15000);
    return () => { if (pollTimer) clearInterval(pollTimer); };
  });
</script>

<div class="panel alerts-panel">
  <div class="panel-header">
    <div class="tab-bar">
      <button class="tab" class:active={activeTab === 'alerts'} onclick={() => activeTab = 'alerts'}>
        Alerts {#if unackedCount > 0}<span class="badge">{unackedCount}</span>{/if}
      </button>
      <button class="tab" class:active={activeTab === 'rules'} onclick={() => activeTab = 'rules'}>
        Rules
      </button>
      <button class="tab" class:active={activeTab === 'autofix'} onclick={() => activeTab = 'autofix'}>
        Auto-Fix {#if pendingProposals.length > 0}<span class="badge">{pendingProposals.length}</span>{/if}
      </button>
    </div>
  </div>

  {#if loading}
    <div class="loading">Loading alerts...</div>
  {:else if error}
    <div class="error-msg">{error}</div>
  {:else if activeTab === 'alerts'}
    {#if alerts.length === 0}
      <EmptyState icon="bell" message="No alerts" />
    {:else}
      <div class="alert-list">
        {#each alerts as alert (alert.id)}
          <div class="alert-item" class:acked={!!alert.acked_at}>
            <div class="alert-indicator" style:background-color={severityColor(alert.severity)}></div>
            <div class="alert-content">
              <div class="alert-header">
                <span class="alert-title">{alert.title}</span>
                <span class="alert-time">{formatTime(alert.fired_at)}</span>
              </div>
              <div class="alert-message">{alert.message}</div>
              <div class="alert-meta">
                <span class="severity-badge" style:color={severityColor(alert.severity)}>
                  {alert.severity}
                </span>
                {#if alert.pipeline.url}
                  <a href={alert.pipeline.url} target="_blank" class="pipeline-link">
                    Pipeline #{alert.pipeline.id}
                  </a>
                {:else}
                  <span class="pipeline-ref">Pipeline #{alert.pipeline.id}</span>
                {/if}
                {#if alert.acked_at}
                  <span class="acked-label">Acked by {alert.acked_by}</span>
                {:else}
                  <button class="ack-btn" onclick={() => ackAlert(alert.id)}>Acknowledge</button>
                {/if}
              </div>
            </div>
          </div>
        {/each}
      </div>
    {/if}

  {:else if activeTab === 'rules'}
    <div class="rules-list">
      {#each rules as rule (rule.id)}
        <div class="rule-item">
          <div class="rule-toggle">
            <label class="toggle">
              <input type="checkbox" checked={rule.enabled} onchange={() => toggleRule(rule.id)} />
              <span class="toggle-slider"></span>
            </label>
          </div>
          <div class="rule-info">
            <div class="rule-name">{rule.name}</div>
            <div class="rule-detail">
              {rule.condition.type}
              {#if rule.condition.threshold} | threshold: {rule.condition.threshold}{/if}
              | severity: <span style:color={severityColor(rule.severity)}>{rule.severity}</span>
            </div>
          </div>
        </div>
      {/each}
    </div>

  {:else if activeTab === 'autofix'}
    <div class="autofix-section">
      {#if pendingProposals.length > 0}
        <h4 class="section-title">Pending Proposals</h4>
        {#each pendingProposals as proposal (proposal.id)}
          <div class="proposal-item">
            <div class="proposal-info">
              <div class="proposal-desc">{proposal.description}</div>
              <div class="proposal-meta">
                Strategy: {proposal.strategy} |
                Confidence: {Math.round(proposal.confidence * 100)}% |
                {formatTime(proposal.created_at)}
              </div>
            </div>
            <div class="proposal-actions">
              <button class="approve-btn" onclick={() => approveProposal(proposal.id)}>Approve</button>
              <button class="reject-btn" onclick={() => rejectProposal(proposal.id)}>Reject</button>
            </div>
          </div>
        {/each}
      {:else}
        <EmptyState icon="wrench" message="No pending proposals" />
      {/if}

      {#if runningExecutions.length > 0}
        <h4 class="section-title">Running Executions</h4>
        {#each runningExecutions as exec (exec.id)}
          <div class="execution-item">
            <div class="exec-status" style:color={statusColor(exec.status)}>{exec.status}</div>
            <div class="exec-info">
              {#if exec.agent_id}Agent: {exec.agent_id}{/if}
              | Started {formatTime(exec.started_at)}
            </div>
          </div>
        {/each}
      {/if}

      {#if executions.length > 0}
        <h4 class="section-title">Recent Executions</h4>
        {#each executions.slice(0, 10) as exec (exec.id)}
          <div class="execution-item">
            <div class="exec-status" style:color={statusColor(exec.status)}>{exec.status}</div>
            <div class="exec-info">
              {exec.result || 'No result'}
              {#if exec.completed_at} | Completed {formatTime(exec.completed_at)}{/if}
            </div>
          </div>
        {/each}
      {/if}
    </div>
  {/if}
</div>

<style>
  .alerts-panel {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    height: 100%;
  }
  .panel-header {
    flex-shrink: 0;
  }
  .tab-bar {
    display: flex;
    gap: 0.25rem;
    border-bottom: 1px solid var(--color-border, #333);
    padding-bottom: 0.25rem;
  }
  .tab {
    background: none;
    border: none;
    color: var(--color-muted, #6b7280);
    cursor: pointer;
    padding: 0.25rem 0.5rem;
    font-size: 0.8rem;
    border-radius: 4px 4px 0 0;
  }
  .tab.active {
    color: var(--color-fg, #e5e5e5);
    border-bottom: 2px solid var(--color-accent, #3b82f6);
  }
  .badge {
    background: var(--color-error, #ef4444);
    color: white;
    border-radius: 8px;
    padding: 0 0.35rem;
    font-size: 0.65rem;
    margin-left: 0.25rem;
  }
  .loading, .error-msg {
    padding: 1rem;
    text-align: center;
    color: var(--color-muted, #6b7280);
    font-size: 0.8rem;
  }
  .error-msg { color: var(--color-error, #ef4444); }

  /* Alert list */
  .alert-list {
    display: flex;
    flex-direction: column;
    gap: 0.375rem;
    overflow-y: auto;
    flex: 1;
  }
  .alert-item {
    display: flex;
    gap: 0.5rem;
    padding: 0.5rem;
    border-radius: 6px;
    background: var(--color-surface, #1a1a2e);
  }
  .alert-item.acked {
    opacity: 0.6;
  }
  .alert-indicator {
    width: 3px;
    border-radius: 2px;
    flex-shrink: 0;
  }
  .alert-content {
    flex: 1;
    min-width: 0;
  }
  .alert-header {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
  }
  .alert-title {
    font-size: 0.8rem;
    font-weight: 600;
    color: var(--color-fg, #e5e5e5);
  }
  .alert-time {
    font-size: 0.65rem;
    color: var(--color-muted, #6b7280);
    flex-shrink: 0;
  }
  .alert-message {
    font-size: 0.75rem;
    color: var(--color-muted, #9ca3af);
    margin-top: 0.15rem;
  }
  .alert-meta {
    display: flex;
    gap: 0.5rem;
    align-items: center;
    margin-top: 0.25rem;
    font-size: 0.65rem;
  }
  .severity-badge {
    font-weight: 600;
    text-transform: uppercase;
    font-size: 0.6rem;
  }
  .pipeline-link {
    color: var(--color-accent, #3b82f6);
    text-decoration: none;
  }
  .pipeline-ref {
    color: var(--color-muted, #6b7280);
  }
  .acked-label {
    color: var(--color-muted, #6b7280);
    font-style: italic;
  }
  .ack-btn {
    background: var(--color-surface-hover, #2a2a3e);
    color: var(--color-fg, #e5e5e5);
    border: 1px solid var(--color-border, #333);
    border-radius: 4px;
    padding: 0.1rem 0.4rem;
    font-size: 0.6rem;
    cursor: pointer;
  }

  /* Rules */
  .rules-list {
    display: flex;
    flex-direction: column;
    gap: 0.375rem;
  }
  .rule-item {
    display: flex;
    gap: 0.5rem;
    align-items: center;
    padding: 0.5rem;
    border-radius: 6px;
    background: var(--color-surface, #1a1a2e);
  }
  .rule-info { flex: 1; }
  .rule-name {
    font-size: 0.8rem;
    font-weight: 600;
    color: var(--color-fg, #e5e5e5);
  }
  .rule-detail {
    font-size: 0.7rem;
    color: var(--color-muted, #9ca3af);
  }
  .toggle {
    position: relative;
    display: inline-block;
    width: 32px;
    height: 18px;
  }
  .toggle input { opacity: 0; width: 0; height: 0; }
  .toggle-slider {
    position: absolute;
    inset: 0;
    background: var(--color-surface-hover, #2a2a3e);
    border-radius: 9px;
    cursor: pointer;
    transition: background 0.2s;
  }
  .toggle-slider::before {
    content: '';
    position: absolute;
    height: 14px;
    width: 14px;
    left: 2px;
    bottom: 2px;
    background: white;
    border-radius: 50%;
    transition: transform 0.2s;
  }
  .toggle input:checked + .toggle-slider {
    background: var(--color-success, #22c55e);
  }
  .toggle input:checked + .toggle-slider::before {
    transform: translateX(14px);
  }

  /* Auto-fix */
  .autofix-section {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    overflow-y: auto;
    flex: 1;
  }
  .section-title {
    font-size: 0.75rem;
    font-weight: 600;
    color: var(--color-muted, #9ca3af);
    text-transform: uppercase;
    letter-spacing: 0.05em;
    margin: 0;
  }
  .proposal-item {
    display: flex;
    gap: 0.5rem;
    justify-content: space-between;
    align-items: center;
    padding: 0.5rem;
    border-radius: 6px;
    background: var(--color-surface, #1a1a2e);
  }
  .proposal-info { flex: 1; min-width: 0; }
  .proposal-desc {
    font-size: 0.8rem;
    color: var(--color-fg, #e5e5e5);
  }
  .proposal-meta {
    font-size: 0.65rem;
    color: var(--color-muted, #9ca3af);
    margin-top: 0.15rem;
  }
  .proposal-actions {
    display: flex;
    gap: 0.25rem;
    flex-shrink: 0;
  }
  .approve-btn, .reject-btn {
    border: none;
    border-radius: 4px;
    padding: 0.2rem 0.5rem;
    font-size: 0.65rem;
    cursor: pointer;
  }
  .approve-btn {
    background: var(--color-success, #22c55e);
    color: white;
  }
  .reject-btn {
    background: var(--color-error, #ef4444);
    color: white;
  }
  .execution-item {
    display: flex;
    gap: 0.5rem;
    align-items: center;
    padding: 0.375rem 0.5rem;
    border-radius: 6px;
    background: var(--color-surface, #1a1a2e);
    font-size: 0.75rem;
  }
  .exec-status {
    font-weight: 600;
    font-size: 0.7rem;
    text-transform: uppercase;
    flex-shrink: 0;
  }
  .exec-info {
    color: var(--color-muted, #9ca3af);
    font-size: 0.7rem;
  }
</style>
