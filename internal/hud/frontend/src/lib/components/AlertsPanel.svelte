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
    gap: var(--space-2);
    height: 100%;
  }

  .panel-header {
    flex-shrink: 0;
    position: relative;
  }

  .panel-header::after {
    content: '';
    position: absolute;
    bottom: 0;
    left: 10%;
    right: 10%;
    height: 1px;
    background: linear-gradient(90deg, transparent, rgba(0, 200, 255, 0.06) 50%, transparent);
    pointer-events: none;
  }

  .tab-bar {
    display: flex;
    gap: var(--space-1);
    border-bottom: 1px solid var(--border);
    padding-bottom: var(--space-1);
  }

  .tab {
    background: none;
    border: none;
    color: var(--fg-muted);
    cursor: pointer;
    padding: var(--space-1) var(--space-2);
    font-size: var(--text-sm);
    border-radius: var(--radius-sm) var(--radius-sm) 0 0;
    letter-spacing: var(--tracking-normal);
    transition: color var(--transition-fast), background var(--transition-fast);
  }

  .tab:hover {
    color: var(--fg-secondary);
    background: var(--bg-elevated);
  }

  .tab.active {
    color: var(--fg-primary);
    border-bottom: 2px solid var(--accent);
    box-shadow: 0 0 6px var(--glow-accent);
  }

  .badge {
    background: var(--error);
    color: var(--bg-primary);
    border-radius: var(--radius-full);
    padding: 0 var(--space-1);
    font-size: var(--text-2xs);
    margin-left: var(--space-1);
    font-weight: 600;
  }

  .loading, .error-msg {
    padding: var(--space-4);
    text-align: center;
    color: var(--fg-muted);
    font-size: var(--text-sm);
  }

  .error-msg {
    color: var(--error);
    background: var(--error-dim);
    border-radius: var(--radius-md);
  }

  /* Alert list */
  .alert-list {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
    overflow-y: auto;
    flex: 1;
  }

  .alert-item {
    display: flex;
    gap: var(--space-2);
    padding: var(--space-2);
    border-radius: var(--radius-md);
    background: var(--bg-secondary);
    border: 1px solid var(--border-subtle);
    position: relative;
    transition: background var(--transition-fast);
  }

  .alert-item::before {
    content: '';
    position: absolute;
    inset: 0;
    border-radius: inherit;
    background: var(--surface-highlight);
    pointer-events: none;
  }

  .alert-item:hover {
    background: var(--bg-tertiary);
  }

  .alert-item.acked {
    opacity: 0.5;
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
    font-size: var(--text-sm);
    font-weight: 600;
    color: var(--fg-primary);
  }

  .alert-time {
    font-size: var(--text-xs);
    color: var(--fg-dim);
    flex-shrink: 0;
    font-family: var(--font-mono);
  }

  .alert-message {
    font-size: var(--text-sm);
    color: var(--fg-muted);
    margin-top: 2px;
    line-height: var(--leading-normal);
  }

  .alert-meta {
    display: flex;
    gap: var(--space-2);
    align-items: center;
    margin-top: var(--space-1);
    font-size: var(--text-xs);
  }

  .severity-badge {
    font-weight: 600;
    text-transform: uppercase;
    font-size: var(--text-2xs);
    letter-spacing: var(--tracking-wide);
  }

  .pipeline-link {
    color: var(--info);
    text-decoration: none;
    transition: color var(--transition-fast);
  }

  .pipeline-link:hover {
    color: var(--fg-primary);
  }

  .pipeline-ref {
    color: var(--fg-muted);
  }

  .acked-label {
    color: var(--fg-dim);
    font-style: italic;
  }

  .ack-btn {
    background: var(--bg-elevated);
    color: var(--fg-primary);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    padding: 2px var(--space-2);
    font-size: var(--text-2xs);
    cursor: pointer;
    transition: background var(--transition-fast), border-color var(--transition-fast);
  }

  .ack-btn:hover {
    background: var(--bg-tertiary);
    border-color: var(--border-focus);
  }

  /* Rules */
  .rules-list {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
  }

  .rule-item {
    display: flex;
    gap: var(--space-2);
    align-items: center;
    padding: var(--space-2);
    border-radius: var(--radius-md);
    background: var(--bg-secondary);
    border: 1px solid var(--border-subtle);
    position: relative;
    transition: background var(--transition-fast);
  }

  .rule-item::before {
    content: '';
    position: absolute;
    inset: 0;
    border-radius: inherit;
    background: var(--surface-highlight);
    pointer-events: none;
  }

  .rule-item:hover {
    background: var(--bg-tertiary);
  }

  .rule-info { flex: 1; }

  .rule-name {
    font-size: var(--text-sm);
    font-weight: 600;
    color: var(--fg-primary);
  }

  .rule-detail {
    font-size: var(--text-xs);
    color: var(--fg-muted);
    letter-spacing: var(--tracking-normal);
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
    background: var(--bg-elevated);
    border-radius: var(--radius-full);
    cursor: pointer;
    transition: background var(--transition-normal);
  }

  .toggle-slider::before {
    content: '';
    position: absolute;
    height: 14px;
    width: 14px;
    left: 2px;
    bottom: 2px;
    background: var(--fg-secondary);
    border-radius: 50%;
    transition: transform var(--transition-normal);
  }

  .toggle input:checked + .toggle-slider {
    background: var(--success);
    box-shadow: 0 0 6px var(--glow-success);
  }

  .toggle input:checked + .toggle-slider::before {
    transform: translateX(14px);
    background: var(--bg-primary);
  }

  /* Auto-fix */
  .autofix-section {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    overflow-y: auto;
    flex: 1;
  }

  .section-title {
    font-size: var(--text-xs);
    font-weight: 600;
    color: var(--fg-muted);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    margin: 0;
  }

  .proposal-item {
    display: flex;
    gap: var(--space-2);
    justify-content: space-between;
    align-items: center;
    padding: var(--space-2);
    border-radius: var(--radius-md);
    background: var(--bg-secondary);
    border: 1px solid var(--border-subtle);
    position: relative;
    transition: background var(--transition-fast);
  }

  .proposal-item::before {
    content: '';
    position: absolute;
    inset: 0;
    border-radius: inherit;
    background: var(--surface-highlight);
    pointer-events: none;
  }

  .proposal-info { flex: 1; min-width: 0; }

  .proposal-desc {
    font-size: var(--text-sm);
    color: var(--fg-primary);
  }

  .proposal-meta {
    font-size: var(--text-xs);
    color: var(--fg-dim);
    margin-top: 2px;
    font-family: var(--font-mono);
  }

  .proposal-actions {
    display: flex;
    gap: var(--space-1);
    flex-shrink: 0;
  }

  .approve-btn, .reject-btn {
    border: none;
    border-radius: var(--radius-sm);
    padding: var(--space-1) var(--space-2);
    font-size: var(--text-xs);
    cursor: pointer;
    font-weight: 600;
    transition: opacity var(--transition-fast);
  }

  .approve-btn:hover, .reject-btn:hover {
    opacity: 0.85;
  }

  .approve-btn {
    background: var(--success-dim);
    color: var(--success);
    border: 1px solid rgba(34, 224, 118, 0.2);
  }

  .reject-btn {
    background: var(--error-dim);
    color: var(--error);
    border: 1px solid rgba(255, 61, 113, 0.2);
  }

  .execution-item {
    display: flex;
    gap: var(--space-2);
    align-items: center;
    padding: var(--space-1) var(--space-2);
    border-radius: var(--radius-md);
    background: var(--bg-secondary);
    border: 1px solid var(--border-subtle);
    font-size: var(--text-sm);
    transition: background var(--transition-fast);
  }

  .execution-item:hover {
    background: var(--bg-tertiary);
  }

  .exec-status {
    font-weight: 600;
    font-size: var(--text-xs);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    flex-shrink: 0;
  }

  .exec-info {
    color: var(--fg-muted);
    font-size: var(--text-xs);
  }
</style>
