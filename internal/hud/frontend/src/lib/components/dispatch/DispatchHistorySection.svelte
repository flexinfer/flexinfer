<script>
  import { taskStore } from '../../stores/tasks.svelte.ts';
  import MetricCard from '../shared/MetricCard.svelte';
  import EmptyState from '../shared/EmptyState.svelte';

  let { collapsed = $bindable(false) } = $props();

  let filterStatus = $state('all');

  let dispatched = $derived(taskStore.dispatchedTasks);
  let completionRate = $derived(taskStore.dispatchedCompletionRate);
  let inFlightCount = $derived(taskStore.dispatchedInFlightCount);
  let completedCount = $derived(dispatched.filter((t) => t.status === 'completed').length);

  let filtered = $derived.by(() => {
    if (filterStatus === 'all') return dispatched;
    return dispatched.filter((t) => t.status === filterStatus);
  });

  function priorityColor(priority) {
    const map = { critical: 'var(--error)', high: 'var(--warning)', medium: 'var(--accent)', low: 'var(--fg-muted)' };
    return map[priority] ?? 'var(--fg-secondary)';
  }

  function statusColor(status) {
    const map = { completed: 'var(--success)', in_progress: 'var(--accent)', pending: 'var(--fg-muted)', blocked: 'var(--error)', cancelled: 'var(--fg-muted)' };
    return map[status] ?? 'var(--fg-secondary)';
  }

  function formatRelative(dateStr) {
    if (!dateStr) return '\u2014';
    const d = new Date(dateStr);
    const diff = Date.now() - d.getTime();
    const mins = Math.floor(diff / 60000);
    if (mins < 1) return 'just now';
    if (mins < 60) return `${mins}m ago`;
    const hrs = Math.floor(mins / 60);
    if (hrs < 24) return `${hrs}h ago`;
    return `${Math.floor(hrs / 24)}d ago`;
  }
</script>

<section class="dispatch-section">
  <div class="section-head">
    <button class="section-toggle" onclick={() => collapsed = !collapsed}>
      <span class="toggle-icon">{collapsed ? '\u25B6' : '\u25BC'}</span>
      <h3 class="section-title">Dispatch history</h3>
      <span class="section-count">{dispatched.length}</span>
    </button>
    <div class="section-subtitle">
      {completedCount} completed · {inFlightCount} in-flight · {completionRate}% rate
    </div>
  </div>

  {#if !collapsed}
    <div class="history-metrics">
      <MetricCard label="Dispatched" value={dispatched.length} compact />
      <MetricCard label="Completed" value={completedCount} color={completedCount > 0 ? 'var(--success)' : 'var(--fg-primary)'} compact />
      <MetricCard label="In-Flight" value={inFlightCount} color={inFlightCount > 0 ? 'var(--accent)' : 'var(--fg-primary)'} compact />
      <MetricCard label="Rate" value={`${completionRate}%`} compact />
    </div>

    <div class="filter-row">
      <select class="filter-select" bind:value={filterStatus}>
        <option value="all">All</option>
        <option value="pending">Pending</option>
        <option value="in_progress">In Progress</option>
        <option value="completed">Completed</option>
        <option value="blocked">Blocked</option>
      </select>
    </div>

    {#if filtered.length > 0}
      <div class="table-wrap">
        <table class="history-table">
          <thead>
            <tr>
              <th>Task</th>
              <th>Agent</th>
              <th>Priority</th>
              <th>Status</th>
              <th>Updated</th>
            </tr>
          </thead>
          <tbody>
            {#each filtered.slice(0, 20) as task (task.id)}
              <tr>
                <td class="cell-task">{task.title}</td>
                <td class="cell-mono">{task.agent_id || task.agent || '\u2014'}</td>
                <td>
                  <span class="priority-pill" style="color: {priorityColor(task.priority)}">{task.priority}</span>
                </td>
                <td>
                  <span class="status-pill" style="color: {statusColor(task.status)}">{task.status}</span>
                </td>
                <td class="cell-mono cell-time">{formatRelative(task.updated_at)}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      {#if filtered.length > 20}
        <div class="more-hint">{filtered.length - 20} more not shown</div>
      {/if}
    {:else}
      <EmptyState
        icon={'\u{1F4E6}'}
        heading="No dispatched tasks"
        description="Tasks dispatched through the HUD will appear here for tracking."
        compact
      />
    {/if}
  {/if}
</section>

<style>
  .history-metrics {
    display: flex;
    gap: var(--space-2);
    margin-bottom: var(--space-2);
  }

  .filter-row {
    margin-bottom: var(--space-2);
  }

  .filter-select {
    font-size: var(--text-xs);
    padding: 2px 8px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border);
    background: var(--bg-secondary);
    color: var(--fg-secondary);
    cursor: pointer;
  }

  .table-wrap {
    overflow-x: auto;
  }

  .history-table {
    width: 100%;
    border-collapse: collapse;
    font-size: var(--text-sm);
  }

  .history-table th {
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

  .history-table td {
    padding: var(--space-1) var(--space-2);
    border-bottom: 1px solid var(--border);
    color: var(--fg-secondary);
    vertical-align: middle;
  }

  .history-table tr:hover {
    background: var(--bg-tertiary);
  }

  .cell-task {
    font-weight: 500;
    color: var(--fg-primary);
    max-width: 250px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .cell-mono {
    font-family: var(--font-mono);
    font-size: var(--text-xs);
  }

  .cell-time {
    color: var(--fg-muted);
    white-space: nowrap;
  }

  .priority-pill,
  .status-pill {
    font-size: var(--text-xs);
    font-family: var(--font-mono);
    font-weight: 500;
  }

  .more-hint {
    font-size: var(--text-xs);
    color: var(--fg-muted);
    text-align: center;
    padding: var(--space-2);
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
