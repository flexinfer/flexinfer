<script lang="ts">
  /**
   * TasksTableView — flat DataTable view with inline priority/status
   * editing, blocked-by chips, resolve button, and bulk select.
   *
   * @type {{
   *   rows: any[],
   *   sortKey: string,
   *   sortDir: 'asc' | 'desc',
   *   hasActiveFilters: boolean,
   *   selectedIds: Set<string>,
   *   onSort: (key: string, dir: 'asc' | 'desc') => void,
   *   onRowClick: (task: any) => void,
   *   onSelect: (ids: Set<string>) => void,
   *   onCyclePriority: (task: any) => void,
   *   onChangeStatus: (task: any, newStatus: string) => void,
   *   onResolve: (task: any) => void,
   *   onClearFilters: () => void,
   * }}
   */
  let {
    rows,
    sortKey,
    sortDir,
    hasActiveFilters,
    selectedIds,
    onSort,
    onRowClick,
    onSelect,
    onCyclePriority,
    onChangeStatus,
    onResolve,
    onClearFilters,
  } = $props();

  import { taskStore } from '../../stores/tasks.svelte.ts';
  import { relativeTime, priorityVariant } from '../../utils/format.ts';
  import { VIRTUAL_SCROLL_THRESHOLD } from '../../utils/tokens.ts';
  import Badge from '../../widgets/Badge.svelte';
  import DataTable from '../shared/DataTable.svelte';
  import EmptyState from '../shared/EmptyState.svelte';
  import { parseIssueRefs, taskAgentName, STATUS_OPTIONS } from '../../utils/tasksHelpers';

  const columns = [
    { key: 'title', label: 'Title', sortable: true },
    { key: 'agent', label: 'Agent' },
    { key: 'priority', label: 'Priority', sortable: true, width: '90px' },
    { key: 'status', label: 'Status', sortable: true, width: '110px' },
    { key: 'blocked_by', label: 'Blocked By', width: '100px' },
    { key: 'created_at', label: 'Created', sortable: true, width: '90px' },
    { key: 'actions', label: 'Actions', width: '60px' },
  ];
</script>

{#if rows.length === 0 && taskStore.lastUpdated}
  <EmptyState
    icon={'☑'}
    heading="No tasks match filters"
    description="Try adjusting your search or filter criteria."
    compact
  >
    {#snippet action()}
      {#if hasActiveFilters}
        <button class="btn btn-ghost" onclick={onClearFilters}>Clear filters</button>
      {/if}
    {/snippet}
  </EmptyState>
{:else}
  <DataTable
    {columns}
    {rows}
    {sortKey}
    {sortDir}
    stableLayout={true}
    loading={!taskStore.lastUpdated}
    skeletonRows={3}
    maxRows={VIRTUAL_SCROLL_THRESHOLD}
    selectable={true}
    {selectedIds}
    {onSelect}
    {onSort}
    onRowClick={onRowClick}
  >
    {#snippet row({ row: task })}
      <td class="task-title" title={task.context || task.title}>
        {@html parseIssueRefs(task.title)}
        {#if task.context}
          <span class="context-hint" title={task.context}>📋</span>
        {/if}
      </td>
      <td class="text-mono text-muted">{taskAgentName(task)}</td>
      <td>
        <button class="priority-btn" onclick={() => onCyclePriority(task)} title="Click to cycle priority">
          <Badge text={task.priority ?? 'medium'} variant={priorityVariant(task.priority)} />
        </button>
      </td>
      <td>
        <select
          class="status-select"
          value={task.status ?? 'pending'}
          onchange={(e) => onChangeStatus(task, (e.target as HTMLSelectElement).value)}
          onclick={(e) => e.stopPropagation()}
        >
          {#each STATUS_OPTIONS as s}
            <option value={s}>{s.replaceAll('_', ' ')}</option>
          {/each}
        </select>
      </td>
      <td class="text-mono">
        <div class="blocked-col">
          {#if task.blocked_by?.length}
            {#each task.blocked_by as dep}
              <span class="blocked-id" class:resolved={task.resolved_deps?.includes(dep)}>
                {dep.slice(0, 8)}
              </span>
            {/each}
          {:else}
            <span class="text-muted">---</span>
          {/if}
        </div>
      </td>
      <td class="text-mono text-muted">{relativeTime(task.created_at)}</td>
      <td class="actions-col">
        {#if task.status !== 'completed' && task.status !== 'cancelled'}
          <button class="btn-resolve" onclick={(e) => { e.stopPropagation(); onResolve(task); }} title="Resolve task">✓</button>
        {/if}
      </td>
    {/snippet}
  </DataTable>
{/if}

<style>
  .task-title {
    color: var(--fg-primary);
    font-weight: 500;
    max-width: 300px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .task-title :global(.issue-link) {
    color: var(--info);
    text-decoration: none;
    font-weight: 600;
    transition: text-shadow var(--transition-fast);
  }
  .task-title :global(.issue-link:hover) {
    text-decoration: underline;
    text-shadow: 0 0 6px var(--glow-info);
  }
  .blocked-col { display: flex; gap: 4px; flex-wrap: wrap; }
  .blocked-id {
    font-size: var(--text-xs);
    padding: 1px 4px;
    background: var(--bg-tertiary);
    border-radius: var(--radius-sm);
    color: var(--fg-secondary);
    border: 1px solid var(--border-subtle);
  }
  .blocked-id.resolved { opacity: 0.4; text-decoration: line-through; }
  .status-select {
    font-size: var(--text-xs);
    padding: 2px 4px;
    border-radius: var(--radius-sm);
    background: var(--bg-tertiary);
    border: 1px solid var(--border);
    color: var(--fg-secondary);
    cursor: pointer;
    text-transform: capitalize;
    transition: border-color var(--transition-fast);
  }
  .status-select:focus {
    border-color: var(--info);
    outline: none;
    box-shadow: 0 0 4px var(--glow-info);
  }
  .priority-btn {
    background: none;
    border: none;
    padding: 0;
    cursor: pointer;
    transition: transform 0.1s ease;
  }
  .priority-btn:hover { transform: scale(1.1); }
  .priority-btn:active { transform: scale(0.95); }
  .actions-col { white-space: nowrap; }
  .btn-resolve {
    background: var(--success-dim);
    border: 1px solid rgba(34, 224, 118, 0.2);
    color: var(--success);
    border-radius: var(--radius-sm);
    padding: 2px var(--space-2);
    font-size: var(--text-sm);
    cursor: pointer;
    transition: background var(--transition-fast), box-shadow var(--transition-fast);
  }
  .btn-resolve:hover {
    background: rgba(34, 224, 118, 0.18);
    box-shadow: 0 0 8px var(--glow-success);
  }
  .context-hint {
    font-size: var(--text-xs);
    margin-left: 4px;
    cursor: help;
  }
</style>
