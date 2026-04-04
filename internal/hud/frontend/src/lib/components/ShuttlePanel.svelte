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
    gap: 1rem;
    padding: 1rem;
  }

  .panel-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
  }

  .panel-header h2 {
    margin: 0;
    font-size: 1.25rem;
  }

  .header-stats {
    display: flex;
    gap: 1rem;
  }

  .stat {
    display: flex;
    flex-direction: column;
    align-items: center;
  }

  .stat-value {
    font-size: 1.1rem;
    font-weight: 600;
  }

  .stat-label {
    font-size: 0.75rem;
    opacity: 0.7;
  }

  .section {
    border: 1px solid var(--border-color, #333);
    border-radius: 8px;
    padding: 0.75rem;
  }

  .section h3 {
    margin: 0 0 0.5rem 0;
    font-size: 0.95rem;
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .loading, .error-banner {
    padding: 1rem;
    text-align: center;
    border-radius: 6px;
  }

  .error-banner {
    background: rgba(220, 38, 38, 0.1);
    color: #ef4444;
  }

  /* Capacity Grid */
  .capacity-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
    gap: 0.75rem;
  }

  .capacity-card {
    border: 1px solid var(--border-color, #333);
    border-radius: 6px;
    padding: 0.75rem;
    background: var(--card-bg, rgba(255,255,255,0.03));
  }

  .capacity-card.idle {
    border-color: var(--color-info, #3b82f6);
  }

  .card-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 0.5rem;
  }

  .agent-name {
    font-weight: 600;
    font-size: 0.9rem;
  }

  .utilization-bar {
    height: 6px;
    background: rgba(255,255,255,0.1);
    border-radius: 3px;
    overflow: hidden;
    margin-bottom: 0.5rem;
  }

  .utilization-fill {
    height: 100%;
    border-radius: 3px;
    transition: width 0.3s;
  }

  .utilization-fill.success { background: #22c55e; }
  .utilization-fill.warning { background: #f59e0b; }
  .utilization-fill.critical { background: #ef4444; }

  .card-stats {
    display: flex;
    justify-content: space-between;
    font-size: 0.75rem;
    opacity: 0.8;
    margin-bottom: 0.25rem;
  }

  .card-tokens {
    font-size: 0.7rem;
    opacity: 0.6;
  }

  /* Recommendations */
  .recommendations-list {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  .recommendation-item {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    padding: 0.5rem;
    border: 1px solid var(--border-color, #333);
    border-radius: 6px;
    font-size: 0.85rem;
  }

  .rec-task {
    flex: 1;
    display: flex;
    flex-direction: column;
  }

  .rec-title { font-weight: 500; }
  .rec-id { font-size: 0.7rem; opacity: 0.6; }
  .rec-arrow { opacity: 0.4; }
  .rec-reason { font-size: 0.75rem; opacity: 0.7; flex: 1; text-align: right; }

  /* Forms */
  .dispatch-form, .preflight-form {
    display: flex;
    gap: 0.5rem;
    align-items: center;
    flex-wrap: wrap;
  }

  .input-field {
    padding: 0.4rem 0.6rem;
    border: 1px solid var(--border-color, #333);
    border-radius: 4px;
    background: transparent;
    color: inherit;
    font-size: 0.85rem;
    flex: 1;
    min-width: 120px;
  }

  .btn {
    padding: 0.4rem 0.8rem;
    border: 1px solid var(--border-color, #333);
    border-radius: 4px;
    cursor: pointer;
    font-size: 0.85rem;
    background: transparent;
    color: inherit;
  }

  .btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-primary { background: rgba(59, 130, 246, 0.2); border-color: #3b82f6; }
  .btn-secondary { background: rgba(255,255,255,0.05); }
  .btn-sm { font-size: 0.75rem; padding: 0.2rem 0.5rem; }

  .result-box {
    margin-top: 0.5rem;
    padding: 0.5rem;
    border-radius: 4px;
    border: 1px solid var(--border-color, #333);
    font-size: 0.85rem;
  }

  .error-result { border-color: #ef4444; }
  .clear-result { border-color: #22c55e; }
  .result-error { color: #ef4444; }
  .result-clear { color: #22c55e; }
  .result-reason { display: block; font-size: 0.75rem; opacity: 0.7; margin-top: 0.25rem; }

  .conflicts-list { display: flex; flex-direction: column; gap: 0.25rem; }
  .conflict-item { display: flex; align-items: center; gap: 0.5rem; font-size: 0.85rem; }

  /* Policy */
  .policy-view {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 0.5rem;
  }

  .policy-item {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 0.4rem 0;
    border-bottom: 1px solid rgba(255,255,255,0.05);
  }

  .policy-label { font-size: 0.85rem; opacity: 0.8; }
  .policy-value { font-weight: 600; font-size: 0.85rem; }

  .policy-editor {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  .policy-field {
    display: flex;
    justify-content: space-between;
    align-items: center;
    font-size: 0.85rem;
  }

  .policy-field input[type="number"] {
    width: 100px;
    padding: 0.3rem;
    border: 1px solid var(--border-color, #333);
    border-radius: 4px;
    background: transparent;
    color: inherit;
    text-align: right;
  }

  .policy-actions {
    display: flex;
    gap: 0.5rem;
    margin-top: 0.5rem;
  }
</style>
