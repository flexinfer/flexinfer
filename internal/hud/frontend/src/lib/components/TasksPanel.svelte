<script lang="ts">
  /**
   * TasksPanel — composition shell for the Activity → Tasks view. The
   * heavy zones live in `lib/components/tasks/*` and pure helpers in
   * `lib/utils/tasksHelpers.ts` per the panel decomposition pattern
   * (`docs/HUD_PANEL_DECOMP.md`).
   */
  import { taskStore } from '../stores/tasks.svelte.ts';
  import { agentStore } from '../stores/agents.svelte.ts';
  import { coordinationStore } from '../stores/coordination.svelte.ts';
  import { toastStore } from '../stores/toasts.svelte.ts';
  import Badge from '../widgets/Badge.svelte';
  import FilterBar from './shared/FilterBar.svelte';
  import BulkToolbar from './shared/BulkToolbar.svelte';
  import TasksRadar from './tasks/TasksRadar.svelte';
  import TasksTableView from './tasks/TasksTableView.svelte';
  import TasksGroupedView from './tasks/TasksGroupedView.svelte';
  import CreateTaskModal from './tasks/CreateTaskModal.svelte';
  import ResolveTaskModal from './tasks/ResolveTaskModal.svelte';
  import TaskDetail from './tasks/TaskDetail.svelte';
  import {
    filterTasks,
    sortTasks,
    groupTasksByStatus,
    agentOptionsFrom,
    PRIORITY_CYCLE,
  } from '../utils/tasksHelpers';

  $effect(() => {
    taskStore.startPolling(5000);
    agentStore.startPolling(30000);
    coordinationStore.startPolling(30000);
    return () => {
      taskStore.stopPolling();
      agentStore.stopPolling();
      coordinationStore.stopPolling();
    };
  });

  let tasks = $derived(taskStore.tasks ?? []);

  // Panel-wide filter/sort/view state. Per the B1 contract this is the
  // candidate set for the store contract, but tasks already has parallel
  // store filter state owned by OverlayShell; keeping the panel state
  // local avoids breaking that consumer.
  let searchQuery = $state('');
  let priorityFilter = $state('');
  let agentFilter = $state('');
  let statusFilter = $state('');
  let viewMode = $state<'flat' | 'grouped'>('flat');
  let collapsedGroups = $state<Set<string>>(new Set());
  let sortKey = $state('created_at');
  let sortDir = $state<'asc' | 'desc'>('desc');

  let showCreateModal = $state(false);
  let showResolveModal = $state(false);
  let resolveTaskId = $state('');
  let resolveTaskTitle = $state('');
  let selectedTask = $state<any>(null);
  let selectedTaskIds = $state<Set<string>>(new Set());

  let pendingCt = $derived(tasks.filter((t) => t.status === 'pending').length);
  let inProgressCt = $derived(tasks.filter((t) => t.status === 'in_progress').length);
  let blockedCt = $derived(tasks.filter((t) => t.status === 'blocked').length);
  let completedCt = $derived(tasks.filter((t) => t.status === 'completed').length);

  let agentOptions = $derived(agentOptionsFrom(tasks));

  let filterDefs = $derived([
    {
      key: 'priority',
      label: 'All Priority',
      value: priorityFilter,
      options: [
        { value: 'critical', label: 'Critical' },
        { value: 'high', label: 'High' },
        { value: 'medium', label: 'Medium' },
        { value: 'low', label: 'Low' },
      ],
    },
    { key: 'agent', label: 'All Agents', value: agentFilter, options: agentOptions },
    {
      key: 'status',
      label: 'All Status',
      value: statusFilter,
      options: [
        { value: 'pending', label: 'Pending' },
        { value: 'in_progress', label: 'In Progress' },
        { value: 'blocked', label: 'Blocked' },
        { value: 'completed', label: 'Completed' },
      ],
    },
  ]);

  let hasActiveFilters = $derived(
    searchQuery.trim() !== '' || priorityFilter !== '' || agentFilter !== '' || statusFilter !== ''
  );
  let filtered = $derived(filterTasks(tasks, searchQuery, priorityFilter, agentFilter, statusFilter));
  let sorted = $derived(sortTasks(filtered, sortKey, sortDir));
  let grouped = $derived(groupTasksByStatus(filtered));

  // Clear selection when filters change.
  $effect(() => {
    searchQuery; priorityFilter; agentFilter; statusFilter;
    selectedTaskIds = new Set();
  });

  function handleFilter(key: string, val: string) {
    if (key === 'priority') priorityFilter = val;
    else if (key === 'agent') agentFilter = val;
    else if (key === 'status') statusFilter = val;
  }
  function clearFilters() { searchQuery = ''; priorityFilter = ''; agentFilter = ''; statusFilter = ''; }
  function toggleGroup(status: string) {
    const next = new Set(collapsedGroups);
    next.has(status) ? next.delete(status) : next.add(status);
    collapsedGroups = next;
  }

  function cyclePriority(task: any) {
    const idx = PRIORITY_CYCLE.indexOf(task.priority ?? 'medium');
    const next = PRIORITY_CYCLE[(idx + 1) % PRIORITY_CYCLE.length];
    taskStore.setPriority(task.id, next);
    toastStore.info(`Priority → ${next}`);
  }
  async function changeStatus(task: any, newStatus: string) {
    await taskStore.updateStatus(task.id, newStatus);
    toastStore.info(`Status → ${newStatus.replaceAll('_', ' ')}`);
  }
  function openResolve(task: any) {
    resolveTaskId = task.id;
    resolveTaskTitle = task.title;
    showResolveModal = true;
  }
  function selectTask(task: any) {
    selectedTask = selectedTask?.id === task.id ? null : task;
  }

  async function bulkComplete() {
    for (const id of selectedTaskIds) await taskStore.updateStatus(id, 'completed');
    toastStore.success(`${selectedTaskIds.size} tasks completed`);
    selectedTaskIds = new Set();
  }
  async function bulkCancel() {
    for (const id of selectedTaskIds) await taskStore.updateStatus(id, 'cancelled');
    toastStore.success(`${selectedTaskIds.size} tasks cancelled`);
    selectedTaskIds = new Set();
  }
  async function bulkHighPriority() {
    for (const id of selectedTaskIds) await taskStore.setPriority(id, 'high');
    toastStore.success(`${selectedTaskIds.size} tasks set to high priority`);
    selectedTaskIds = new Set();
  }
  let bulkActions = $derived([
    { label: 'Complete', variant: 'success', onclick: bulkComplete },
    { label: 'Cancel', variant: 'danger', onclick: bulkCancel },
    { label: 'High Priority', variant: 'warning', onclick: bulkHighPriority },
  ]);
</script>

<div class="panel tasks-panel">
  <div class="header-bar">
    <div class="header-stats">
      <span class="header-total text-mono">{tasks.length} tasks</span>
      <Badge text="{pendingCt} pending" variant="warning" />
      <Badge text="{inProgressCt} in-progress" variant="info" />
      <Badge text="{blockedCt} blocked" variant="error" />
      <Badge text="{completedCt} completed" variant="success" />
    </div>
    <div class="header-actions">
      <button class="btn btn-success" onclick={() => showCreateModal = true}>+ New Task</button>
      <div class="view-toggle">
        <button class="btn btn-ghost" class:active-toggle={viewMode === 'flat'} onclick={() => viewMode = 'flat'}>Flat</button>
        <button class="btn btn-ghost" class:active-toggle={viewMode === 'grouped'} onclick={() => viewMode = 'grouped'}>By Status</button>
      </div>
    </div>
  </div>

  <FilterBar
    search={searchQuery}
    placeholder="Search tasks..."
    filters={filterDefs}
    resultCount={filtered.length}
    onSearch={(val) => searchQuery = val}
    onFilter={handleFilter}
  />

  <div class="tasks-layout">
    <div class="task-main">
      <div class="task-content">
        {#if viewMode === 'flat'}
          <TasksTableView
            rows={sorted}
            {sortKey}
            {sortDir}
            {hasActiveFilters}
            selectedIds={selectedTaskIds}
            onSort={(key, dir) => { sortKey = key; sortDir = dir; }}
            onRowClick={selectTask}
            onSelect={(ids) => selectedTaskIds = ids}
            onCyclePriority={cyclePriority}
            onChangeStatus={changeStatus}
            onResolve={openResolve}
            onClearFilters={clearFilters}
          />
          <BulkToolbar
            count={selectedTaskIds.size}
            actions={bulkActions}
            onClearSelection={() => { selectedTaskIds = new Set(); }}
          />
        {:else}
          <TasksGroupedView
            groups={grouped}
            {collapsedGroups}
            {hasActiveFilters}
            onToggleGroup={toggleGroup}
            onClearFilters={clearFilters}
            onCyclePriority={cyclePriority}
            onChangeStatus={changeStatus}
            onResolve={openResolve}
          />
        {/if}
      </div>
    </div>
    <TasksRadar />
  </div>
</div>

<CreateTaskModal open={showCreateModal} onClose={() => showCreateModal = false} />
<ResolveTaskModal open={showResolveModal} taskId={resolveTaskId} taskTitle={resolveTaskTitle} onClose={() => showResolveModal = false} />
<TaskDetail task={selectedTask} onClose={() => selectedTask = null} onResolve={openResolve} />

<style>
  .tasks-panel { display: flex; flex-direction: column; overflow: hidden; }
  .header-bar {
    display: flex; align-items: center; justify-content: space-between;
    padding: var(--space-2) 0; border-bottom: 1px solid var(--border);
    flex-wrap: wrap; gap: var(--space-2); position: relative;
  }
  .header-bar::after {
    content: ''; position: absolute; bottom: 0; left: 10%; right: 10%; height: 1px;
    background: linear-gradient(90deg, transparent, rgba(0, 200, 255, 0.06) 50%, transparent);
    pointer-events: none;
  }
  .header-stats { display: flex; align-items: center; gap: var(--space-3); font-size: var(--text-sm); }
  .header-total { font-weight: 600; color: var(--fg-primary); }
  .header-actions { display: flex; align-items: center; gap: var(--space-2); }
  .view-toggle { display: flex; gap: 2px; background: var(--bg-tertiary); border-radius: var(--radius-sm); padding: 2px; }
  .active-toggle {
    background: var(--bg-elevated) !important; color: var(--fg-primary) !important;
    box-shadow: 0 0 4px rgba(0, 200, 255, 0.1);
  }
  .task-content {
    flex: 1; min-height: 0; overflow-y: auto;
    background: var(--bg-secondary); border: 1px solid var(--border);
    border-radius: var(--radius-md); position: relative;
  }
  .task-content::before {
    content: ''; position: absolute; inset: 0; border-radius: inherit;
    background: var(--surface-highlight); pointer-events: none; z-index: 1;
  }
  .tasks-layout {
    flex: 1; min-height: 0; display: grid;
    grid-template-columns: minmax(0, 1fr) 300px;
    gap: var(--space-3); margin-top: var(--space-2);
  }
  .task-main { min-height: 0; display: flex; flex-direction: column; }
  @media (max-width: 1200px) { .tasks-layout { grid-template-columns: 1fr; } }
  @media (max-width: 768px) { .header-bar { flex-direction: column; align-items: flex-start; } }
</style>
