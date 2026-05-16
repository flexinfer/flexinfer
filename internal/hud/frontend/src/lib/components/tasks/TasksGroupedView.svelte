<script lang="ts">
  /**
   * TasksGroupedView — grouped tables, one per status group, with
   * collapsible headers.
   *
   * @type {{
   *   groups: Array<[string, any[]]>,
   *   collapsedGroups: Set<string>,
   *   hasActiveFilters: boolean,
   *   onToggleGroup: (status: string) => void,
   *   onClearFilters: () => void,
   *   onCyclePriority: (task: any) => void,
   *   onChangeStatus: (task: any, newStatus: string) => void,
   *   onResolve: (task: any) => void,
   * }}
   */
  let { groups, collapsedGroups, hasActiveFilters, onToggleGroup, onClearFilters, onCyclePriority, onChangeStatus, onResolve } = $props();

  import { relativeTime, priorityVariant } from '../../utils/format.ts';
  import Badge from '../../widgets/Badge.svelte';
  import EmptyState from '../shared/EmptyState.svelte';
  import { parseIssueRefs, taskAgentName, STATUS_OPTIONS } from '../../utils/tasksHelpers';
</script>

<div class="grouped-view">
  {#each groups as [status, items] (status)}
    <div class="group-section">
      <button class="group-header" onclick={() => onToggleGroup(status)}>
        <span class="group-chevron">{collapsedGroups.has(status) ? '▶' : '▼'}</span>
        <span class="group-status-label">{status.replaceAll('_', ' ')}</span>
        <span class="count-badge">{items.length}</span>
      </button>
      {#if !collapsedGroups.has(status)}
        <div class="group-body">
          <div class="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Title</th>
                  <th>Agent</th>
                  <th>Priority</th>
                  <th>Status</th>
                  <th>Blocked By</th>
                  <th>Created</th>
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                {#each items as task (task.id)}
                  <tr>
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
                        <button class="btn-resolve" onclick={() => onResolve(task)} title="Resolve task">✓</button>
                      {/if}
                    </td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        </div>
      {/if}
    </div>
  {:else}
    <EmptyState icon={'☑'} heading="No tasks match filters" compact>
      {#snippet action()}
        {#if hasActiveFilters}
          <button class="btn btn-ghost" onclick={onClearFilters}>Clear filters</button>
        {/if}
      {/snippet}
    </EmptyState>
  {/each}
</div>

<style>
  .grouped-view { display: flex; flex-direction: column; }
  .group-section { border-bottom: 1px solid var(--border); }
  .group-section:last-child { border-bottom: none; }
  .group-header {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    width: 100%;
    padding: var(--space-3);
    text-align: left;
    font-size: var(--text-sm);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-secondary);
    background: var(--bg-tertiary);
    border: none;
    cursor: pointer;
    transition: background var(--transition-fast), color var(--transition-fast);
  }
  .group-header:hover { background: var(--bg-elevated); color: var(--fg-primary); }
  .group-chevron {
    font-size: 10px;
    width: 14px;
    flex-shrink: 0;
    transition: transform var(--transition-fast);
  }
  .group-status-label { text-transform: capitalize; }
  .count-badge {
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    font-weight: 600;
    background: var(--info-dim);
    color: var(--info);
    padding: 2px 8px;
    border-radius: var(--radius-full);
    border: 1px solid rgba(0, 200, 255, 0.15);
  }
  .group-body { padding: 0; }
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
  }
  .priority-btn {
    background: none;
    border: none;
    padding: 0;
    cursor: pointer;
  }
  .actions-col { white-space: nowrap; }
  .btn-resolve {
    background: var(--success-dim);
    border: 1px solid rgba(34, 224, 118, 0.2);
    color: var(--success);
    border-radius: var(--radius-sm);
    padding: 2px var(--space-2);
    font-size: var(--text-sm);
    cursor: pointer;
  }
  .btn-resolve:hover { background: rgba(34, 224, 118, 0.18); box-shadow: 0 0 8px var(--glow-success); }
  .context-hint { font-size: var(--text-xs); margin-left: 4px; cursor: help; }
</style>
