<script>
  import { mergeQueueStore } from '../../stores/mergeQueue.svelte.ts';
  import MetricCard from '../shared/MetricCard.svelte';
  import EmptyState from '../shared/EmptyState.svelte';

  let { collapsed = $bindable(false) } = $props();

  let summary = $derived(mergeQueueStore.summary);
  let ready = $derived(mergeQueueStore.ready);
  let blocked = $derived(mergeQueueStore.blocked);
  let totalCount = $derived(mergeQueueStore.totalCount);
</script>

<section class="dispatch-section">
  <div class="section-head">
    <button class="section-toggle" onclick={() => collapsed = !collapsed}>
      <span class="toggle-icon">{collapsed ? '\u25B6' : '\u25BC'}</span>
      <h3 class="section-title">Merge queue</h3>
      <span class="section-count">{totalCount}</span>
    </button>
    <div class="section-subtitle">
      {summary.ready_to_merge} ready · {summary.blocked} blocked · {summary.conflict_pairs} conflict pair{summary.conflict_pairs === 1 ? '' : 's'}
    </div>
  </div>

  {#if !collapsed}
    <div class="merge-metrics">
      <MetricCard label="Total" value={summary.total_branches} compact />
      <MetricCard label="Ready" value={summary.ready_to_merge} color={summary.ready_to_merge > 0 ? 'var(--success)' : 'var(--fg-primary)'} compact />
      <MetricCard label="Blocked" value={summary.blocked} color={summary.blocked > 0 ? 'var(--error)' : 'var(--fg-primary)'} compact />
      <MetricCard label="Conflicts" value={summary.conflict_pairs} color={summary.conflict_pairs > 0 ? 'var(--warning)' : 'var(--fg-primary)'} compact />
    </div>

    {#if ready.length > 0}
      <div class="subtable-label">Ready to merge</div>
      <div class="table-wrap">
        <table class="merge-table">
          <thead>
            <tr>
              <th>Agent</th>
              <th>Branch</th>
              <th>Namespace</th>
              <th>Tasks</th>
              <th>Conflicts</th>
            </tr>
          </thead>
          <tbody>
            {#each ready as candidate (candidate.agent_id + candidate.branch)}
              <tr>
                <td class="cell-mono">{candidate.agent_id}</td>
                <td class="cell-mono cell-branch">{candidate.branch}</td>
                <td class="cell-mono cell-ns">{candidate.namespace || '\u2014'}</td>
                <td class="cell-num">{candidate.task_count}</td>
                <td class="cell-num">
                  {#if candidate.conflict_files > 0}
                    <span class="conflict-badge">{candidate.conflict_files}</span>
                  {:else}
                    \u2014
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}

    {#if blocked.length > 0}
      <div class="subtable-label">Blocked</div>
      <div class="table-wrap">
        <table class="merge-table">
          <thead>
            <tr>
              <th>Agent</th>
              <th>Branch</th>
              <th>Blockers</th>
              <th>Blocked Tasks</th>
            </tr>
          </thead>
          <tbody>
            {#each blocked as candidate (candidate.agent_id + candidate.branch)}
              <tr>
                <td class="cell-mono">{candidate.agent_id}</td>
                <td class="cell-mono cell-branch">{candidate.branch}</td>
                <td>
                  {#if candidate.merge_blockers?.length}
                    {#each candidate.merge_blockers as blocker}
                      <span class="blocker-badge">{blocker}</span>
                    {/each}
                  {:else}
                    \u2014
                  {/if}
                </td>
                <td class="cell-num">{candidate.blocked_tasks}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}

    {#if ready.length === 0 && blocked.length === 0}
      <EmptyState
        icon={'\u2713'}
        heading="No branches in merge queue"
        description="All branches are either merged or not yet ready for merge evaluation."
        compact
      />
    {/if}
  {/if}
</section>

<style>
  .merge-metrics {
    display: flex;
    gap: var(--space-2);
    margin-bottom: var(--space-3);
  }

  .subtable-label {
    font-size: var(--text-xs);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-muted);
    margin: var(--space-2) 0 var(--space-1) 0;
  }

  .table-wrap {
    overflow-x: auto;
  }

  .merge-table {
    width: 100%;
    border-collapse: collapse;
    font-size: var(--text-sm);
  }

  .merge-table th {
    text-align: left;
    padding: var(--space-1) var(--space-2);
    font-size: var(--text-xs);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-muted);
    border-bottom: 1px solid var(--border);
    white-space: nowrap;
  }

  .merge-table td {
    padding: var(--space-1) var(--space-2);
    border-bottom: 1px solid var(--border);
    color: var(--fg-secondary);
    vertical-align: middle;
  }

  .merge-table tr:hover {
    background: var(--bg-tertiary);
  }

  .cell-mono {
    font-family: var(--font-mono);
    font-size: var(--text-xs);
  }

  .cell-branch {
    max-width: 200px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .cell-ns {
    max-width: 160px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .cell-num {
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    text-align: center;
  }

  .conflict-badge {
    display: inline-block;
    font-size: 9px;
    padding: 1px 4px;
    border-radius: var(--radius-sm);
    background: rgba(var(--error-rgb, 255, 85, 85), 0.15);
    color: var(--error);
  }

  .blocker-badge {
    display: inline-block;
    font-size: 9px;
    padding: 1px 4px;
    border-radius: var(--radius-sm);
    background: rgba(var(--warning-rgb, 255, 170, 51), 0.15);
    color: var(--warning);
    margin-right: 4px;
    margin-bottom: 2px;
  }

  .section-toggle {
    display: flex;
    align-items: center;
    gap: var(--space-1);
    background: none;
    border: none;
    cursor: pointer;
    padding: 0;
    color: inherit;
  }

  .toggle-icon {
    font-size: 10px;
    color: var(--fg-muted);
    width: 12px;
  }

  .section-toggle .section-title {
    margin: 0;
  }

  .section-count {
    font-family: var(--font-mono);
    font-size: 10px;
    background: var(--bg-tertiary);
    color: var(--fg-secondary);
    padding: 0 5px;
    border-radius: var(--radius-lg);
  }
</style>
