<script>
  import { presenceDiagnosticsStore } from '../../stores/presenceDiagnostics.svelte.ts';
  import EmptyState from '../shared/EmptyState.svelte';

  let { agents = [] } = $props();

  $effect(() => {
    presenceDiagnosticsStore.syncAgents(agents);
  });

  $effect(() => {
    presenceDiagnosticsStore.startPolling(10000);
    return () => {
      presenceDiagnosticsStore.stopPolling();
    };
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
        bind:value={presenceDiagnosticsStore.diagnosticsAgentId}
        onchange={() => {
          presenceDiagnosticsStore.fetchDiagnostics();
        }}
      >
        <option value="">Select agent...</option>
        {#each agents as agent (agent.agent_id)}
          <option value={agent.agent_id}>{agent.agent_id}</option>
        {/each}
      </select>
      <button class="btn btn-sm" onclick={() => {
        presenceDiagnosticsStore.fetchDiagnostics();
      }} disabled={presenceDiagnosticsStore.diagnosticsLoading || !presenceDiagnosticsStore.diagnosticsAgentId}>
        {presenceDiagnosticsStore.diagnosticsLoading ? 'Refreshing...' : 'Refresh'}
      </button>
    </div>
  </div>

  {#if presenceDiagnosticsStore.diagnosticsError}
    <div class="text-xs text-muted diagnostics-error">{presenceDiagnosticsStore.diagnosticsError}</div>
  {/if}

  {#if !presenceDiagnosticsStore.diagnosticsAgentId}
    <EmptyState icon={'\u2699'} heading="Select an agent to inspect diagnostics" compact />
  {:else}
    <div class="diagnostics-metrics">
      <div class="stat-card" style="--accent-color: var(--accent)">
        <div class="metric-value">{presenceDiagnosticsStore.contextInspect?.estimated_tokens ?? '---'}</div>
        <div class="metric-label">Prompt Est. Tokens</div>
      </div>
      <div class="stat-card" style="--accent-color: var(--info)">
        <div class="metric-value">{presenceDiagnosticsStore.contextInspect?.entry_count ?? '---'}</div>
        <div class="metric-label">Context Entries</div>
      </div>
      <div class="stat-card" style="--accent-color: var(--warning)">
        <div class="metric-value">{presenceDiagnosticsStore.nudgeQueueStatus?.pending ?? '---'}</div>
        <div class="metric-label">Queue Pending</div>
      </div>
      <div class="stat-card" style="--accent-color: var(--error)">
        <div class="metric-value">{presenceDiagnosticsStore.nudgeQueueStatus?.dropped ?? '---'}</div>
        <div class="metric-label">Queue Dropped</div>
      </div>
    </div>

    <div class="diagnostics-grid">
      <div class="diag-section">
        <div class="section-header">
          <span class="section-title">Context Breakdown</span>
          {#if presenceDiagnosticsStore.contextInspect?.session_id}
            <span class="text-mono text-xs text-muted">{presenceDiagnosticsStore.contextInspect.session_id}</span>
          {/if}
        </div>
        {#if presenceDiagnosticsStore.contextInspectError}
          <div class="text-xs text-muted">{presenceDiagnosticsStore.contextInspectError}</div>
        {:else if !presenceDiagnosticsStore.contextInspect}
          <div class="text-xs text-muted">No context diagnostics available.</div>
        {:else}
          <div class="diag-list">
            {#each (presenceDiagnosticsStore.contextInspect.by_entry_type ?? []) as bucket}
              <div class="diag-row">
                <span class="text-mono">{bucket.entry_type}</span>
                <span class="text-xs text-muted">{bucket.count} entries</span>
                <span class="text-xs text-mono">{bucket.estimated_tokens} tok</span>
              </div>
            {/each}
          </div>
          {#if (presenceDiagnosticsStore.contextInspect.sections ?? []).length > 0}
            <div class="diag-subtitle">Prompt Sections</div>
            <div class="diag-list">
              {#each (presenceDiagnosticsStore.contextInspect.sections ?? []) as section}
                <div class="diag-row">
                  <span class="text-mono">{section.section}</span>
                  <span class="text-xs text-muted">{section.source}</span>
                  <span class="text-xs text-mono">{section.estimated_tokens} tok</span>
                </div>
              {/each}
            </div>
          {/if}
          {#if (presenceDiagnosticsStore.contextInspect.top_entries ?? []).length > 0}
            <div class="diag-subtitle">Top Entries</div>
            <div class="diag-list">
              {#each (presenceDiagnosticsStore.contextInspect.top_entries ?? []).slice(0, 5) as entry}
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
        {#if presenceDiagnosticsStore.nudgeQueuePolicyError && presenceDiagnosticsStore.nudgeQueueStatusError}
          <div class="text-xs text-muted">{presenceDiagnosticsStore.nudgeQueuePolicyError}</div>
        {:else}
          <div class="diag-kv">
            <div><span class="text-muted text-xs">Cap</span><span class="text-mono">{presenceDiagnosticsStore.nudgeQueuePolicy?.cap ?? presenceDiagnosticsStore.nudgeQueueStatus?.cap ?? '---'}</span></div>
            <div><span class="text-muted text-xs">Drop</span><span class="text-mono">{presenceDiagnosticsStore.nudgeQueuePolicy?.drop_policy ?? presenceDiagnosticsStore.nudgeQueueStatus?.drop_policy ?? '---'}</span></div>
            <div><span class="text-muted text-xs">Debounce</span><span class="text-mono">{presenceDiagnosticsStore.nudgeQueuePolicy?.debounce_ms ?? presenceDiagnosticsStore.nudgeQueueStatus?.debounce_ms ?? '---'}ms</span></div>
          </div>
          {#if presenceDiagnosticsStore.nudgeQueueStatus?.by_lane && Object.keys(presenceDiagnosticsStore.nudgeQueueStatus.by_lane).length > 0}
            <div class="diag-subtitle">Pending by Lane</div>
            <div class="diag-lanes">
              {#each Object.entries(presenceDiagnosticsStore.nudgeQueueStatus.by_lane) as [lane, count]}
                <span class="lane-chip">
                  <span class="text-mono">{lane}</span>
                  <span class="text-mono">{count}</span>
                </span>
              {/each}
            </div>
          {:else}
            <div class="text-xs text-muted">No queued nudges for this agent.</div>
          {/if}
          {#if (presenceDiagnosticsStore.nudgeQueuePolicy?.lane_priority ?? []).length > 0}
            <div class="diag-subtitle">Lane Priority</div>
            <div class="diag-lanes">
              {#each presenceDiagnosticsStore.nudgeQueuePolicy.lane_priority as lane}
                <span class="lane-chip lane-priority-chip"><span class="text-mono">{lane}</span></span>
              {/each}
            </div>
          {/if}

          <div class="diag-subtitle">Runtime Controls</div>
          <form
            class="diag-policy-form"
            onsubmit={(e) => {
              e.preventDefault();
              presenceDiagnosticsStore.updateNudgePolicy();
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
                  bind:value={presenceDiagnosticsStore.nudgePolicyCapInput}
                  oninput={() => presenceDiagnosticsStore.markNudgePolicyDirty()}
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
                  bind:value={presenceDiagnosticsStore.nudgePolicyDebounceInput}
                  oninput={() => presenceDiagnosticsStore.markNudgePolicyDirty()}
                />
              </div>
              <div class="form-group diag-form-group">
                <label class="form-label" for="diag-policy-drop">Drop Policy</label>
                <select
                  id="diag-policy-drop"
                  class="form-input"
                  bind:value={presenceDiagnosticsStore.nudgePolicyDropPolicy}
                  onchange={() => presenceDiagnosticsStore.markNudgePolicyDirty()}
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
                  bind:value={presenceDiagnosticsStore.nudgePolicyUpdatedBy}
                  oninput={() => presenceDiagnosticsStore.markNudgePolicyDirty()}
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
                bind:value={presenceDiagnosticsStore.nudgePolicyLanePriorityInput}
                oninput={() => presenceDiagnosticsStore.markNudgePolicyDirty()}
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
                bind:value={presenceDiagnosticsStore.nudgePolicyAdminToken}
              />
            </div>

            <div class="diag-policy-actions">
              <button class="btn btn-sm btn-ghost" type="button" onclick={() => presenceDiagnosticsStore.resetNudgePolicyForm()} disabled={presenceDiagnosticsStore.nudgePolicyUpdating}>
                Reset
              </button>
              <button class="btn btn-sm btn-primary" type="submit" disabled={presenceDiagnosticsStore.nudgePolicyUpdating}>
                {presenceDiagnosticsStore.nudgePolicyUpdating ? 'Updating...' : 'Apply Policy'}
              </button>
            </div>

            {#if presenceDiagnosticsStore.nudgePolicyMutationError}
              <div class="text-xs diagnostics-error">{presenceDiagnosticsStore.nudgePolicyMutationError}</div>
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
