<script>
  import { coordinationStore } from '../stores/coordination.svelte.ts';
  import { presenceActionsStore } from '../stores/presenceActions.svelte.ts';
  import { mergeQueueStore } from '../stores/mergeQueue.svelte.ts';
  import { orchestrationStore } from '../stores/orchestration.svelte.ts';
  import PanelShell from './shared/PanelShell.svelte';
  import EmptyState from './shared/EmptyState.svelte';
  import MetricCard from './shared/MetricCard.svelte';
  import DispatchTaskModal from './presence/DispatchTaskModal.svelte';
  import RecommendationsSection from './dispatch/RecommendationsSection.svelte';
  import MergeQueueSection from './dispatch/MergeQueueSection.svelte';
  import FileConflictsSection from './dispatch/FileConflictsSection.svelte';
  import DispatchHistorySection from './dispatch/DispatchHistorySection.svelte';

  $effect(() => {
    coordinationStore.startPolling(15000);
    mergeQueueStore.startPolling(30000);
    orchestrationStore.startPolling(30000);
    return () => {
      coordinationStore.stopPolling();
      mergeQueueStore.stopPolling();
      orchestrationStore.stopPolling();
    };
  });

  let summary = $derived(coordinationStore.summary);
  let agents = $derived(coordinationStore.agents);
  let blockers = $derived(coordinationStore.activeBlockers);
  let namespaces = $derived(coordinationStore.riskyNamespaces);
  let relations = $derived(coordinationStore.relations);
  let attentionAgents = $derived(coordinationStore.topAttentionAgents);

  let recsCollapsed = $state(!orchestrationStore.hasRecommendations);
  let mergeCollapsed = $state(mergeQueueStore.totalCount === 0);
  let conflictsCollapsed = $state(!mergeQueueStore.hasConflicts);
  let historyCollapsed = $state(true);

  let sortKey = $state('attention');
  let sortDir = $state('desc');

  let sortedAgents = $derived.by(() => {
    const items = [...agents];
    items.sort((a, b) => {
      let cmp = 0;
      if (sortKey === 'attention') {
        cmp = (a.needs_attention === b.needs_attention ? 0 : a.needs_attention ? -1 : 1);
        if (cmp === 0) cmp = (b.blocking_others + b.blocked_tasks) - (a.blocking_others + a.blocked_tasks);
      } else if (sortKey === 'agent_id') {
        cmp = a.agent_id.localeCompare(b.agent_id);
      } else if (sortKey === 'tasks') {
        cmp = b.task_count - a.task_count;
      } else if (sortKey === 'blockers') {
        cmp = (b.blocking_others + b.blocked_by_others) - (a.blocking_others + a.blocked_by_others);
      }
      return sortDir === 'asc' ? -cmp : cmp;
    });
    return items;
  });

  function handleSort(key) {
    if (sortKey === key) {
      sortDir = sortDir === 'asc' ? 'desc' : 'asc';
    } else {
      sortKey = key;
      sortDir = 'desc';
    }
  }

  function dispatchTo(agentId) {
    presenceActionsStore.onOpenDispatch(agentId);
  }

  function agentStatusColor(status) {
    if (status === 'active') return 'var(--success)';
    if (status === 'idle') return 'var(--warning)';
    return 'var(--fg-muted)';
  }
</script>

<PanelShell
  title="Dispatch"
  icon={'\u2692'}
  count={agents.length}
  loading={coordinationStore.loading}
  empty={agents.length === 0}
  emptyIcon={'\u25C8'}
  emptyMessage="No agents registered"
  emptyHint="Start agents to see coordination and dispatch signals"
>
  {#snippet header()}
    <div class="dispatch-intro">
      <div class="dispatch-summary">
        <div class="dispatch-summary-eyebrow">Coordination now</div>
        <div class="dispatch-summary-line">
          {summary.agents_needing_attention} agent{summary.agents_needing_attention === 1 ? '' : 's'} need attention
          {#if summary.cross_agent_blockers > 0}
            · {summary.cross_agent_blockers} cross-agent blocker{summary.cross_agent_blockers === 1 ? '' : 's'}
          {/if}
          {#if summary.conflict_files > 0}
            · {summary.conflict_files} conflicted file{summary.conflict_files === 1 ? '' : 's'}
          {/if}
        </div>
        <div class="dispatch-summary-copy">
          Route work where it can move, keep blockers visible, and watch for namespaces that need a human nudge before they stall.
        </div>
      </div>
      <div class="dispatch-metrics">
        <MetricCard label="Active Namespaces" value={summary.active_namespaces} />
        <MetricCard label="At Risk" value={summary.namespaces_at_risk} color={summary.namespaces_at_risk > 0 ? 'var(--warning)' : 'var(--fg-primary)'} />
        <MetricCard label="Conflicts" value={summary.conflict_files} color={summary.conflict_files > 0 ? 'var(--error)' : 'var(--fg-primary)'} />
        <MetricCard label="X-Agent Blockers" value={summary.cross_agent_blockers} color={summary.cross_agent_blockers > 0 ? 'var(--warning)' : 'var(--fg-primary)'} />
        <MetricCard label="Orphan Tasks" value={summary.orphan_tasks} />
        <MetricCard label="Idle Holders" value={summary.idle_claim_holders} color={summary.idle_claim_holders > 0 ? 'var(--warning)' : 'var(--fg-primary)'} />
        <MetricCard label="Merge Ready" value={summary.merge_ready_branches ?? 0} color={(summary.merge_ready_branches ?? 0) > 0 ? 'var(--success)' : 'var(--fg-primary)'} />
        <MetricCard label="System Load" value={orchestrationStore.systemLoadPct} color={orchestrationStore.systemLoad > 0.8 ? 'var(--error)' : orchestrationStore.systemLoad > 0.5 ? 'var(--warning)' : 'var(--fg-primary)'} />
      </div>
    </div>
  {/snippet}

  <div class="dispatch-layout">
    <RecommendationsSection bind:collapsed={recsCollapsed} />

    <section class="dispatch-section">
      <div class="section-head">
        <h3 class="section-title">Agent roster</h3>
        <div class="section-subtitle">
          {attentionAgents.length} attention agent{attentionAgents.length === 1 ? '' : 's'} · {namespaces.length} risky namespace{namespaces.length === 1 ? '' : 's'}
        </div>
      </div>
      <div class="table-wrap">
        <table class="dispatch-table">
          <thead>
            <tr>
              <th class="sortable" onclick={() => handleSort('agent_id')}>
                Agent {sortKey === 'agent_id' ? (sortDir === 'asc' ? '\u25B2' : '\u25BC') : ''}
              </th>
              <th>Status</th>
              <th>Namespace</th>
              <th class="sortable" onclick={() => handleSort('tasks')}>
                Tasks {sortKey === 'tasks' ? (sortDir === 'asc' ? '\u25B2' : '\u25BC') : ''}
              </th>
              <th class="sortable" onclick={() => handleSort('blockers')}>
                Blockers {sortKey === 'blockers' ? (sortDir === 'asc' ? '\u25B2' : '\u25BC') : ''}
              </th>
              <th>Claims</th>
              <th>Merge</th>
              <th class="sortable" onclick={() => handleSort('attention')}>
                Attention {sortKey === 'attention' ? (sortDir === 'asc' ? '\u25B2' : '\u25BC') : ''}
              </th>
              <th>Action</th>
            </tr>
          </thead>
          <tbody>
            {#each sortedAgents as agent (agent.agent_id)}
              <tr class:attention={agent.needs_attention}>
                <td class="cell-agent">{agent.agent_id}</td>
                <td>
                  <span class="status-pill" style="color: {agentStatusColor(agent.status)}">{agent.status}</span>
                </td>
                <td class="cell-ns">{agent.namespace || '\u2014'}</td>
                <td class="cell-num">
                  {agent.task_count}
                  {#if agent.blocked_tasks > 0}
                    <span class="blocked-badge">{agent.blocked_tasks} blocked</span>
                  {/if}
                </td>
                <td class="cell-num">
                  {#if agent.blocking_others > 0}
                    <span class="blocking-badge">{agent.blocking_others} blocking</span>
                  {/if}
                  {#if agent.blocked_by_others > 0}
                    <span class="blocked-badge">{agent.blocked_by_others} blocked by</span>
                  {/if}
                  {#if agent.blocking_others === 0 && agent.blocked_by_others === 0}
                    \u2014
                  {/if}
                </td>
                <td class="cell-num">
                  {agent.claim_count}
                  {#if agent.conflict_files > 0}
                    <span class="conflict-badge">{agent.conflict_files} conflict</span>
                  {/if}
                </td>
                <td class="cell-num">
                  {#if agent.merge_ready}
                    <span class="merge-ready-badge">{'\u2713'} ready</span>
                  {:else if agent.merge_blockers?.length}
                    <span class="merge-blocked-badge">{agent.merge_blockers.length} blocker{agent.merge_blockers.length === 1 ? '' : 's'}</span>
                  {:else}
                    \u2014
                  {/if}
                </td>
                <td>
                  {#if agent.needs_attention}
                    <span class="attention-indicator" title={agent.attention_reasons?.join(', ') || 'Needs attention'}>
                      {'\u26A0'}
                    </span>
                  {:else}
                    <span class="ok-indicator">{'\u2713'}</span>
                  {/if}
                </td>
                <td>
                  <button class="btn btn-sm btn-dispatch" onclick={() => dispatchTo(agent.agent_id)}>
                    Dispatch
                  </button>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </section>

    <section class="dispatch-section">
      <div class="section-head">
        <h3 class="section-title">Blocking chains</h3>
        <div class="section-subtitle">Dependencies that may need a human nudge or reroute</div>
      </div>
      {#if blockers.length > 0}
        <div class="blocker-list">
          {#each blockers as blocker (blocker.task_id + blocker.blocked_by_task_id)}
            <div class="blocker-card" class:cross-agent={blocker.cross_agent}>
              <div class="blocker-task">{blocker.task_title || blocker.task_id}</div>
              <div class="blocker-arrow">{'\u2190'} blocked by</div>
              <div class="blocker-dep">{blocker.blocked_by_task_title || blocker.blocked_by_task_id}</div>
              {#if blocker.cross_agent}
                <div class="blocker-meta">
                  {blocker.task_agent_id || '?'} {'\u2192'} {blocker.blocked_by_agent_id || '?'}
                  <span class="cross-agent-tag">cross-agent</span>
                </div>
              {/if}
            </div>
          {/each}
        </div>
      {:else}
        <EmptyState
          icon={'\u2713'}
          heading="No active blockers"
          description="The current dependency graph is clear enough for direct dispatch."
          compact
        />
      {/if}
    </section>

    <section class="dispatch-section">
      <div class="section-head">
        <h3 class="section-title">Relation map</h3>
        <div class="section-subtitle">Cross-links and pressure points worth checking before work expands</div>
      </div>
      {#if relations.length > 0}
        <div class="relation-list">
          {#each relations.slice(0, 8) as rel}
            <div class="relation-card" class:severe={rel.severity === 'high'}>
              <span class="relation-kind">{rel.kind}</span>
              <span class="relation-edge">{rel.source_label} {'\u2192'} {rel.target_label}</span>
              {#if rel.detail}
                <span class="relation-detail">{rel.detail}</span>
              {/if}
            </div>
          {/each}
        </div>
      {:else}
        <EmptyState
          icon={'\u25C8'}
          heading="No active relations"
          description="There are no pressure points that need a coordination review right now."
          compact
        />
      {/if}
    </section>

    <MergeQueueSection bind:collapsed={mergeCollapsed} />
    <FileConflictsSection bind:collapsed={conflictsCollapsed} />
    <DispatchHistorySection bind:collapsed={historyCollapsed} />
  </div>
</PanelShell>

<DispatchTaskModal />

<style>
  .dispatch-metrics {
    display: flex;
    gap: var(--space-2);
    padding: 0;
    overflow-x: auto;
  }

  .dispatch-intro {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    padding: var(--space-2) var(--space-3) var(--space-3);
  }

  .dispatch-summary {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
    padding: var(--space-3);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    background: linear-gradient(180deg, color-mix(in srgb, var(--bg-tertiary) 72%, transparent), var(--bg-secondary));
  }

  .dispatch-summary-eyebrow {
    font-size: var(--text-xs);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-muted);
    font-weight: 600;
  }

  .dispatch-summary-line {
    font-size: var(--text-base);
    font-weight: 600;
    color: var(--fg-primary);
  }

  .dispatch-summary-copy {
    font-size: var(--text-sm);
    color: var(--fg-muted);
    max-width: 72ch;
    line-height: var(--leading-normal);
  }

  .dispatch-layout {
    display: flex;
    flex-direction: column;
    gap: var(--space-4);
    padding: 0 var(--space-3) var(--space-3);
    overflow-y: auto;
    flex: 1;
  }

  .dispatch-section {
    flex-shrink: 0;
  }

  .section-title {
    font-size: var(--text-xs);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-muted);
    margin: 0 0 var(--space-2) 0;
    padding: 0;
  }

  .section-head {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
    gap: var(--space-2);
    margin-bottom: var(--space-2);
  }

  .section-subtitle {
    font-size: var(--text-xs);
    color: var(--fg-muted);
    text-align: right;
    line-height: var(--leading-tight);
  }

  .table-wrap {
    overflow-x: auto;
  }

  .dispatch-table {
    width: 100%;
    border-collapse: collapse;
    font-size: var(--text-sm);
  }

  .dispatch-table thead {
    position: sticky;
    top: 0;
    z-index: 2;
    background: var(--bg-secondary);
  }

  .dispatch-table th {
    text-align: left;
    padding: var(--space-1) var(--space-2);
    font-size: var(--text-xs);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-muted);
    border-bottom: 1px solid var(--border);
    white-space: nowrap;
    user-select: none;
  }

  .dispatch-table th.sortable {
    cursor: pointer;
  }

  .dispatch-table th.sortable:hover {
    color: var(--fg-primary);
  }

  .dispatch-table td {
    padding: var(--space-1) var(--space-2);
    border-bottom: 1px solid var(--border);
    color: var(--fg-secondary);
    vertical-align: middle;
  }

  .dispatch-table tr:hover {
    background: var(--bg-tertiary);
  }

  .dispatch-table tr.attention {
    border-left: 2px solid var(--warning);
  }

  .cell-agent {
    font-family: var(--font-mono);
    font-weight: 500;
    color: var(--fg-primary);
    white-space: nowrap;
  }

  .cell-ns {
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    max-width: 200px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .cell-num {
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    white-space: nowrap;
  }

  .status-pill {
    font-size: var(--text-xs);
    font-family: var(--font-mono);
    font-weight: 500;
  }

  .blocked-badge,
  .blocking-badge,
  .conflict-badge {
    display: inline-block;
    font-size: 9px;
    padding: 1px 4px;
    border-radius: var(--radius-sm);
    margin-left: 4px;
  }

  .blocked-badge {
    background: rgba(var(--error-rgb, 255, 85, 85), 0.15);
    color: var(--error);
  }

  .blocking-badge {
    background: rgba(var(--warning-rgb, 255, 170, 51), 0.15);
    color: var(--warning);
  }

  .conflict-badge {
    background: rgba(var(--error-rgb, 255, 85, 85), 0.15);
    color: var(--error);
  }

  .merge-ready-badge {
    display: inline-block;
    font-size: 9px;
    padding: 1px 4px;
    border-radius: var(--radius-sm);
    background: rgba(var(--success-rgb, 80, 200, 120), 0.15);
    color: var(--success);
  }

  .merge-blocked-badge {
    display: inline-block;
    font-size: 9px;
    padding: 1px 4px;
    border-radius: var(--radius-sm);
    background: rgba(var(--warning-rgb, 255, 170, 51), 0.15);
    color: var(--warning);
  }

  .attention-indicator {
    color: var(--warning);
    cursor: help;
  }

  .ok-indicator {
    color: var(--success);
  }

  .btn-dispatch {
    font-size: var(--text-xs);
    padding: 2px 8px;
    border-radius: var(--radius-sm);
    cursor: pointer;
    border: 1px solid var(--accent);
    background: transparent;
    color: var(--accent);
    transition: background var(--transition-fast);
  }

  .btn-dispatch:hover {
    background: var(--accent);
    color: var(--bg-primary);
  }

  /* Blockers */
  .blocker-list {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .blocker-card {
    padding: var(--space-2);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--bg-tertiary);
    font-size: var(--text-xs);
  }

  .blocker-card.cross-agent {
    border-color: var(--warning);
  }

  .blocker-task {
    font-weight: 600;
    color: var(--fg-primary);
    font-family: var(--font-mono);
  }

  .blocker-arrow {
    color: var(--fg-muted);
    margin: 2px 0;
  }

  .blocker-dep {
    color: var(--fg-secondary);
    font-family: var(--font-mono);
  }

  .blocker-meta {
    margin-top: 4px;
    color: var(--fg-muted);
    font-size: 10px;
  }

  .cross-agent-tag {
    display: inline-block;
    padding: 0 4px;
    border-radius: 2px;
    background: rgba(var(--warning-rgb, 255, 170, 51), 0.15);
    color: var(--warning);
    font-size: 9px;
    margin-left: 4px;
  }

  /* Relations */
  .relation-list {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
  }

  .relation-card {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    padding: var(--space-1) var(--space-2);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--bg-tertiary);
    font-size: var(--text-xs);
  }

  .relation-card.severe {
    border-color: var(--error);
  }

  .relation-kind {
    font-weight: 600;
    color: var(--accent);
    font-family: var(--font-mono);
    text-transform: uppercase;
    font-size: 9px;
    min-width: 60px;
  }

  .relation-edge {
    color: var(--fg-primary);
    font-family: var(--font-mono);
  }

  .relation-detail {
    color: var(--fg-muted);
    flex: 1;
    text-align: right;
  }
</style>
