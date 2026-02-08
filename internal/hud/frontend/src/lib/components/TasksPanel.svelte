<script>
  import { taskStore } from '../stores/tasks.svelte.ts';
  import { agentStore } from '../stores/agents.svelte.ts';
  import { toastStore } from '../stores/toasts.svelte.ts';
  import Badge from '../widgets/Badge.svelte';
  import Modal from '../widgets/Modal.svelte';

  $effect(() => {
    taskStore.startPolling(5000);
    agentStore.startPolling(30000);
    return () => {
      taskStore.stopPolling();
      agentStore.stopPolling();
    };
  });

  let tasks = $derived(taskStore.tasks ?? []);
  let availableAgents = $derived(agentStore.agents ?? []);

  let searchQuery = $state('');
  let priorityFilter = $state('all');
  let agentFilter = $state('all');
  let statusFilter = $state('all');
  let viewMode = $state('flat'); // 'flat' | 'grouped'
  let collapsedGroups = $state(new Set());

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

  // Unique agents
  let agents = $derived.by(() => {
    const set = new Set();
    tasks.forEach(t => { if (t.agent) set.add(t.agent); });
    return ['all', ...Array.from(set).sort()];
  });

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

    if (priorityFilter !== 'all') {
      result = result.filter(t => t.priority === priorityFilter);
    }

    if (agentFilter !== 'all') {
      result = result.filter(t => t.agent === agentFilter);
    }

    if (statusFilter !== 'all') {
      result = result.filter(t => t.status === statusFilter);
    }

    return result;
  });

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

  function statusVariant(status) {
    const map = {
      pending: 'warning',
      in_progress: 'info',
      blocked: 'error',
      completed: 'success',
      cancelled: 'error',
    };
    return map[status] ?? 'info';
  }

  function priorityVariant(priority) {
    const map = {
      critical: 'error',
      high: 'warning',
      medium: 'info',
      low: 'accent',
    };
    return map[priority] ?? 'info';
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

  function relativeTime(ts) {
    if (!ts) return '---';
    const now = Date.now();
    const then = new Date(ts).getTime();
    const diff = now - then;
    const secs = Math.floor(diff / 1000);
    if (secs < 60) return secs + 's ago';
    const mins = Math.floor(secs / 60);
    if (mins < 60) return mins + 'm ago';
    const hours = Math.floor(mins / 60);
    if (hours < 24) return hours + 'h ago';
    const days = Math.floor(hours / 24);
    return days + 'd ago';
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

  <!-- Filter row -->
  <div class="toolbar">
    <input
      type="text"
      placeholder="Search tasks..."
      bind:value={searchQuery}
      class="search-input"
    />
    <select bind:value={priorityFilter}>
      <option value="all">All Priority</option>
      <option value="critical">Critical</option>
      <option value="high">High</option>
      <option value="medium">Medium</option>
      <option value="low">Low</option>
    </select>
    <select bind:value={agentFilter}>
      {#each agents as agent}
        <option value={agent}>{agent === 'all' ? 'All Agents' : agent}</option>
      {/each}
    </select>
    <select bind:value={statusFilter}>
      <option value="all">All Status</option>
      <option value="pending">Pending</option>
      <option value="in_progress">In Progress</option>
      <option value="blocked">Blocked</option>
      <option value="completed">Completed</option>
    </select>
    <div class="toolbar-spacer"></div>
    <span class="text-muted text-xs text-mono">{filtered.length} results</span>
  </div>

  <!-- Content -->
  <div class="task-content">
    {#if viewMode === 'flat'}
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
            {#each filtered as task (task.id)}
              <tr class="row-enter">
                <td class="task-title" title={task.context || task.title}>
                  {task.title ?? '---'}
                  {#if task.context}
                    <span class="context-hint" title={task.context}>📋</span>
                  {/if}
                </td>
                <td class="text-mono text-muted">{task.agent ?? '---'}</td>
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
                <td class="text-mono blocked-col">
                  {#if task.blocked_by?.length}
                    {#each task.blocked_by as dep}
                      <span class="blocked-id" class:resolved={task.resolved_deps?.includes(dep)}>
                        {dep.slice(0, 8)}
                      </span>
                    {/each}
                  {:else}
                    <span class="text-muted">---</span>
                  {/if}
                </td>
                <td class="text-mono text-muted">{relativeTime(task.created_at)}</td>
                <td class="actions-col">
                  {#if task.status !== 'completed' && task.status !== 'cancelled'}
                    <button class="btn-resolve" onclick={() => openResolve(task)} title="Resolve task">✓</button>
                  {/if}
                </td>
              </tr>
            {:else}
              <tr>
                <td colspan="7" class="empty-cell">No tasks match filters</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {:else}
      <!-- Grouped view -->
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
                            {task.title ?? '---'}
                            {#if task.context}
                              <span class="context-hint" title={task.context}>📋</span>
                            {/if}
                          </td>
                          <td class="text-mono text-muted">{task.agent ?? '---'}</td>
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
                          <td class="text-mono blocked-col">
                            {#if task.blocked_by?.length}
                              {#each task.blocked_by as dep}
                                <span class="blocked-id" class:resolved={task.resolved_deps?.includes(dep)}>
                                  {dep.slice(0, 8)}
                                </span>
                              {/each}
                            {:else}
                              <span class="text-muted">---</span>
                            {/if}
                          </td>
                          <td class="text-mono text-muted">{relativeTime(task.created_at)}</td>
                          <td class="actions-col">
                            {#if task.status !== 'completed' && task.status !== 'cancelled'}
                              <button class="btn-resolve" onclick={() => openResolve(task)} title="Resolve task">✓</button>
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
          <div class="empty-state">
            <span class="text-muted">No tasks match filters</span>
          </div>
        {/each}
      </div>
    {/if}
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
      {showOptional ? '▼' : '▶'} Optional fields
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
                    <button type="button" class="chip-remove" onclick={() => removeBlockedBy(depId)}>×</button>
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
    padding: 8px 0;
    border-bottom: 1px solid var(--border);
    flex-wrap: wrap;
    gap: 8px;
  }

  .header-stats {
    display: flex;
    align-items: center;
    gap: 10px;
    font-size: 12px;
  }

  .header-total {
    font-weight: 600;
    color: var(--fg-primary);
  }

  .header-actions {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .view-toggle {
    display: flex;
    gap: 4px;
    background: var(--bg-tertiary);
    border-radius: var(--border-radius);
    padding: 2px;
  }

  .active-toggle {
    background: var(--bg-secondary) !important;
    color: var(--fg-primary) !important;
  }

  .search-input {
    width: 200px;
  }

  .task-content {
    flex: 1;
    overflow-y: auto;
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--border-radius);
    margin-top: 8px;
  }

  .task-title {
    color: var(--fg-primary);
    font-weight: 500;
    max-width: 300px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .blocked-col {
    display: flex;
    gap: 4px;
    flex-wrap: wrap;
  }

  .blocked-id {
    font-size: 10px;
    padding: 1px 4px;
    background: var(--bg-tertiary);
    border-radius: 3px;
    color: var(--fg-secondary);
  }

  .blocked-id.resolved {
    opacity: 0.4;
    text-decoration: line-through;
  }

  .empty-cell {
    text-align: center;
    color: var(--fg-muted);
    padding: 32px 10px !important;
  }

  /* Inline status dropdown */
  .status-select {
    font-size: 10px;
    padding: 2px 4px;
    border-radius: 4px;
    background: var(--bg-tertiary);
    border: 1px solid var(--border);
    color: var(--fg-secondary);
    cursor: pointer;
    text-transform: capitalize;
  }

  .status-select:focus {
    border-color: var(--border-focus);
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
    font-size: 12px;
    padding: 6px 8px;
    background: var(--bg-primary);
    border: 1px solid var(--border);
    border-radius: var(--border-radius);
    color: var(--fg-primary);
  }

  .create-form textarea:focus {
    border-color: var(--border-focus);
    outline: none;
  }

  .required {
    color: var(--error);
  }

  .form-row {
    display: flex;
    gap: 12px;
  }

  .form-field-half {
    flex: 1;
  }

  .optional-toggle {
    background: none;
    border: none;
    color: var(--fg-muted);
    font-size: 11px;
    cursor: pointer;
    padding: 6px 0;
    text-align: left;
  }

  .optional-toggle:hover {
    color: var(--fg-secondary);
  }

  .optional-section {
    border: 1px solid var(--border);
    border-radius: var(--border-radius);
    padding: 12px;
    margin-bottom: 8px;
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
    font-size: 10px;
    padding: 2px 6px;
    background: var(--bg-tertiary);
    border: 1px solid var(--border);
    border-radius: 4px;
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
  }

  .chip-remove:hover {
    color: var(--error);
  }

  /* Resolve modal */
  .resolve-title {
    font-weight: 600;
    color: var(--fg-primary);
    margin-bottom: 8px;
    font-size: 13px;
  }

  /* Actions column */
  .actions-col {
    white-space: nowrap;
  }

  .btn-resolve {
    background: rgba(34, 178, 85, 0.15);
    border: 1px solid rgba(34, 178, 85, 0.3);
    color: var(--success);
    border-radius: 4px;
    padding: 2px 8px;
    font-size: 12px;
    cursor: pointer;
    transition: background 0.1s;
  }

  .btn-resolve:hover {
    background: rgba(34, 178, 85, 0.25);
  }

  .context-hint {
    font-size: 10px;
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
    gap: 8px;
    width: 100%;
    padding: 10px 12px;
    text-align: left;
    font-size: 12px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    color: var(--fg-secondary);
    background: var(--bg-tertiary);
    border: none;
    cursor: pointer;
    transition: background 0.1s;
  }

  .group-header:hover {
    background: rgba(0, 46, 52, 0.8);
    color: var(--fg-primary);
  }

  .group-chevron {
    font-size: 10px;
    width: 14px;
    flex-shrink: 0;
  }

  .group-status-label {
    text-transform: capitalize;
  }

  .count-badge {
    font-family: var(--font-mono);
    font-size: 10px;
    background: rgba(1, 135, 153, 0.15);
    color: var(--info);
    padding: 1px 6px;
    border-radius: 10px;
  }

  .group-body {
    padding: 0;
  }
</style>
