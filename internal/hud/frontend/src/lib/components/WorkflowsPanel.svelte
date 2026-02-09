<script>
  import { workflowStore } from '../stores/workflows.svelte.ts';
  import Badge from '../widgets/Badge.svelte';
  import DagView from '../widgets/DagView.svelte';

  $effect(() => {
    workflowStore.startPolling(5000);
    return () => { workflowStore.stopPolling(); };
  });

  let workflows = $derived(workflowStore.workflows ?? []);
  let definitions = $derived(workflowStore.uniqueDefinitions ?? []);
  let selected = $derived(workflowStore.selectedWorkflow ?? null);

  /** Track whether we're viewing an instance or a definition. */
  let viewMode = $state(/** @type {'instance' | 'definition' | null} */ (null));
  let selectedDef = $state(/** @type {import('../stores/workflows.svelte.ts').WorkflowDefinition | null} */ (null));

  function selectWorkflow(wf) {
    viewMode = 'instance';
    selectedDef = null;
    workflowStore.fetchDetail(wf.id);
  }

  function selectDefinition(def) {
    viewMode = 'definition';
    selectedDef = def;
  }

  function statusVariant(status) {
    const map = {
      running: 'info',
      completed: 'success',
      failed: 'error',
      cancelled: 'error',
      pending: 'warning',
      waiting_approval: 'warning',
      approved: 'success',
      rejected: 'error',
    };
    return map[status] ?? 'info';
  }

  function stepProgress(wf) {
    if (!wf.steps?.length) return '0/0';
    const done = wf.steps.filter(s =>
      s.status === 'completed' || s.status === 'approved'
    ).length;
    return `${done}/${wf.steps.length}`;
  }

  function dagSteps(wf) {
    if (!wf.steps) return [];
    return wf.steps.map(s => ({
      id: s.id ?? s.name,
      name: s.name ?? s.id,
      status: s.status ?? 'pending',
      depends_on: s.depends_on ?? [],
    }));
  }

  function formatTime(ts) {
    if (!ts) return '---';
    const d = new Date(ts);
    return d.toLocaleTimeString('en-US', { hour12: false });
  }

  function formatDateTime(ts) {
    if (!ts) return '---';
    const d = new Date(ts);
    return d.toLocaleString('en-US', {
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
      hour12: false,
    });
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
    return hours + 'h ago';
  }

  function eventVariant(eventType) {
    const map = {
      started: 'info',
      completed: 'success',
      failed: 'error',
      approved: 'success',
      rejected: 'error',
      waiting_approval: 'warning',
      step_started: 'info',
      step_completed: 'success',
      step_failed: 'error',
    };
    return map[eventType] ?? 'info';
  }

  let hasWaitingStep = $derived.by(() => {
    if (!selected?.steps) return false;
    return selected.steps.some(s => s.status === 'waiting_approval');
  });

  let waitingStep = $derived.by(() => {
    if (!selected?.steps) return null;
    return selected.steps.find(s => s.status === 'waiting_approval');
  });

  function approveStep() {
    const step = waitingStep;
    if (step && selected) {
      workflowStore.approveStep(selected.id, step.id ?? step.name);
    }
  }

  function rejectStep() {
    const step = waitingStep;
    if (step && selected) {
      workflowStore.rejectStep(selected.id, step.id ?? step.name);
    }
  }
</script>

<div class="panel workflows-panel">
  <!-- Left sidebar: workflow list -->
  <div class="wf-sidebar">
    <div class="sidebar-header">
      <span class="card-title">Workflows</span>
      <span class="count-badge">{workflows.length + definitions.length}</span>
    </div>
    <div class="wf-list">
      <!-- Running instances -->
      {#if workflows.length > 0}
        <div class="wf-section-label">Running</div>
        {#each workflows as wf (wf.id)}
          <button
            class="wf-item"
            class:wf-selected={viewMode === 'instance' && selected?.id === wf.id}
            onclick={() => selectWorkflow(wf)}
          >
            <div class="wf-item-top">
              <span class="wf-name truncate">{wf.name ?? wf.id}</span>
              <Badge text={wf.status ?? 'pending'} variant={statusVariant(wf.status)} />
            </div>
            <div class="wf-item-bottom">
              <span class="wf-progress text-mono text-xs">{stepProgress(wf)} steps</span>
              <span class="wf-time text-mono text-xs text-muted">{relativeTime(wf.started_at)}</span>
            </div>
          </button>
        {/each}
      {/if}

      <!-- Registered definitions -->
      {#if definitions.length > 0}
        <div class="wf-section-label">Definitions</div>
        {#each definitions as def (def.id)}
          <button
            class="wf-item wf-def-item"
            class:wf-selected={viewMode === 'definition' && selectedDef?.id === def.id}
            onclick={() => selectDefinition(def)}
          >
            <div class="wf-item-top">
              <span class="wf-name truncate">{def.name}</span>
              <Badge text="{def.step_count} steps" variant="info" />
            </div>
            <div class="wf-item-bottom">
              <span class="wf-progress text-mono text-xs text-muted">{def.created_by}</span>
              <span class="wf-time text-mono text-xs text-muted">{relativeTime(def.created_at)}</span>
            </div>
          </button>
        {/each}
      {/if}

      <!-- Empty state -->
      {#if workflows.length === 0 && definitions.length === 0}
        <div class="empty-state">
          <span class="text-muted text-sm">No workflows</span>
        </div>
      {/if}
    </div>
  </div>

  <!-- Right main area: detail -->
  <div class="wf-detail">
    {#if viewMode === 'instance' && selected}
      <!-- Instance detail view -->
      <div class="detail-top">
        <div class="detail-title-row">
          <h2 class="detail-title">{selected.name ?? selected.id}</h2>
          <Badge text={selected.status ?? 'pending'} variant={statusVariant(selected.status)} />
        </div>
        <div class="detail-meta">
          <span class="text-mono text-xs text-muted">
            Started: {formatDateTime(selected.started_at)}
          </span>
          {#if selected.completed_at}
            <span class="text-mono text-xs text-muted">
              Completed: {formatDateTime(selected.completed_at)}
            </span>
          {/if}
          <span class="text-mono text-xs text-muted">
            Progress: {stepProgress(selected)}
          </span>
        </div>
      </div>

      <!-- Center: DAG -->
      <div class="detail-dag">
        <DagView steps={dagSteps(selected)} />
      </div>

      <!-- Bottom: Event timeline -->
      <div class="detail-events">
        <div class="events-header">
          <span class="card-title">Event Timeline</span>
        </div>
        <div class="events-scroll">
          {#if selected.events?.length}
            {#each selected.events as event, i (event.id ?? `${event.timestamp}-${i}`)}
              <div class="event-row">
                <span class="event-time text-mono">{formatTime(event.timestamp)}</span>
                <Badge text={event.event_type ?? 'event'} variant={eventVariant(event.event_type)} />
                {#if event.step_name}
                  <span class="event-step text-mono">{event.step_name}</span>
                {/if}
                {#if event.details}
                  <span class="event-details text-muted truncate">{event.details}</span>
                {/if}
              </div>
            {/each}
          {:else}
            <div class="empty-state">
              <span class="text-muted text-sm">No events yet</span>
            </div>
          {/if}
        </div>
      </div>

      <!-- Action bar -->
      <div class="action-bar">
        <button
          class="btn btn-success"
          disabled={!hasWaitingStep}
          onclick={approveStep}
        >
          Approve
        </button>
        <button
          class="btn btn-danger"
          disabled={!hasWaitingStep}
          onclick={rejectStep}
        >
          Reject
        </button>
        <div class="toolbar-spacer"></div>
        <button class="btn btn-ghost">Cancel Workflow</button>
      </div>

    {:else if viewMode === 'definition' && selectedDef}
      <!-- Definition detail view -->
      <div class="detail-top">
        <div class="detail-title-row">
          <h2 class="detail-title">{selectedDef.name}</h2>
          <Badge text="definition" variant="info" />
        </div>
        <div class="detail-meta">
          <span class="text-mono text-xs text-muted">
            Steps: {selectedDef.step_count}
          </span>
          <span class="text-mono text-xs text-muted">
            Created by: {selectedDef.created_by}
          </span>
          {#if selectedDef.namespace}
            <span class="text-mono text-xs text-muted">
              Namespace: {selectedDef.namespace}
            </span>
          {/if}
          <span class="text-mono text-xs text-muted">
            Registered: {formatDateTime(selectedDef.created_at)}
          </span>
        </div>
      </div>

      <div class="def-detail-body">
        {#if selectedDef.description}
          <div class="def-description">
            <span class="card-title">Description</span>
            <p class="def-description-text">{selectedDef.description}</p>
          </div>
        {/if}

        <div class="def-info-grid">
          <div class="def-info-card">
            <span class="def-info-label">ID</span>
            <span class="def-info-value text-mono">{selectedDef.id}</span>
          </div>
          <div class="def-info-card">
            <span class="def-info-label">Steps</span>
            <span class="def-info-value">{selectedDef.step_count}</span>
          </div>
          <div class="def-info-card">
            <span class="def-info-label">Created By</span>
            <span class="def-info-value">{selectedDef.created_by}</span>
          </div>
          {#if selectedDef.namespace}
            <div class="def-info-card">
              <span class="def-info-label">Namespace</span>
              <span class="def-info-value">{selectedDef.namespace}</span>
            </div>
          {/if}
        </div>

        <div class="def-usage">
          <span class="card-title">Usage</span>
          <code class="def-usage-code">agent_workflow_start(definition_id="{selectedDef.name}", input=&#123;...&#125;)</code>
        </div>
      </div>

    {:else}
      <div class="no-selection">
        <div class="no-selection-icon">&#9881;</div>
        <span class="text-muted">Select a workflow</span>
        <span class="text-xs text-muted">Choose a workflow from the list to view its DAG and events</span>
      </div>
    {/if}
  </div>
</div>

<style>
  .workflows-panel {
    display: flex;
    overflow: hidden;
    gap: 0;
  }

  /* Sidebar */
  .wf-sidebar {
    width: 30%;
    min-width: 220px;
    border-right: 1px solid var(--border);
    display: flex;
    flex-direction: column;
    background: var(--bg-secondary);
  }

  .sidebar-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 12px 14px;
    border-bottom: 1px solid var(--border);
  }

  .count-badge {
    font-family: var(--font-mono);
    font-size: 10px;
    background: var(--bg-tertiary);
    color: var(--fg-secondary);
    padding: 1px 6px;
    border-radius: var(--radius-lg);
  }

  .wf-list {
    flex: 1;
    overflow-y: auto;
  }

  .wf-item {
    display: flex;
    flex-direction: column;
    gap: 4px;
    width: 100%;
    padding: 10px 14px;
    text-align: left;
    border-bottom: 1px solid var(--border);
    cursor: pointer;
    transition: background 0.1s;
  }

  .wf-item:hover {
    background: var(--bg-tertiary);
  }

  .wf-selected {
    background: rgba(1, 135, 153, 0.08) !important;
    border-left: 3px solid var(--info);
    padding-left: 11px;
  }

  .wf-item-top {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
  }

  .wf-name {
    font-weight: 500;
    color: var(--fg-primary);
    font-size: 12px;
  }

  .wf-item-bottom {
    display: flex;
    justify-content: space-between;
    gap: 8px;
  }

  .wf-progress {
    color: var(--fg-secondary);
  }

  .wf-time {
    flex-shrink: 0;
  }

  /* Detail area */
  .wf-detail {
    flex: 1;
    display: flex;
    flex-direction: column;
    overflow: hidden;
    min-width: 0;
  }

  .detail-top {
    padding: 14px 16px;
    border-bottom: 1px solid var(--border);
  }

  .detail-title-row {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 6px;
  }

  .detail-title {
    font-size: 16px;
    font-weight: 600;
    color: var(--fg-primary);
    margin: 0;
  }

  .detail-meta {
    display: flex;
    gap: 16px;
    flex-wrap: wrap;
  }

  .detail-dag {
    flex: 1;
    min-height: 120px;
    overflow: auto;
    padding: 12px 16px;
    border-bottom: 1px solid var(--border);
  }

  .detail-events {
    flex: 1;
    min-height: 100px;
    max-height: 200px;
    display: flex;
    flex-direction: column;
    border-bottom: 1px solid var(--border);
  }

  .events-header {
    padding: 8px 16px;
    border-bottom: 1px solid var(--border);
  }

  .events-scroll {
    flex: 1;
    overflow-y: auto;
  }

  .event-row {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 5px 16px;
    font-size: 12px;
    border-bottom: 1px solid rgba(3, 89, 100, 0.3);
  }

  .event-row:last-child {
    border-bottom: none;
  }

  .event-time {
    color: var(--fg-muted);
    font-size: 11px;
    flex-shrink: 0;
    width: 65px;
  }

  .event-step {
    color: var(--fg-secondary);
    font-size: 11px;
    flex-shrink: 0;
  }

  .event-details {
    flex: 1;
    min-width: 0;
    font-size: 11px;
  }

  /* Action bar */
  .action-bar {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 10px 16px;
    background: var(--bg-secondary);
    border-top: 1px solid var(--border);
  }

  .action-bar .btn:disabled {
    opacity: 0.35;
    cursor: not-allowed;
  }

  /* Section labels */
  .wf-section-label {
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    color: var(--fg-muted);
    padding: 8px 14px 4px;
    background: var(--bg-secondary);
  }

  .wf-def-item {
    opacity: 0.85;
  }

  /* Definition detail view */
  .def-detail-body {
    flex: 1;
    overflow-y: auto;
    padding: 16px;
    display: flex;
    flex-direction: column;
    gap: 20px;
  }

  .def-description {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .def-description-text {
    color: var(--fg-secondary);
    font-size: 13px;
    line-height: 1.5;
    margin: 0;
  }

  .def-info-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
    gap: 10px;
  }

  .def-info-card {
    display: flex;
    flex-direction: column;
    gap: 3px;
    padding: 10px 12px;
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
  }

  .def-info-label {
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.3px;
    color: var(--fg-muted);
  }

  .def-info-value {
    font-size: 13px;
    color: var(--fg-primary);
    word-break: break-all;
  }

  .def-usage {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .def-usage-code {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--fg-secondary);
    background: var(--bg-tertiary);
    padding: 10px 12px;
    border-radius: var(--radius-md);
    border: 1px solid var(--border);
    white-space: pre-wrap;
    word-break: break-all;
  }

  /* No selection */
  .no-selection {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 8px;
  }

  .no-selection-icon {
    font-size: 40px;
    opacity: 0.3;
  }
</style>
