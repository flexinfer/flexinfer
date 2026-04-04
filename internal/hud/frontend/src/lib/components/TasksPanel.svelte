<script>
  import { taskStore } from '../stores/tasks.svelte.ts';
  import { agentStore } from '../stores/agents.svelte.ts';
  import { coordinationStore } from '../stores/coordination.svelte.ts';
  import { toastStore } from '../stores/toasts.svelte.ts';
  import { relativeTime, statusVariant, priorityVariant } from '../utils/format.ts';
  import { VIRTUAL_SCROLL_THRESHOLD } from '../utils/tokens.ts';
  import Badge from '../widgets/Badge.svelte';
  import Modal from '../widgets/Modal.svelte';
  import FilterBar from './shared/FilterBar.svelte';
  import DataTable from './shared/DataTable.svelte';
  import BulkToolbar from './shared/BulkToolbar.svelte';
  import DetailDrawer from './shared/DetailDrawer.svelte';
  import EmptyState from './shared/EmptyState.svelte';

  // --- GitLab issue-reference linking ---
  const GITLAB_BASE = 'https://gitlab.flexinfer.ai';
  const SERVICE_PROJECTS = new Set([
    'loom-core', 'loom', 'loom-zed', 'flexdeck', 'flexinfer', 'flexinfer-site',
    'homelab-homepage', 'jobsearch-app', 'storyboard-generator',
    'streamslate', 'streamslate-site', 'substack', 'tech-radar',
    'mcp-gateway', 'mcp-orchestra', 'mcp-sandbox', 'mentatlab',
    'news-analyzer', 'diff-surgeon', 'comfyui-images-proxy',
  ]);

  function escapeHtml(text) {
    return text
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }

  function gitlabIssueUrl(project, num) {
    const group = SERVICE_PROJECTS.has(project) ? 'services' : 'libs';
    return `${GITLAB_BASE}/${group}/${project}/-/issues/${num}`;
  }

  function parseIssueRefs(title) {
    if (!title) return '---';
    const escaped = escapeHtml(title);
    return escaped.replace(
      /\[([a-zA-Z0-9_-]+)#(\d+)\]/g,
      (_, proj, num) =>
        `<a href="${gitlabIssueUrl(proj, num)}" class="issue-link" target="_blank" rel="noopener" onclick="event.stopPropagation()">${proj}#${num}</a>`
    );
  }

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
  let availableAgents = $derived(agentStore.agents ?? []);
  let coordinationSummary = $derived(coordinationStore.summary);
  let coordinationBlockers = $derived(coordinationStore.blockers ?? []);
  let riskyNamespaces = $derived(coordinationStore.riskyNamespaces);

  let searchQuery = $state('');
  let priorityFilter = $state('');
  let agentFilter = $state('');
  let statusFilter = $state('');
  let viewMode = $state('flat'); // 'flat' | 'grouped'
  let collapsedGroups = $state(new Set());

  // Sort state for DataTable
  let sortKey = $state('created_at');
  let sortDir = $state('desc');

  // Create task modal
  let showCreateModal = $state(false);
  let newTitle = $state('');
  let newPriority = $state('medium');
  let newSessionId = $state('');
  let newTags = $state('');
  let newContext = $state('');
  let newFilePath = $state('');
  let newBlockedBy = $state([]);
  let selectedAgentId = $state('');
  let creating = $state(false);
  let showOptional = $state(false);

  // Resolve modal
  let showResolveModal = $state(false);
  let resolveTaskId = $state('');
  let resolveTaskTitle = $state('');
  let resolutionText = $state('');
  let resolving = $state(false);

  // Counts
  let pendingCt = $derived(tasks.filter(t => t.status === 'pending').length);
  let inProgressCt = $derived(tasks.filter(t => t.status === 'in_progress').length);
  let blockedCt = $derived(tasks.filter(t => t.status === 'blocked').length);
  let completedCt = $derived(tasks.filter(t => t.status === 'completed').length);

  // Unique agents for filter dropdown
  let agentOptions = $derived.by(() => {
    const set = new Set();
    tasks.forEach((t) => {
      const agentName = t.agent_id ?? t.agent;
      if (agentName) set.add(agentName);
    });
    return Array.from(set).sort().map(a => ({ value: a, label: a }));
  });

  // FilterBar filter definitions
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
    {
      key: 'agent',
      label: 'All Agents',
      value: agentFilter,
      options: agentOptions,
    },
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

  function handleSearch(val) {
    searchQuery = val;
  }

  function handleFilter(key, val) {
    if (key === 'priority') priorityFilter = val;
    else if (key === 'agent') agentFilter = val;
    else if (key === 'status') statusFilter = val;
  }

  function clearFilters() {
    searchQuery = '';
    priorityFilter = '';
    agentFilter = '';
    statusFilter = '';
  }

  let hasActiveFilters = $derived(
    searchQuery.trim() !== '' || priorityFilter !== '' || agentFilter !== '' || statusFilter !== ''
  );

  // Filtered tasks
  let filtered = $derived.by(() => {
    let result = tasks;

    if (searchQuery.trim()) {
      const q = searchQuery.toLowerCase().trim();
      result = result.filter(t =>
        (t.title ?? '').toLowerCase().includes(q) ||
        (t.description ?? '').toLowerCase().includes(q)
      );
    }

    if (priorityFilter) {
      result = result.filter(t => t.priority === priorityFilter);
    }

    if (agentFilter) {
      result = result.filter((t) => (t.agent_id ?? t.agent) === agentFilter);
    }

    if (statusFilter) {
      result = result.filter(t => t.status === statusFilter);
    }

    return result;
  });

  // Sorted tasks for DataTable (flat view)
  const PRIORITY_ORDER = { critical: 0, high: 1, medium: 2, low: 3 };
  const STATUS_ORDER = { in_progress: 0, blocked: 1, pending: 2, completed: 3, cancelled: 4 };

  let sorted = $derived.by(() => {
    const rows = [...filtered];
    rows.sort((a, b) => {
      let cmp = 0;
      switch (sortKey) {
        case 'title':
          cmp = (a.title ?? '').localeCompare(b.title ?? '');
          break;
        case 'priority':
          cmp = (PRIORITY_ORDER[a.priority] ?? 9) - (PRIORITY_ORDER[b.priority] ?? 9);
          break;
        case 'status':
          cmp = (STATUS_ORDER[a.status] ?? 9) - (STATUS_ORDER[b.status] ?? 9);
          break;
        case 'created_at':
          cmp = new Date(a.created_at).getTime() - new Date(b.created_at).getTime();
          break;
        default:
          break;
      }
      return sortDir === 'desc' ? -cmp : cmp;
    });
    return rows;
  });

  function handleSort(key, dir) {
    sortKey = key;
    sortDir = dir;
  }

  // DataTable column definitions
  const columns = [
    { key: 'title', label: 'Title', sortable: true },
    { key: 'agent', label: 'Agent' },
    { key: 'priority', label: 'Priority', sortable: true, width: '90px' },
    { key: 'status', label: 'Status', sortable: true, width: '110px' },
    { key: 'blocked_by', label: 'Blocked By', width: '100px' },
    { key: 'created_at', label: 'Created', sortable: true, width: '90px' },
    { key: 'actions', label: 'Actions', width: '60px' },
  ];

  // Grouped tasks
  let grouped = $derived.by(() => {
    const groups = {};
    const order = ['in_progress', 'blocked', 'pending', 'completed', 'cancelled'];
    order.forEach(s => { groups[s] = []; });

    filtered.forEach(t => {
      const key = t.status ?? 'pending';
      if (!groups[key]) groups[key] = [];
      groups[key].push(t);
    });

    return Object.entries(groups).filter(([, items]) => items.length > 0);
  });

  function toggleGroup(status) {
    const next = new Set(collapsedGroups);
    if (next.has(status)) {
      next.delete(status);
    } else {
      next.add(status);
    }
    collapsedGroups = next;
  }

  const PRIORITY_CYCLE = ['low', 'medium', 'high', 'critical'];
  const STATUS_OPTIONS = ['pending', 'in_progress', 'blocked', 'completed', 'cancelled'];

  function cyclePriority(task) {
    const idx = PRIORITY_CYCLE.indexOf(task.priority ?? 'medium');
    const next = PRIORITY_CYCLE[(idx + 1) % PRIORITY_CYCLE.length];
    taskStore.setPriority(task.id, next);
    toastStore.info(`Priority → ${next}`);
  }

  async function changeStatus(task, newStatus) {
    await taskStore.updateStatus(task.id, newStatus);
    toastStore.info(`Status → ${newStatus.replaceAll('_', ' ')}`);
  }

  function resetCreateForm() {
    newTitle = '';
    newPriority = 'medium';
    newSessionId = '';
    newTags = '';
    newContext = '';
    newFilePath = '';
    newBlockedBy = [];
    selectedAgentId = '';
    creating = false;
    showOptional = false;
  }

  function openCreateModal() {
    resetCreateForm();
    showCreateModal = true;
  }

  function closeCreateModal() {
    showCreateModal = false;
    resetCreateForm();
  }

  function onAgentSelect(agentId) {
    selectedAgentId = agentId;
    if (agentId) {
      const agent = availableAgents.find(a => a.agent_id === agentId);
      if (agent?.session_id) {
        newSessionId = agent.session_id;
      }
    } else {
      newSessionId = '';
    }
  }

  function agentStatusIcon(status) {
    if (status === 'active') return '🟢';
    if (status === 'idle') return '🟡';
    return '⚪';
  }

  function taskAgentName(task) {
    return task.agent_id ?? task.agent ?? '---';
  }

  function addBlockedBy(taskId) {
    if (taskId && !newBlockedBy.includes(taskId)) {
      newBlockedBy = [...newBlockedBy, taskId];
    }
  }

  function removeBlockedBy(taskId) {
    newBlockedBy = newBlockedBy.filter(id => id !== taskId);
  }

  // Pending/in-progress tasks for blocked-by picker
  let blockableTasks = $derived(
    tasks.filter(t => t.status === 'pending' || t.status === 'in_progress')
  );

  async function submitCreateTask() {
    if (!newTitle.trim()) return;
    creating = true;
    const tags = newTags.trim() ? newTags.split(',').map(t => t.trim()).filter(Boolean) : undefined;
    const ok = await taskStore.createTask({
      title: newTitle.trim(),
      priority: newPriority,
      sessionId: newSessionId.trim() || undefined,
      tags,
      context: newContext.trim() || undefined,
      filePath: newFilePath.trim() || undefined,
      blockedBy: newBlockedBy.length ? newBlockedBy : undefined,
    });
    if (ok) {
      toastStore.success('Task created');
      closeCreateModal();
    } else {
      toastStore.error(taskStore.error ?? 'Failed to create task');
      creating = false;
    }
  }

  function openResolve(task) {
    resolveTaskId = task.id;
    resolveTaskTitle = task.title;
    resolutionText = '';
    resolving = false;
    showResolveModal = true;
  }

  function closeResolve() {
    showResolveModal = false;
    resolveTaskId = '';
    resolveTaskTitle = '';
    resolutionText = '';
    resolving = false;
  }

  async function submitResolve() {
    if (!resolveTaskId) return;
    resolving = true;
    await taskStore.resolve(resolveTaskId, resolutionText.trim());
    toastStore.success('Task resolved');
    closeResolve();
  }

  // Bulk selection
  let selectedTaskIds = $state(new Set());

  function handleTaskSelect(ids) {
    selectedTaskIds = ids;
  }

  // Clear selection on filter change
  $effect(() => {
    // Track filter values to clear selection
    searchQuery; priorityFilter; agentFilter; statusFilter;
    selectedTaskIds = new Set();
  });

  async function bulkComplete() {
    for (const id of selectedTaskIds) {
      await taskStore.updateStatus(id, 'completed');
    }
    toastStore.success(`${selectedTaskIds.size} tasks completed`);
    selectedTaskIds = new Set();
  }

  async function bulkCancel() {
    for (const id of selectedTaskIds) {
      await taskStore.updateStatus(id, 'cancelled');
    }
    toastStore.success(`${selectedTaskIds.size} tasks cancelled`);
    selectedTaskIds = new Set();
  }

  async function bulkHighPriority() {
    for (const id of selectedTaskIds) {
      await taskStore.setPriority(id, 'high');
    }
    toastStore.success(`${selectedTaskIds.size} tasks set to high priority`);
    selectedTaskIds = new Set();
  }

  let bulkActions = $derived([
    { label: 'Complete', variant: 'success', onclick: bulkComplete },
    { label: 'Cancel', variant: 'danger', onclick: bulkCancel },
    { label: 'High Priority', variant: 'warning', onclick: bulkHighPriority },
  ]);

  // Detail drawer
  let selectedTask = $state(null);
  let selectedTaskRelations = $derived.by(() => {
    if (!selectedTask?.id) return [];
    return coordinationBlockers.filter((blocker) => blocker.task_id === selectedTask.id);
  });
  let activeBlockers = $derived(coordinationStore.activeBlockers);
  let attentionAgents = $derived(coordinationStore.topAttentionAgents);
  let unassignedCount = $derived(tasks.filter((task) => !task.agent_id && !task.agent).length);
  let staleBlockedCount = $derived(
    tasks.filter((task) => task.status === 'blocked' && task.blocked_by?.length).length
  );

  function selectTask(task) {
    selectedTask = selectedTask?.id === task.id ? null : task;
  }

  function closeTaskDetail() {
    selectedTask = null;
  }

</script>

<div class="panel tasks-panel">
  <!-- Header bar -->
  <div class="header-bar">
    <div class="header-stats">
      <span class="header-total text-mono">{tasks.length} tasks</span>
      <Badge text="{pendingCt} pending" variant="warning" />
      <Badge text="{inProgressCt} in-progress" variant="info" />
      <Badge text="{blockedCt} blocked" variant="error" />
      <Badge text="{completedCt} completed" variant="success" />
    </div>
    <div class="header-actions">
      <button class="btn btn-success" onclick={openCreateModal}>+ New Task</button>
      <div class="view-toggle">
        <button
          class="btn btn-ghost"
          class:active-toggle={viewMode === 'flat'}
          onclick={() => viewMode = 'flat'}
        >Flat</button>
        <button
          class="btn btn-ghost"
          class:active-toggle={viewMode === 'grouped'}
          onclick={() => viewMode = 'grouped'}
        >By Status</button>
      </div>
    </div>
  </div>

  <!-- Filter row (shared component) -->
  <FilterBar
    search={searchQuery}
    placeholder="Search tasks..."
    filters={filterDefs}
    resultCount={filtered.length}
    onSearch={handleSearch}
    onFilter={handleFilter}
  />

  <div class="tasks-layout">
    <div class="task-main">
      <div class="task-content">
        {#if viewMode === 'flat'}
          {#if filtered.length === 0 && taskStore.lastUpdated}
            <EmptyState
              icon={'\u2611'}
              heading="No tasks match filters"
              description="Try adjusting your search or filter criteria."
              compact
            >
              {#snippet action()}
                {#if hasActiveFilters}
                  <button class="btn btn-ghost" onclick={clearFilters}>Clear filters</button>
                {/if}
              {/snippet}
            </EmptyState>
          {:else}
            <DataTable
              {columns}
              rows={sorted}
              {sortKey}
              {sortDir}
              stableLayout={true}
              loading={!taskStore.lastUpdated}
              skeletonRows={3}
              maxRows={VIRTUAL_SCROLL_THRESHOLD}
              selectable={true}
              selectedIds={selectedTaskIds}
              onSelect={handleTaskSelect}
              onSort={handleSort}
              onRowClick={selectTask}
            >
              {#snippet row({ row: task })}
                <td class="task-title" title={task.context || task.title}>
                  {@html parseIssueRefs(task.title)}
                  {#if task.context}
                    <span class="context-hint" title={task.context}>{'\uD83D\uDCCB'}</span>
                  {/if}
                </td>
                <td class="text-mono text-muted">{taskAgentName(task)}</td>
                <td>
                  <button class="priority-btn" onclick={() => cyclePriority(task)} title="Click to cycle priority">
                    <Badge text={task.priority ?? 'medium'} variant={priorityVariant(task.priority)} />
                  </button>
                </td>
                <td>
                  <select
                    class="status-select"
                    value={task.status ?? 'pending'}
                    onchange={(e) => changeStatus(task, e.target.value)}
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
                    <button class="btn-resolve" onclick={(e) => { e.stopPropagation(); openResolve(task); }} title="Resolve task">{'\u2713'}</button>
                  {/if}
                </td>
              {/snippet}
            </DataTable>
            <BulkToolbar
              count={selectedTaskIds.size}
              actions={bulkActions}
              onClearSelection={() => { selectedTaskIds = new Set(); }}
            />
          {/if}
        {:else}
          <div class="grouped-view">
            {#each grouped as [status, items] (status)}
              <div class="group-section">
                <button class="group-header" onclick={() => toggleGroup(status)}>
                  <span class="group-chevron">{collapsedGroups.has(status) ? '\u25B6' : '\u25BC'}</span>
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
                                  <span class="context-hint" title={task.context}>{'\uD83D\uDCCB'}</span>
                                {/if}
                              </td>
                              <td class="text-mono text-muted">{taskAgentName(task)}</td>
                              <td>
                                <button class="priority-btn" onclick={() => cyclePriority(task)} title="Click to cycle priority">
                                  <Badge text={task.priority ?? 'medium'} variant={priorityVariant(task.priority)} />
                                </button>
                              </td>
                              <td>
                                <select
                                  class="status-select"
                                  value={task.status ?? 'pending'}
                                  onchange={(e) => changeStatus(task, e.target.value)}
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
                                  <button class="btn-resolve" onclick={() => openResolve(task)} title="Resolve task">{'\u2713'}</button>
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
              <EmptyState
                icon={'\u2611'}
                heading="No tasks match filters"
                compact
              >
                {#snippet action()}
                  {#if hasActiveFilters}
                    <button class="btn btn-ghost" onclick={clearFilters}>Clear filters</button>
                  {/if}
                {/snippet}
              </EmptyState>
            {/each}
          </div>
        {/if}
      </div>
    </div>

    <aside class="tasks-rail">
      <section class="radar-card">
        <div class="radar-label">Dependency Radar</div>
        <div class="radar-value">{coordinationSummary.cross_agent_blockers} cross-agent blockers</div>
        <div class="radar-meta">{staleBlockedCount} dependency-bound · {unassignedCount} unassigned</div>
      </section>

      <section class="radar-card">
        <div class="radar-label">Risky Namespaces</div>
        {#if riskyNamespaces.length > 0}
          <div class="radar-stack">
            {#each riskyNamespaces.slice(0, 4) as namespace}
              <div class="radar-list-item">
                <span class="radar-item-title text-mono" title={namespace.namespace}>{namespace.namespace}</span>
                <span class="radar-item-meta">{namespace.blocked_tasks} blocked · {namespace.cross_agent_blockers} x-agent</span>
              </div>
            {/each}
          </div>
        {:else}
          <div class="radar-meta">No risky namespaces</div>
        {/if}
      </section>

      <section class="radar-card">
        <div class="radar-label">Active Blockers</div>
        {#if activeBlockers.length > 0}
          <div class="radar-stack">
            {#each activeBlockers.slice(0, 5) as blocker}
              <div class="radar-list-item">
                <span class="radar-item-title" title={blocker.task_title}>{blocker.task_title}</span>
                <span class="radar-item-meta">
                  blocked by {blocker.blocked_by_task_title || blocker.blocked_by_task_id}
                  {#if blocker.cross_agent} · cross-agent{/if}
                </span>
              </div>
            {/each}
          </div>
        {:else}
          <div class="radar-meta">No active blockers</div>
        {/if}
      </section>

      <section class="radar-card">
        <div class="radar-label">Agent Pressure</div>
        {#if attentionAgents.length > 0}
          <div class="radar-stack">
            {#each attentionAgents.slice(0, 4) as agent}
              <div class="radar-list-item">
                <span class="radar-item-title text-mono">{agent.agent_id}</span>
                <span class="radar-item-meta">{agent.blocked_tasks} blocked · {agent.claim_count} claims</span>
              </div>
            {/each}
          </div>
        {:else}
          <div class="radar-meta">No agents need attention</div>
        {/if}
      </section>
    </aside>
  </div>
</div>

<!-- Create Task Modal -->
<Modal open={showCreateModal} title="New Task" onClose={closeCreateModal}>
  <form class="create-form" onsubmit={(e) => { e.preventDefault(); submitCreateTask(); }}>
    <div class="form-field">
      <label class="form-label" for="task-title">Title <span class="required">*</span></label>
      <input id="task-title" type="text" bind:value={newTitle} placeholder="What needs to be done?" required />
    </div>

    <div class="form-row">
      <div class="form-field form-field-half">
        <label class="form-label" for="task-agent">Assign to Agent</label>
        <select id="task-agent" value={selectedAgentId} onchange={(e) => onAgentSelect(e.target.value)}>
          <option value="">Unassigned</option>
          {#each availableAgents as agent}
            <option value={agent.agent_id}>
              {agentStatusIcon(agent.status)} {agent.agent_id} ({agent.agent_type})
            </option>
          {/each}
        </select>
      </div>
      <div class="form-field form-field-half">
        <label class="form-label" for="task-priority">Priority</label>
        <select id="task-priority" bind:value={newPriority}>
          <option value="low">Low</option>
          <option value="medium">Medium</option>
          <option value="high">High</option>
          <option value="critical">Critical</option>
        </select>
      </div>
    </div>

    <div class="form-field">
      <label class="form-label" for="task-context">Context / Description</label>
      <textarea
        id="task-context"
        bind:value={newContext}
        placeholder="Describe what needs to be done, include relevant details..."
        rows="3"
      ></textarea>
    </div>

    <button type="button" class="optional-toggle" onclick={() => showOptional = !showOptional}>
      {showOptional ? '\u25BC' : '\u25B6'} Optional fields
    </button>

    {#if showOptional}
      <div class="optional-section">
        <div class="form-field">
          <label class="form-label" for="task-filepath">File Path</label>
          <input id="task-filepath" type="text" bind:value={newFilePath} placeholder="services/api/auth.go" />
        </div>
        <div class="form-field">
          <label class="form-label" for="task-tags">Tags (comma-separated)</label>
          <input id="task-tags" type="text" bind:value={newTags} placeholder="auth, refactor, bug..." />
        </div>
        <div class="form-field">
          <label class="form-label" for="task-blocked-by">Blocked By</label>
          <div class="blocked-by-picker">
            <select id="task-blocked-by" onchange={(e) => { addBlockedBy(e.target.value); e.target.value = ''; }}>
              <option value="">Select a task...</option>
              {#each blockableTasks as t}
                {#if !newBlockedBy.includes(t.id)}
                  <option value={t.id}>{t.title} ({t.id.slice(0, 8)})</option>
                {/if}
              {/each}
            </select>
            {#if newBlockedBy.length > 0}
              <div class="blocked-chips">
                {#each newBlockedBy as depId}
                  <span class="dep-chip">
                    {depId.slice(0, 8)}
                    <button type="button" class="chip-remove" onclick={() => removeBlockedBy(depId)}>{'\u00D7'}</button>
                  </span>
                {/each}
              </div>
            {/if}
          </div>
        </div>
        {#if selectedAgentId}
          <div class="form-field">
            <label class="form-label" for="task-session">Session ID (auto-filled from agent)</label>
            <input id="task-session" type="text" bind:value={newSessionId} placeholder="Link to session..." />
          </div>
        {/if}
      </div>
    {/if}

    <div class="form-actions">
      <button type="button" class="btn btn-ghost" onclick={closeCreateModal}>Cancel</button>
      <button type="submit" class="btn btn-success" disabled={creating || !newTitle.trim()}>
        {creating ? 'Creating...' : 'Create Task'}
      </button>
    </div>
  </form>
</Modal>

<!-- Resolve Task Modal -->
<Modal open={showResolveModal} title="Resolve Task" onClose={closeResolve}>
  <form class="create-form" onsubmit={(e) => { e.preventDefault(); submitResolve(); }}>
    <p class="resolve-title">{resolveTaskTitle}</p>
    <div class="form-field">
      <label class="form-label" for="resolution-text">Resolution</label>
      <textarea
        id="resolution-text"
        bind:value={resolutionText}
        placeholder="What was done to complete this task?"
        rows="3"
      ></textarea>
    </div>
    <div class="form-actions">
      <button type="button" class="btn btn-ghost" onclick={closeResolve}>Cancel</button>
      <button type="submit" class="btn btn-success" disabled={resolving}>
        {resolving ? 'Resolving...' : 'Resolve'}
      </button>
    </div>
  </form>
</Modal>

<!-- Task Detail Drawer -->
<DetailDrawer
  open={!!selectedTask}
  title={selectedTask?.title ?? ''}
  subtitle={selectedTask ? taskAgentName(selectedTask) : 'Unassigned'}
  onClose={closeTaskDetail}
>
  {#snippet header()}
    {#if selectedTask}
      <div class="detail-stats">
        <div class="stat-chip">
          <Badge text={selectedTask.priority ?? 'medium'} variant={priorityVariant(selectedTask.priority)} />
        </div>
        <div class="stat-chip">
          <Badge text={selectedTask.status ?? 'pending'} variant={statusVariant(selectedTask.status)} />
        </div>
        {#if selectedTask.created_at}
          <div class="stat-chip">
            <span class="stat-chip-value">{relativeTime(selectedTask.created_at)}</span>
            <span class="stat-chip-label">created</span>
          </div>
        {/if}
      </div>
    {/if}
  {/snippet}

  {#if selectedTask}
    {#if selectedTask.context}
      <div class="section">
        <div class="section-title text-xs uppercase text-muted">Context</div>
        <pre class="detail-pre">{selectedTask.context}</pre>
      </div>
    {/if}
    {#if selectedTask.file_path}
      <div class="section">
        <div class="section-title text-xs uppercase text-muted">File</div>
        <span class="text-mono text-sm">{selectedTask.file_path}</span>
      </div>
    {/if}
    {#if selectedTask.tags?.length}
      <div class="section">
        <div class="section-title text-xs uppercase text-muted">Tags</div>
        <div class="tag-chips">
          {#each selectedTask.tags as tag}
            <span class="tag-chip">{tag}</span>
          {/each}
        </div>
      </div>
    {/if}
    {#if selectedTask.blocked_by?.length}
      <div class="section">
        <div class="section-title text-xs uppercase text-muted">Blocked By</div>
        <div class="dep-list">
          {#each selectedTask.blocked_by as depId}
            <span class="blocked-id" class:resolved={selectedTask.resolved_deps?.includes(depId)}>
              {depId.slice(0, 12)}
            </span>
          {/each}
        </div>
      </div>
    {/if}
    {#if selectedTaskRelations.length > 0}
      <div class="section">
        <div class="section-title text-xs uppercase text-muted">Dependency Relations</div>
        <div class="relation-cards">
          {#each selectedTaskRelations as relation}
            <div class="relation-card" class:relation-card-cross={relation.cross_agent}>
              <div class="relation-card-title">{relation.blocked_by_task_title || relation.blocked_by_task_id}</div>
              <div class="relation-card-meta">
                {relation.blocked_by_status || 'unknown'} · {relation.cross_agent ? 'cross-agent' : 'local'}
                {#if relation.resolved} · resolved{/if}
              </div>
              <div class="relation-card-detail">
                {relation.blocked_by_agent_id || 'unassigned'} · {relation.blocked_by_namespace || 'unscoped'}
              </div>
            </div>
          {/each}
        </div>
      </div>
    {/if}
    {#if selectedTask.resolution}
      <div class="section">
        <div class="section-title text-xs uppercase text-muted">Resolution</div>
        <pre class="detail-pre">{selectedTask.resolution}</pre>
      </div>
    {/if}
  {/if}

  {#snippet footer()}
    {#if selectedTask && selectedTask.status !== 'completed' && selectedTask.status !== 'cancelled'}
      <button class="btn btn-success" onclick={() => { closeTaskDetail(); openResolve(selectedTask); }}>
        Resolve Task
      </button>
    {/if}
  {/snippet}
</DetailDrawer>

<style>
  .tasks-panel {
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  .header-bar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: var(--space-2) 0;
    border-bottom: 1px solid var(--border);
    flex-wrap: wrap;
    gap: var(--space-2);
    position: relative;
  }

  .header-bar::after {
    content: '';
    position: absolute;
    bottom: 0;
    left: 10%;
    right: 10%;
    height: 1px;
    background: linear-gradient(90deg, transparent, rgba(0, 200, 255, 0.06) 50%, transparent);
    pointer-events: none;
  }

  .header-stats {
    display: flex;
    align-items: center;
    gap: var(--space-3);
    font-size: var(--text-sm);
  }

  .header-total {
    font-weight: 600;
    color: var(--fg-primary);
  }

  .header-actions {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }

  .view-toggle {
    display: flex;
    gap: 2px;
    background: var(--bg-tertiary);
    border-radius: var(--radius-sm);
    padding: 2px;
  }

  .active-toggle {
    background: var(--bg-elevated) !important;
    color: var(--fg-primary) !important;
    box-shadow: 0 0 4px rgba(0, 200, 255, 0.1);
  }

  .task-content {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    position: relative;
  }

  .task-content::before {
    content: '';
    position: absolute;
    inset: 0;
    border-radius: inherit;
    background: var(--surface-highlight);
    pointer-events: none;
    z-index: 1;
  }

  .tasks-layout {
    flex: 1;
    min-height: 0;
    display: grid;
    grid-template-columns: minmax(0, 1fr) 300px;
    gap: var(--space-3);
    margin-top: var(--space-2);
  }

  .task-main,
  .tasks-rail {
    min-height: 0;
  }

  .task-main {
    display: flex;
    flex-direction: column;
  }

  .tasks-rail {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    overflow-y: auto;
  }

  .radar-card {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    padding: var(--space-3);
    display: flex;
    flex-direction: column;
    gap: 4px;
    position: relative;
  }

  .radar-card::before {
    content: '';
    position: absolute;
    inset: 0;
    border-radius: inherit;
    background: var(--surface-highlight);
    pointer-events: none;
  }

  .radar-label {
    font-size: var(--text-xs);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-muted);
    font-weight: 600;
  }

  .radar-value {
    font-size: 18px;
    font-family: var(--font-mono);
    color: var(--fg-primary);
    font-weight: 700;
  }

  .radar-meta,
  .radar-list-item {
    font-size: var(--text-xs);
    color: var(--fg-muted);
    font-family: var(--font-mono);
  }

  .radar-stack {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .radar-list-item {
    display: flex;
    flex-direction: column;
    gap: 3px;
    padding-top: var(--space-2);
    border-top: 1px solid var(--border-subtle);
  }

  .radar-list-item:first-child {
    padding-top: 0;
    border-top: none;
  }

  .radar-item-title {
    color: var(--fg-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .radar-item-meta {
    color: var(--fg-dim);
  }

  .relation-cards {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .relation-card {
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    padding: var(--space-3);
    background: var(--bg-secondary);
    display: flex;
    flex-direction: column;
    gap: 4px;
    position: relative;
  }

  .relation-card::before {
    content: '';
    position: absolute;
    inset: 0;
    border-radius: inherit;
    background: var(--surface-highlight);
    pointer-events: none;
  }

  .relation-card-cross {
    border-color: color-mix(in srgb, var(--warning) 40%, var(--border));
    box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--warning) 15%, transparent);
  }

  .relation-card-title {
    font-size: var(--text-sm);
    color: var(--fg-primary);
    font-weight: 600;
  }

  .relation-card-meta,
  .relation-card-detail {
    font-size: var(--text-xs);
    color: var(--fg-dim);
    font-family: var(--font-mono);
    letter-spacing: var(--tracking-normal);
  }

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

  .blocked-col {
    display: flex;
    gap: 4px;
    flex-wrap: wrap;
  }

  .blocked-id {
    font-size: var(--text-xs);
    padding: 1px 4px;
    background: var(--bg-tertiary);
    border-radius: var(--radius-sm);
    color: var(--fg-secondary);
    border: 1px solid var(--border-subtle);
  }

  .blocked-id.resolved {
    opacity: 0.4;
    text-decoration: line-through;
  }

  /* Inline status dropdown */
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

  /* Clickable priority badge */
  .priority-btn {
    background: none;
    border: none;
    padding: 0;
    cursor: pointer;
    transition: transform 0.1s ease;
  }

  .priority-btn:hover {
    transform: scale(1.1);
  }

  .priority-btn:active {
    transform: scale(0.95);
  }

  /* Create form */
  .create-form {
    display: flex;
    flex-direction: column;
  }

  .create-form textarea {
    width: 100%;
    resize: vertical;
    font-family: inherit;
    font-size: var(--text-sm);
    padding: 6px var(--space-2);
    background: var(--bg-primary);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    color: var(--fg-primary);
    transition: border-color var(--transition-fast);
  }

  .create-form textarea:focus {
    border-color: var(--info);
    outline: none;
    box-shadow: 0 0 4px var(--glow-info);
  }

  .required {
    color: var(--error);
  }

  .form-row {
    display: flex;
    gap: var(--space-3);
  }

  .form-field-half {
    flex: 1;
  }

  .optional-toggle {
    background: none;
    border: none;
    color: var(--fg-muted);
    font-size: var(--text-xs);
    cursor: pointer;
    padding: 6px 0;
    text-align: left;
    transition: color var(--transition-fast);
  }

  .optional-toggle:hover {
    color: var(--fg-secondary);
  }

  .optional-section {
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    padding: var(--space-3);
    margin-bottom: var(--space-2);
    background: var(--bg-primary);
  }

  .blocked-by-picker {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .blocked-chips {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
  }

  .dep-chip {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    padding: 2px 6px;
    background: var(--bg-tertiary);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-sm);
    color: var(--fg-secondary);
  }

  .chip-remove {
    background: none;
    border: none;
    color: var(--fg-muted);
    cursor: pointer;
    padding: 0 2px;
    font-size: 12px;
    line-height: 1;
    transition: color var(--transition-fast);
  }

  .chip-remove:hover {
    color: var(--error);
  }

  /* Resolve modal */
  .resolve-title {
    font-weight: 600;
    color: var(--fg-primary);
    margin-bottom: var(--space-2);
    font-size: var(--text-sm);
  }

  /* Actions column */
  .actions-col {
    white-space: nowrap;
  }

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

  /* Grouped view */
  .grouped-view {
    display: flex;
    flex-direction: column;
  }

  .group-section {
    border-bottom: 1px solid var(--border);
  }

  .group-section:last-child {
    border-bottom: none;
  }

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

  .group-header:hover {
    background: var(--bg-elevated);
    color: var(--fg-primary);
  }

  .group-chevron {
    font-size: 10px;
    width: 14px;
    flex-shrink: 0;
    transition: transform var(--transition-fast);
  }

  .group-status-label {
    text-transform: capitalize;
  }

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

  .group-body {
    padding: 0;
  }

  /* --- Detail Drawer --- */

  .tag-chips {
    display: flex;
    gap: var(--space-1);
    flex-wrap: wrap;
  }

  .tag-chip {
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    padding: 2px 6px;
    background: var(--bg-tertiary);
    border-radius: var(--radius-sm);
    color: var(--fg-secondary);
    border: 1px solid var(--border-subtle);
  }

  .dep-list {
    display: flex;
    gap: var(--space-1);
    flex-wrap: wrap;
  }

  @media (max-width: 1200px) {
    .tasks-layout {
      grid-template-columns: 1fr;
    }
  }

  @media (max-width: 768px) {
    .header-bar {
      flex-direction: column;
      align-items: flex-start;
    }
    .task-title {
      max-width: 200px;
    }
  }
</style>
