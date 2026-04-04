<script>
  import Badge from '../widgets/Badge.svelte';
  import EmptyState from './shared/EmptyState.svelte';

  let snapshot = $state(null);
  let policies = $state(null);
  let loading = $state(true);
  let error = $state('');
  let pollTimer = $state(null);

  // Policy editing
  let editingPolicies = $state(false);
  let policyDraft = $state(null);
  let savingPolicy = $state(false);

  // Dispatch request
  let dispatchTaskId = $state('');
  let dispatchTaskTitle = $state('');
  let dispatching = $state(false);
  let dispatchResult = $state(null);

  // Preflight request
  let preflightAgentId = $state('');
  let preflightFilePath = $state('');
  let preflightResult = $state(null);
  let preflighting = $state(false);

  async function fetchSnapshot() {
    try {
      const res = await fetch('/api/shuttle/status');
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      snapshot = await res.json();
      error = '';
    } catch (e) {
      error = e.message ?? 'Failed to fetch shuttle status';
    } finally {
      loading = false;
    }
  }

  async function fetchPolicies() {
    try {
      const res = await fetch('/api/shuttle/policies');
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      policies = await res.json();
    } catch (e) {
      console.error('Failed to fetch policies:', e);
    }
  }

  function startPolling() {
    fetchSnapshot();
    fetchPolicies();
    pollTimer = setInterval(fetchSnapshot, 5000);
  }

  function stopPolling() {
    if (pollTimer) {
      clearInterval(pollTimer);
      pollTimer = null;
    }
  }

  $effect(() => {
    startPolling();
    return () => stopPolling();
  });

  // Derived data
  let capacities = $derived(snapshot?.capacities ?? []);
  let recommendations = $derived(snapshot?.recommendations ?? []);
  let pendingTasks = $derived(snapshot?.pending_tasks ?? 0);
  let activeAgents = $derived(snapshot?.active_agents ?? 0);
  let systemLoad = $derived(snapshot?.system_load ?? 0);

  // Capacity card colour
  function utilizationVariant(util) {
    if (util >= 0.8) return 'critical';
    if (util >= 0.5) return 'warning';
    return 'success';
  }

  function statusBadgeVariant(status) {
    if (status === 'active') return 'success';
    if (status === 'idle') return 'info';
    return 'neutral';
  }

  // Policy editing
  function startEditPolicies() {
    policyDraft = JSON.parse(JSON.stringify(policies ?? {}));
    editingPolicies = true;
  }

  function cancelEditPolicies() {
    editingPolicies = false;
    policyDraft = null;
  }

  async function savePolicies() {
    savingPolicy = true;
    try {
      const res = await fetch('/api/shuttle/policies', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(policyDraft),
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const result = await res.json();
      policies = result.policy ?? policyDraft;
      editingPolicies = false;
      policyDraft = null;
    } catch (e) {
      console.error('Failed to save policies:', e);
    } finally {
      savingPolicy = false;
    }
  }

  // Dispatch evaluation
  async function evaluateDispatch() {
    if (!dispatchTaskId.trim()) return;
    dispatching = true;
    dispatchResult = null;
    try {
      const res = await fetch('/api/shuttle/dispatch', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          task_id: dispatchTaskId.trim(),
          task_title: dispatchTaskTitle.trim(),
        }),
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      dispatchResult = await res.json();
    } catch (e) {
      dispatchResult = { error: e.message };
    } finally {
      dispatching = false;
    }
  }

  // Preflight check
  async function runPreflight() {
    if (!preflightAgentId.trim() || !preflightFilePath.trim()) return;
    preflighting = true;
    preflightResult = null;
    try {
      const res = await fetch('/api/shuttle/preflight', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          agent_id: preflightAgentId.trim(),
          file_path: preflightFilePath.trim(),
        }),
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      preflightResult = await res.json();
    } catch (e) {
      preflightResult = { error: e.message };
    } finally {
      preflighting = false;
    }
  }
</script>

<div class="shuttle-panel">
  <header class="panel-header">
    <h2>Shuttle</h2>
    <div class="header-stats">
      <span class="stat">
        <span class="stat-value">{activeAgents}</span>
        <span class="stat-label">agents</span>
      </span>
      <span class="stat">
        <span class="stat-value">{pendingTasks}</span>
        <span class="stat-label">pending</span>
      </span>
      <span class="stat">
        <span class="stat-value">{(systemLoad * 100).toFixed(0)}%</span>
        <span class="stat-label">load</span>
      </span>
    </div>
  </header>

  {#if loading}
    <div class="loading">Loading shuttle data...</div>
  {:else if error}
    <div class="error-banner">{error}</div>
  {:else}

    <!-- Agent Capacity Cards -->
    <section class="section">
      <h3>Agent Capacity</h3>
      {#if capacities.length === 0}
        <EmptyState message="No agents registered" />
      {:else}
        <div class="capacity-grid">
          {#each capacities as cap}
            <div class="capacity-card" class:idle={cap.status === 'idle'}>
              <div class="card-header">
                <span class="agent-name">{cap.agent_id}</span>
                <Badge variant={statusBadgeVariant(cap.status)}>{cap.status}</Badge>
              </div>
              <div class="card-body">
                <div class="utilization-bar">
                  <div
                    class="utilization-fill {utilizationVariant(cap.utilization)}"
                    style="width: {Math.min(cap.utilization * 100, 100)}%"
                  ></div>
                </div>
                <div class="card-stats">
                  <span>{cap.active_tasks}/{cap.max_tasks} tasks</span>
                  <span>{cap.available_slots} slots free</span>
                  <span>{(cap.utilization * 100).toFixed(0)}% util</span>
                </div>
                <div class="card-tokens">
                  {cap.tokens_used.toLocaleString()} / {cap.token_budget.toLocaleString()} tokens
                </div>
              </div>
            </div>
          {/each}
        </div>
      {/if}
    </section>

    <!-- Recommendations -->
    <section class="section">
      <h3>Dispatch Recommendations</h3>
      {#if recommendations.length === 0}
        <EmptyState message="No pending dispatch recommendations" />
      {:else}
        <div class="recommendations-list">
          {#each recommendations as rec}
            <div class="recommendation-item">
              <div class="rec-task">
                <span class="rec-title">{rec.task_title || rec.task_id}</span>
                <span class="rec-id">{rec.task_id}</span>
              </div>
              <div class="rec-arrow">&#8594;</div>
              <div class="rec-agent">
                <Badge variant="info">{rec.recommended_agent}</Badge>
              </div>
              <div class="rec-reason">{rec.reason}</div>
            </div>
          {/each}
        </div>
      {/if}
    </section>

    <!-- Dispatch Evaluation -->
    <section class="section">
      <h3>Evaluate Dispatch</h3>
      <div class="dispatch-form">
        <input
          type="text"
          placeholder="Task ID"
          bind:value={dispatchTaskId}
          class="input-field"
        />
        <input
          type="text"
          placeholder="Task title (optional)"
          bind:value={dispatchTaskTitle}
          class="input-field"
        />
        <button
          class="btn btn-primary"
          onclick={evaluateDispatch}
          disabled={dispatching || !dispatchTaskId.trim()}
        >
          {dispatching ? 'Evaluating...' : 'Evaluate'}
        </button>
      </div>
      {#if dispatchResult}
        <div class="result-box" class:error-result={dispatchResult.error}>
          {#if dispatchResult.error}
            <span class="result-error">{dispatchResult.error}</span>
          {:else}
            <span>Recommended: <strong>{dispatchResult.recommended_agent || 'none'}</strong></span>
            {#if dispatchResult.reason}
              <span class="result-reason">{dispatchResult.reason}</span>
            {/if}
          {/if}
        </div>
      {/if}
    </section>

    <!-- Preflight Check -->
    <section class="section">
      <h3>Preflight Conflict Check</h3>
      <div class="preflight-form">
        <input
          type="text"
          placeholder="Agent ID"
          bind:value={preflightAgentId}
          class="input-field"
        />
        <input
          type="text"
          placeholder="File path"
          bind:value={preflightFilePath}
          class="input-field"
        />
        <button
          class="btn btn-secondary"
          onclick={runPreflight}
          disabled={preflighting || !preflightAgentId.trim() || !preflightFilePath.trim()}
        >
          {preflighting ? 'Checking...' : 'Check'}
        </button>
      </div>
      {#if preflightResult}
        <div class="result-box" class:error-result={preflightResult.error} class:clear-result={preflightResult.clear}>
          {#if preflightResult.error}
            <span class="result-error">{preflightResult.error}</span>
          {:else if preflightResult.clear}
            <span class="result-clear">No conflicts detected</span>
          {:else}
            <div class="conflicts-list">
              {#each (preflightResult.conflicts ?? []) as conflict}
                <div class="conflict-item">
                  <Badge variant="critical">conflict</Badge>
                  <span>{conflict.file_path} held by <strong>{conflict.held_by}</strong></span>
                </div>
              {/each}
            </div>
          {/if}
        </div>
      {/if}
    </section>

    <!-- Policy Controls -->
    <section class="section">
      <h3>
        Policies
        {#if !editingPolicies}
          <button class="btn btn-sm" onclick={startEditPolicies}>Edit</button>
        {/if}
      </h3>
      {#if editingPolicies && policyDraft}
        <div class="policy-editor">
          <label class="policy-field">
            <span>Max Tasks Per Agent</span>
            <input type="number" bind:value={policyDraft.dispatch.max_tasks_per_agent} min="1" max="50" />
          </label>
          <label class="policy-field">
            <span>Token Budget Cap</span>
            <input type="number" bind:value={policyDraft.dispatch.token_budget_cap} min="0" step="10000" />
          </label>
          <label class="policy-field">
            <span>Prefer Idle Agents</span>
            <input type="checkbox" bind:checked={policyDraft.dispatch.prefer_idle_agents} />
          </label>
          <label class="policy-field">
            <span>Enable Auto-Dispatch</span>
            <input type="checkbox" bind:checked={policyDraft.dispatch.enable_auto_dispatch} />
          </label>
          <div class="policy-actions">
            <button class="btn btn-primary" onclick={savePolicies} disabled={savingPolicy}>
              {savingPolicy ? 'Saving...' : 'Save'}
            </button>
            <button class="btn btn-secondary" onclick={cancelEditPolicies}>Cancel</button>
          </div>
        </div>
      {:else if policies}
        <div class="policy-view">
          <div class="policy-item">
            <span class="policy-label">Max Tasks Per Agent</span>
            <span class="policy-value">{policies.dispatch?.max_tasks_per_agent ?? '-'}</span>
          </div>
          <div class="policy-item">
            <span class="policy-label">Token Budget Cap</span>
            <span class="policy-value">{(policies.dispatch?.token_budget_cap ?? 0).toLocaleString()}</span>
          </div>
          <div class="policy-item">
            <span class="policy-label">Prefer Idle Agents</span>
            <span class="policy-value">{policies.dispatch?.prefer_idle_agents ? 'Yes' : 'No'}</span>
          </div>
          <div class="policy-item">
            <span class="policy-label">Auto-Dispatch</span>
            <Badge variant={policies.dispatch?.enable_auto_dispatch ? 'success' : 'neutral'}>
              {policies.dispatch?.enable_auto_dispatch ? 'Enabled' : 'Disabled'}
            </Badge>
          </div>
        </div>
      {/if}
    </section>
  {/if}
</div>

<style>
  .shuttle-panel {
    display: flex;
    flex-direction: column;
    gap: var(--space-4);
    padding: var(--space-4);
  }

  .panel-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
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

  .panel-header h2 {
    margin: 0;
    font-size: var(--text-lg);
    color: var(--fg-primary);
  }

  .header-stats {
    display: flex;
    gap: var(--space-4);
  }

  .stat {
    display: flex;
    flex-direction: column;
    align-items: center;
  }

  .stat-value {
    font-size: var(--text-base);
    font-weight: 600;
    color: var(--fg-primary);
    font-family: var(--font-mono);
  }

  .stat-label {
    font-size: var(--text-xs);
    color: var(--fg-dim);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
  }

  .section {
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    padding: var(--space-3);
    position: relative;
  }

  .section::before {
    content: '';
    position: absolute;
    inset: 0;
    border-radius: inherit;
    background: var(--surface-highlight);
    pointer-events: none;
  }

  .section h3 {
    margin: 0 0 var(--space-2) 0;
    font-size: var(--text-sm);
    display: flex;
    align-items: center;
    gap: var(--space-2);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-muted);
  }

  .loading, .error-banner {
    padding: var(--space-4);
    text-align: center;
    border-radius: var(--radius-md);
    color: var(--fg-muted);
  }

  .error-banner {
    background: var(--error-dim);
    color: var(--error);
    border: 1px solid rgba(255, 61, 113, 0.2);
  }

  /* Capacity Grid */
  .capacity-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
    gap: var(--space-3);
  }

  .capacity-card {
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    padding: var(--space-3);
    background: var(--bg-secondary);
    position: relative;
    transition: border-color var(--transition-fast);
  }

  .capacity-card::before {
    content: '';
    position: absolute;
    inset: 0;
    border-radius: inherit;
    background: var(--surface-highlight);
    pointer-events: none;
  }

  .capacity-card.idle {
    border-color: var(--info);
    box-shadow: 0 0 6px var(--glow-info);
  }

  .card-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: var(--space-2);
  }

  .agent-name {
    font-weight: 600;
    font-size: var(--text-sm);
    color: var(--fg-primary);
    font-family: var(--font-mono);
  }

  .utilization-bar {
    height: 6px;
    background: var(--bg-elevated);
    border-radius: var(--radius-xs);
    overflow: hidden;
    margin-bottom: var(--space-2);
  }

  .utilization-fill {
    height: 100%;
    border-radius: var(--radius-xs);
    transition: width var(--transition-slow);
  }

  .utilization-fill.success { background: var(--success); box-shadow: 0 0 4px var(--glow-success); }
  .utilization-fill.warning { background: var(--warning); box-shadow: 0 0 4px var(--glow-warning); }
  .utilization-fill.critical { background: var(--error); box-shadow: 0 0 4px var(--glow-error); }

  .card-stats {
    display: flex;
    justify-content: space-between;
    font-size: var(--text-xs);
    color: var(--fg-secondary);
    margin-bottom: var(--space-1);
  }

  .card-tokens {
    font-size: var(--text-xs);
    color: var(--fg-dim);
    font-family: var(--font-mono);
  }

  /* Recommendations */
  .recommendations-list {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .recommendation-item {
    display: flex;
    align-items: center;
    gap: var(--space-3);
    padding: var(--space-2);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-md);
    font-size: var(--text-sm);
    transition: background var(--transition-fast);
  }

  .recommendation-item:hover {
    background: var(--bg-tertiary);
  }

  .rec-task {
    flex: 1;
    display: flex;
    flex-direction: column;
  }

  .rec-title {
    font-weight: 500;
    color: var(--fg-primary);
  }

  .rec-id {
    font-size: var(--text-xs);
    color: var(--fg-dim);
    font-family: var(--font-mono);
  }

  .rec-arrow {
    color: var(--fg-muted);
  }

  .rec-reason {
    font-size: var(--text-xs);
    color: var(--fg-muted);
    flex: 1;
    text-align: right;
  }

  /* Forms */
  .dispatch-form, .preflight-form {
    display: flex;
    gap: var(--space-2);
    align-items: center;
    flex-wrap: wrap;
  }

  .input-field {
    padding: var(--space-1) var(--space-2);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--bg-primary);
    color: var(--fg-primary);
    font-size: var(--text-sm);
    font-family: var(--font-mono);
    flex: 1;
    min-width: 120px;
    transition: border-color var(--transition-normal), box-shadow var(--transition-normal);
  }

  .input-field:focus {
    border-color: var(--border-active);
    box-shadow: 0 0 0 2px rgba(0, 200, 255, 0.08);
    outline: none;
  }

  .btn {
    padding: var(--space-1) var(--space-3);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    cursor: pointer;
    font-size: var(--text-sm);
    background: transparent;
    color: var(--fg-primary);
    transition: background var(--transition-fast), border-color var(--transition-fast);
  }

  .btn:hover {
    background: var(--bg-elevated);
  }

  .btn:disabled { opacity: 0.5; cursor: not-allowed; }

  .btn-primary {
    background: var(--info-dim);
    border-color: var(--info);
    color: var(--info);
  }

  .btn-primary:hover {
    background: rgba(0, 200, 255, 0.18);
  }

  .btn-secondary {
    background: var(--bg-tertiary);
  }

  .btn-sm {
    font-size: var(--text-xs);
    padding: 2px var(--space-2);
  }

  .result-box {
    margin-top: var(--space-2);
    padding: var(--space-2);
    border-radius: var(--radius-sm);
    border: 1px solid var(--border);
    font-size: var(--text-sm);
  }

  .error-result {
    border-color: rgba(255, 61, 113, 0.2);
    background: var(--error-dim);
  }

  .clear-result {
    border-color: rgba(34, 224, 118, 0.2);
    background: var(--success-dim);
  }

  .result-error { color: var(--error); }
  .result-clear { color: var(--success); }

  .result-reason {
    display: block;
    font-size: var(--text-xs);
    color: var(--fg-muted);
    margin-top: var(--space-1);
  }

  .conflicts-list { display: flex; flex-direction: column; gap: var(--space-1); }

  .conflict-item {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    font-size: var(--text-sm);
  }

  /* Policy */
  .policy-view {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: var(--space-2);
  }

  .policy-item {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: var(--space-1) 0;
    border-bottom: 1px solid var(--border-subtle);
  }

  .policy-label {
    font-size: var(--text-sm);
    color: var(--fg-secondary);
  }

  .policy-value {
    font-weight: 600;
    font-size: var(--text-sm);
    color: var(--fg-primary);
    font-family: var(--font-mono);
  }

  .policy-editor {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .policy-field {
    display: flex;
    justify-content: space-between;
    align-items: center;
    font-size: var(--text-sm);
    color: var(--fg-secondary);
  }

  .policy-field input[type="number"] {
    width: 100px;
    padding: var(--space-1);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--bg-primary);
    color: var(--fg-primary);
    text-align: right;
    font-family: var(--font-mono);
    transition: border-color var(--transition-normal);
  }

  .policy-field input[type="number"]:focus {
    border-color: var(--border-active);
    outline: none;
  }

  .policy-actions {
    display: flex;
    gap: var(--space-2);
    margin-top: var(--space-2);
  }
</style>
