<script>
  import { toastStore } from '../../stores/toasts.svelte.ts';
  import EmptyState from '../shared/EmptyState.svelte';

  let { agents = [] } = $props();

  let diagnosticsAgentId = $state('');
  let diagnosticsLoading = $state(false);
  let diagnosticsError = $state('');
  let contextInspect = $state(null);
  let contextInspectError = $state('');
  let nudgeQueueStatus = $state(null);
  let nudgeQueueStatusError = $state('');
  let nudgeQueuePolicy = $state(null);
  let nudgeQueuePolicyError = $state('');
  let nudgePolicyCapInput = $state('');
  let nudgePolicyDebounceInput = $state('');
  let nudgePolicyDropPolicy = $state('drop_old');
  let nudgePolicyLanePriorityInput = $state('');
  let nudgePolicyUpdatedBy = $state('hud-ui');
  let nudgePolicyAdminToken = $state('');
  let nudgePolicyUpdating = $state(false);
  let nudgePolicyMutationError = $state('');
  let nudgePolicyFormDirty = $state(false);
  const nudgeDropPolicyOptions = ['drop_old', 'drop_new', 'summarize'];

  function parseLanePriorityInput(raw) {
    return raw
      .split(',')
      .map((lane) => lane.trim())
      .filter((lane) => lane.length > 0);
  }

  function markNudgePolicyDirty() {
    nudgePolicyFormDirty = true;
    nudgePolicyMutationError = '';
  }

  function hydrateNudgePolicyForm(policy) {
    if (!policy || nudgePolicyFormDirty || nudgePolicyUpdating) return;
    nudgePolicyCapInput = String(policy.cap ?? '');
    nudgePolicyDebounceInput = String(policy.debounce_ms ?? 0);
    nudgePolicyDropPolicy = nudgeDropPolicyOptions.includes(policy.drop_policy) ? policy.drop_policy : 'drop_old';
    nudgePolicyLanePriorityInput = (policy.lane_priority ?? []).join(', ');
    nudgePolicyMutationError = '';
    nudgePolicyFormDirty = false;
  }

  function resetNudgePolicyForm() {
    const source = nudgeQueuePolicy ?? nudgeQueueStatus;
    if (!source) return;
    nudgePolicyFormDirty = false;
    hydrateNudgePolicyForm(source);
  }

  async function updateNudgePolicy() {
    if (nudgePolicyUpdating) return;

    const token = nudgePolicyAdminToken.trim();
    if (!token) {
      nudgePolicyMutationError = 'Admin token is required to update policy.';
      return;
    }

    const cap = Number.parseInt(nudgePolicyCapInput.trim(), 10);
    if (!Number.isInteger(cap) || cap <= 0) {
      nudgePolicyMutationError = 'Cap must be a positive integer.';
      return;
    }

    const debounceMs = Number.parseInt(nudgePolicyDebounceInput.trim(), 10);
    if (!Number.isInteger(debounceMs) || debounceMs < 0) {
      nudgePolicyMutationError = 'Debounce must be a non-negative integer (ms).';
      return;
    }

    const dropPolicy = nudgePolicyDropPolicy.trim();
    if (!nudgeDropPolicyOptions.includes(dropPolicy)) {
      nudgePolicyMutationError = 'Drop policy must be drop_old, drop_new, or summarize.';
      return;
    }

    const lanePriority = parseLanePriorityInput(nudgePolicyLanePriorityInput);
    if (lanePriority.length === 0) {
      nudgePolicyMutationError = 'Lane priority must include at least one lane.';
      return;
    }

    const currentPolicy = nudgeQueuePolicy ?? nudgeQueueStatus;
    if (
      currentPolicy &&
      currentPolicy.cap === cap &&
      currentPolicy.debounce_ms === debounceMs &&
      currentPolicy.drop_policy === dropPolicy &&
      JSON.stringify(currentPolicy.lane_priority ?? []) === JSON.stringify(lanePriority)
    ) {
      nudgePolicyMutationError = '';
      nudgePolicyFormDirty = false;
      toastStore.info('Nudge queue policy is already up to date');
      return;
    }

    nudgePolicyMutationError = '';
    nudgePolicyUpdating = true;
    try {
      const res = await globalThis.fetch('/api/agent/nudge-queue-policy', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-Admin-Token': token,
        },
        body: JSON.stringify({
          cap,
          debounce_ms: debounceMs,
          drop_policy: dropPolicy,
          lane_priority: lanePriority,
          updated_by: nudgePolicyUpdatedBy.trim() || 'hud-ui',
        }),
      });
      let data = null;
      try {
        data = await res.json();
      } catch {
        data = null;
      }
      if (!res.ok) {
        throw new Error(data?.error || `${res.status} ${res.statusText}`);
      }
      nudgeQueuePolicy = data?.policy ?? null;
      nudgePolicyFormDirty = false;
      hydrateNudgePolicyForm(nudgeQueuePolicy);
      toastStore.success('Nudge queue policy updated');
      await fetchDiagnostics();
    } catch (e) {
      nudgePolicyMutationError = e instanceof Error ? e.message : 'Failed to update policy';
      toastStore.error(nudgePolicyMutationError);
    } finally {
      nudgePolicyUpdating = false;
    }
  }

  async function fetchJSON(url) {
    const res = await globalThis.fetch(url);
    let data = null;
    try {
      data = await res.json();
    } catch {
      data = null;
    }
    if (!res.ok) {
      const msg = data?.error || `${res.status} ${res.statusText}`;
      throw new Error(msg);
    }
    return data;
  }

  async function fetchDiagnostics() {
    const agentID = diagnosticsAgentId?.trim();
    if (!agentID) return;

    diagnosticsLoading = true;
    diagnosticsError = '';
    contextInspectError = '';
    nudgeQueueStatusError = '';
    nudgeQueuePolicyError = '';

    const [ctxResult, queueResult, policyResult] = await Promise.allSettled([
      fetchJSON(`/api/agent/context-inspect?agent_id=${encodeURIComponent(agentID)}&detail=true&limit=200`),
      fetchJSON(`/api/agent/nudge-queue?agent_id=${encodeURIComponent(agentID)}`),
      fetchJSON('/api/agent/nudge-queue-policy'),
    ]);

    if (ctxResult.status === 'fulfilled') {
      contextInspect = ctxResult.value ?? null;
    } else {
      contextInspect = null;
      contextInspectError = ctxResult.reason?.message ?? 'Failed to load context diagnostics';
    }

    if (queueResult.status === 'fulfilled') {
      nudgeQueueStatus = queueResult.value?.status ?? null;
    } else {
      nudgeQueueStatus = null;
      nudgeQueueStatusError = queueResult.reason?.message ?? 'Failed to load queue status';
    }

    if (policyResult.status === 'fulfilled') {
      nudgeQueuePolicy = policyResult.value?.policy ?? null;
    } else {
      nudgeQueuePolicy = null;
      nudgeQueuePolicyError = policyResult.reason?.message ?? 'Failed to load queue policy';
    }

    if (!nudgePolicyFormDirty && !nudgePolicyUpdating) {
      hydrateNudgePolicyForm(nudgeQueuePolicy ?? nudgeQueueStatus);
    }

    if (contextInspectError && nudgeQueueStatusError && nudgeQueuePolicyError) {
      diagnosticsError = 'Unable to load diagnostics from HUD API.';
    }

    diagnosticsLoading = false;
  }

  $effect(() => {
    if (!diagnosticsAgentId) {
      diagnosticsAgentId = agents.find((a) => a.status === 'active')?.agent_id || agents[0]?.agent_id || '';
    }
    if (!diagnosticsAgentId) return;
    fetchDiagnostics();
    const timer = setInterval(() => {
      fetchDiagnostics();
    }, 10000);
    return () => clearInterval(timer);
  });
</script>

<div class="card diagnostics-card">
  <div class="card-header diagnostics-header">
    <span class="card-title">Agent Diagnostics</span>
    <div class="diagnostics-controls">
      <label class="sr-only" for="diag-agent">Agent</label>
      <select
        id="diag-agent"
        class="form-input diagnostics-select"
        bind:value={diagnosticsAgentId}
        onchange={() => {
          fetchDiagnostics();
        }}
      >
        <option value="">Select agent...</option>
        {#each agents as agent (agent.agent_id)}
          <option value={agent.agent_id}>{agent.agent_id}</option>
        {/each}
      </select>
      <button class="btn btn-sm" onclick={() => {
        fetchDiagnostics();
      }} disabled={diagnosticsLoading || !diagnosticsAgentId}>
        {diagnosticsLoading ? 'Refreshing...' : 'Refresh'}
      </button>
    </div>
  </div>

  {#if diagnosticsError}
    <div class="text-xs text-muted diagnostics-error">{diagnosticsError}</div>
  {/if}

  {#if !diagnosticsAgentId}
    <EmptyState icon={'\u2699'} heading="Select an agent to inspect diagnostics" compact />
  {:else}
    <div class="diagnostics-metrics">
      <div class="stat-card" style="--accent-color: var(--accent)">
        <div class="metric-value">{contextInspect?.estimated_tokens ?? '---'}</div>
        <div class="metric-label">Prompt Est. Tokens</div>
      </div>
      <div class="stat-card" style="--accent-color: var(--info)">
        <div class="metric-value">{contextInspect?.entry_count ?? '---'}</div>
        <div class="metric-label">Context Entries</div>
      </div>
      <div class="stat-card" style="--accent-color: var(--warning)">
        <div class="metric-value">{nudgeQueueStatus?.pending ?? '---'}</div>
        <div class="metric-label">Queue Pending</div>
      </div>
      <div class="stat-card" style="--accent-color: var(--error)">
        <div class="metric-value">{nudgeQueueStatus?.dropped ?? '---'}</div>
        <div class="metric-label">Queue Dropped</div>
      </div>
    </div>

    <div class="diagnostics-grid">
      <div class="diag-section">
        <div class="section-header">
          <span class="section-title">Context Breakdown</span>
          {#if contextInspect?.session_id}
            <span class="text-mono text-xs text-muted">{contextInspect.session_id}</span>
          {/if}
        </div>
        {#if contextInspectError}
          <div class="text-xs text-muted">{contextInspectError}</div>
        {:else if !contextInspect}
          <div class="text-xs text-muted">No context diagnostics available.</div>
        {:else}
          <div class="diag-list">
            {#each (contextInspect.by_entry_type ?? []) as bucket}
              <div class="diag-row">
                <span class="text-mono">{bucket.entry_type}</span>
                <span class="text-xs text-muted">{bucket.count} entries</span>
                <span class="text-xs text-mono">{bucket.estimated_tokens} tok</span>
              </div>
            {/each}
          </div>
          {#if (contextInspect.sections ?? []).length > 0}
            <div class="diag-subtitle">Prompt Sections</div>
            <div class="diag-list">
              {#each (contextInspect.sections ?? []) as section}
                <div class="diag-row">
                  <span class="text-mono">{section.section}</span>
                  <span class="text-xs text-muted">{section.source}</span>
                  <span class="text-xs text-mono">{section.estimated_tokens} tok</span>
                </div>
              {/each}
            </div>
          {/if}
          {#if (contextInspect.top_entries ?? []).length > 0}
            <div class="diag-subtitle">Top Entries</div>
            <div class="diag-list">
              {#each (contextInspect.top_entries ?? []).slice(0, 5) as entry}
                <div class="diag-row">
                  <span class="truncate" title={entry.title || entry.id}>{entry.title || entry.id}</span>
                  <span class="text-xs text-muted">{entry.entry_type}</span>
                  <span class="text-xs text-mono">{entry.estimated_tokens} tok</span>
                </div>
              {/each}
            </div>
          {/if}
        {/if}
      </div>

      <div class="diag-section">
        <div class="section-header">
          <span class="section-title">Nudge Queue Policy</span>
        </div>
        {#if nudgeQueuePolicyError && nudgeQueueStatusError}
          <div class="text-xs text-muted">{nudgeQueuePolicyError}</div>
        {:else}
          <div class="diag-kv">
            <div><span class="text-muted text-xs">Cap</span><span class="text-mono">{nudgeQueuePolicy?.cap ?? nudgeQueueStatus?.cap ?? '---'}</span></div>
            <div><span class="text-muted text-xs">Drop</span><span class="text-mono">{nudgeQueuePolicy?.drop_policy ?? nudgeQueueStatus?.drop_policy ?? '---'}</span></div>
            <div><span class="text-muted text-xs">Debounce</span><span class="text-mono">{nudgeQueuePolicy?.debounce_ms ?? nudgeQueueStatus?.debounce_ms ?? '---'}ms</span></div>
          </div>
          {#if nudgeQueueStatus?.by_lane && Object.keys(nudgeQueueStatus.by_lane).length > 0}
            <div class="diag-subtitle">Pending by Lane</div>
            <div class="diag-lanes">
              {#each Object.entries(nudgeQueueStatus.by_lane) as [lane, count]}
                <span class="lane-chip">
                  <span class="text-mono">{lane}</span>
                  <span class="text-mono">{count}</span>
                </span>
              {/each}
            </div>
          {:else}
            <div class="text-xs text-muted">No queued nudges for this agent.</div>
          {/if}
          {#if (nudgeQueuePolicy?.lane_priority ?? []).length > 0}
            <div class="diag-subtitle">Lane Priority</div>
            <div class="diag-lanes">
              {#each nudgeQueuePolicy.lane_priority as lane}
                <span class="lane-chip lane-priority-chip"><span class="text-mono">{lane}</span></span>
              {/each}
            </div>
          {/if}

          <div class="diag-subtitle">Runtime Controls</div>
          <form
            class="diag-policy-form"
            onsubmit={(e) => {
              e.preventDefault();
              updateNudgePolicy();
            }}
          >
            <div class="diag-policy-grid">
              <div class="form-group diag-form-group">
                <label class="form-label" for="diag-policy-cap">Cap</label>
                <input
                  id="diag-policy-cap"
                  class="form-input"
                  type="number"
                  min="1"
                  step="1"
                  bind:value={nudgePolicyCapInput}
                  oninput={markNudgePolicyDirty}
                />
              </div>
              <div class="form-group diag-form-group">
                <label class="form-label" for="diag-policy-debounce">Debounce (ms)</label>
                <input
                  id="diag-policy-debounce"
                  class="form-input"
                  type="number"
                  min="0"
                  step="1"
                  bind:value={nudgePolicyDebounceInput}
                  oninput={markNudgePolicyDirty}
                />
              </div>
              <div class="form-group diag-form-group">
                <label class="form-label" for="diag-policy-drop">Drop Policy</label>
                <select
                  id="diag-policy-drop"
                  class="form-input"
                  bind:value={nudgePolicyDropPolicy}
                  onchange={markNudgePolicyDirty}
                >
                  <option value="drop_old">drop_old</option>
                  <option value="drop_new">drop_new</option>
                  <option value="summarize">summarize</option>
                </select>
              </div>
              <div class="form-group diag-form-group">
                <label class="form-label" for="diag-policy-updated-by">Updated By</label>
                <input
                  id="diag-policy-updated-by"
                  class="form-input"
                  type="text"
                  placeholder="hud-ui"
                  bind:value={nudgePolicyUpdatedBy}
                  oninput={markNudgePolicyDirty}
                />
              </div>
            </div>

            <div class="form-group diag-form-group">
              <label class="form-label" for="diag-policy-lanes">Lane Priority (comma-separated)</label>
              <input
                id="diag-policy-lanes"
                class="form-input text-mono"
                type="text"
                placeholder="control, handoff, advice, default"
                bind:value={nudgePolicyLanePriorityInput}
                oninput={markNudgePolicyDirty}
              />
            </div>

            <div class="form-group diag-form-group">
              <label class="form-label" for="diag-policy-token">Admin Token</label>
              <input
                id="diag-policy-token"
                class="form-input text-mono"
                type="password"
                autocomplete="off"
                placeholder="Required for policy updates"
                bind:value={nudgePolicyAdminToken}
              />
            </div>

            <div class="diag-policy-actions">
              <button class="btn btn-sm btn-ghost" type="button" onclick={resetNudgePolicyForm} disabled={nudgePolicyUpdating}>
                Reset
              </button>
              <button class="btn btn-sm btn-primary" type="submit" disabled={nudgePolicyUpdating}>
                {nudgePolicyUpdating ? 'Updating...' : 'Apply Policy'}
              </button>
            </div>

            {#if nudgePolicyMutationError}
              <div class="text-xs diagnostics-error">{nudgePolicyMutationError}</div>
            {/if}
          </form>
        {/if}
      </div>
    </div>
  {/if}
</div>

<style>
  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
    border: 0;
  }

  .diagnostics-card {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  .diagnostics-header {
    align-items: center;
    gap: 8px;
  }

  .diagnostics-controls {
    margin-left: auto;
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .diagnostics-select {
    min-width: 220px;
    max-width: 320px;
  }

  .diagnostics-error {
    padding: 0 4px;
    color: var(--error);
  }

  .diagnostics-metrics {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 10px;
  }

  .stat-card {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--border-radius);
    padding: 14px 16px;
    border-left: 3px solid var(--accent-color, var(--info));
    display: flex;
    flex-direction: column;
    justify-content: center;
  }

  .stat-card .metric-value {
    font-size: 22px;
    font-weight: 700;
    font-family: var(--font-mono);
    color: var(--fg-primary);
    line-height: 1.1;
  }

  .stat-card .metric-label {
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    color: var(--fg-muted);
    margin-top: 4px;
  }

  .diagnostics-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 12px;
  }

  .diag-section {
    border: 1px solid var(--border);
    border-radius: var(--border-radius);
    background: var(--bg-secondary);
    padding: 10px 12px;
    display: flex;
    flex-direction: column;
    gap: 8px;
    min-height: 220px;
  }

  .section-header {
    margin-bottom: 8px;
  }

  .section-title {
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    color: var(--fg-muted);
  }

  .diag-list {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .diag-row {
    display: grid;
    grid-template-columns: 1fr auto auto;
    gap: 8px;
    align-items: center;
    padding: 6px 8px;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--bg-tertiary);
    font-size: 12px;
  }

  .diag-subtitle {
    font-size: 11px;
    color: var(--fg-muted);
    text-transform: uppercase;
    letter-spacing: 0.4px;
    margin-top: 4px;
  }

  .diag-kv {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 8px;
  }

  .diag-kv > div {
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: 6px 8px;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--bg-tertiary);
    font-size: 12px;
  }

  .diag-lanes {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }

  .lane-chip {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 3px 7px;
    border-radius: var(--radius-md);
    background: var(--bg-tertiary);
    border: 1px solid var(--border);
    font-size: 11px;
  }

  .lane-priority-chip {
    border-color: rgba(129, 240, 254, 0.3);
  }

  .diag-policy-form {
    margin-top: 2px;
    padding-top: 8px;
    border-top: 1px dashed var(--border);
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .diag-policy-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 8px;
  }

  .diag-form-group {
    margin-bottom: 0;
  }

  .diag-policy-actions {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    margin-top: 2px;
  }

  @media (max-width: 1180px) {
    .diagnostics-metrics {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }

    .diagnostics-grid {
      grid-template-columns: 1fr;
    }
  }

  @media (max-width: 760px) {
    .diagnostics-controls {
      width: 100%;
      margin-left: 0;
      flex-direction: column;
      align-items: stretch;
    }

    .diagnostics-select {
      min-width: 0;
      max-width: none;
    }

    .diag-policy-grid {
      grid-template-columns: 1fr;
    }
  }
</style>
