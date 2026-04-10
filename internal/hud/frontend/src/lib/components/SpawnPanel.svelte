<script lang="ts">
  import { spawnStore } from '../stores/spawn.svelte';
  import type { SpawnRequest, SpawnState, SpawnTelemetry } from '../stores/spawn.svelte';
  import { labsAuthStore } from '../stores/labsAuth.svelte.ts';
  import { router } from '../stores/router.svelte';
  import StatusDot from '../widgets/StatusDot.svelte';
  import BudgetBar from '../widgets/BudgetBar.svelte';
  import EmptyState from './shared/EmptyState.svelte';
  import LabsAccessBar from './shared/LabsAccessBar.svelte';
  import ConfirmDialog from './shared/ConfirmDialog.svelte';

  $effect(() => {
    spawnStore.startPolling(10000);
    return () => { spawnStore.stopPolling(); };
  });

  let spawns = $derived(spawnStore.spawns);
  let activeCount = $derived(spawnStore.activeSpawns.length);
  let completedCount = $derived(spawnStore.completedSpawns.length);
  let loading = $derived(spawnStore.loading);
  let spawning = $derived(spawnStore.spawning);
  let error = $derived(spawnStore.error);
  let failedCount = $derived(spawnStore.spawns.filter((spawn) => spawn.status === 'failed').length);
  let config = $derived(spawnStore.config);
  let configLoading = $derived(spawnStore.configLoading);
  let configError = $derived(spawnStore.configError);
  let hasAdminToken = $derived(labsAuthStore.hasToken);

  // Filter state for spawn list
  let statusFilter = $state('all');
  let searchQuery = $state('');
  let filteredSpawns = $derived.by(() => {
    let result = spawns;
    if (statusFilter === 'active') {
      result = result.filter((s: SpawnState) => s.status === 'creating' || s.status === 'building' || s.status === 'running');
    } else if (statusFilter === 'completed') {
      result = result.filter((s: SpawnState) => s.status === 'completed');
    } else if (statusFilter === 'failed') {
      result = result.filter((s: SpawnState) => s.status === 'failed' || s.status === 'stopped');
    }
    const q = searchQuery.trim().toLowerCase();
    if (q) {
      result = result.filter((s: SpawnState) =>
        s.request.project.toLowerCase().includes(q) ||
        s.request.task_description.toLowerCase().includes(q) ||
        s.agent_id.toLowerCase().includes(q) ||
        s.request.agent_type.toLowerCase().includes(q)
      );
    }
    return result;
  });

  // Form state
  let agentType = $state('claude-code');
  let project = $state('');
  let branch = $state('');
  let taskDescription = $state('');
  let timeoutMinutes = $state(60);
  let multiTurn = $state(false);
  let formReady = $derived(Boolean(project.trim()) && Boolean(taskDescription.trim()));
  let defaultsApplied = $state(false);

  $effect(() => {
    if (defaultsApplied || !config?.defaults) return;
    agentType = config.defaults.agent_type || agentType;
    timeoutMinutes = config.defaults.timeout_minutes || timeoutMinutes;
    if (!branch && config.defaults.base_branch) {
      branch = config.defaults.base_branch;
    }
    if (!project && config.projects.length === 1) {
      project = config.projects[0].name;
    }
    defaultsApplied = true;
  });

  async function handleSpawn() {
    const projectName = project.trim();
    const task = taskDescription.trim();
    if (!projectName || !task) return;
    const req: SpawnRequest = {
      agent_type: agentType,
      project: projectName,
      task_description: task,
      timeout_minutes: timeoutMinutes,
    };
    if (branch) req.branch = branch;
    if (multiTurn) req.multi_turn = true;
    const result = await spawnStore.spawn(req);
    if (result) {
      taskDescription = '';
      multiTurn = false;
    }
  }

  let stopConfirmId = $state<string | null>(null);

  async function handleStop(spawnId: string) {
    await spawnStore.stop(spawnId);
  }

  function statusColor(status: string): string {
    switch (status) {
      case 'running': return 'var(--color-success, #22c55e)';
      case 'building': return 'var(--color-info, #60a5fa)';
      case 'creating': return 'var(--color-info, #3b82f6)';
      case 'completed': return 'var(--color-muted, #6b7280)';
      case 'failed': return 'var(--color-error, #ef4444)';
      case 'stopped': return 'var(--color-warn, #f59e0b)';
      default: return 'var(--color-muted, #6b7280)';
    }
  }

  function formatDuration(startedAt: string, endedAt?: string): string {
    const start = new Date(startedAt).getTime();
    const end = endedAt ? new Date(endedAt).getTime() : Date.now();
    const seconds = Math.floor((end - start) / 1000);
    const m = Math.floor(seconds / 60);
    const s = seconds % 60;
    if (m > 0) return `${m}m ${s}s`;
    return `${s}s`;
  }

  // rowTelemetry returns the best-known telemetry for a list row:
  //   1. Live snapshot from spawnStore.telemetryBySpawnId (active spawns).
  //   2. Embedded telemetry on SpawnState (completed/failed/stopped spawns).
  function rowTelemetry(s: SpawnState): SpawnTelemetry | undefined {
    const live = spawnStore.telemetryBySpawnId.get(s.spawn_id);
    if (live) return live;
    return s.telemetry ?? undefined;
  }

  function hasBudget(s: SpawnState): boolean {
    return Boolean(s.request.max_cost_usd || s.request.max_turns);
  }

  function formatCostShort(usd: number): string {
    return `$${usd.toFixed(4)}`;
  }

  function formatTurns(n: number): string {
    return Number.isFinite(n) ? String(Math.floor(n)) : '0';
  }
</script>

<div class="panel spawn-panel">
  <section class="spawn-layout">
    <div class="spawn-form">
      <div class="form-header">
        <div class="form-heading">
          <div class="form-eyebrow">Labs / Spawn</div>
          <h1 class="form-title">Launch an agent with enough context to be useful</h1>
          <p class="form-description">
            Pick the worker, aim it at a project, and give it a concrete outcome. Active runs stay visible below with live status and stop controls.
          </p>
        </div>

        <div class="form-summary">
          <div class="summary-pill">
            <span class="summary-label">Active</span>
            <strong>{activeCount}</strong>
          </div>
          <div class="summary-pill">
            <span class="summary-label">Completed</span>
            <strong>{completedCount}</strong>
          </div>
          <div class="summary-pill">
            <span class="summary-label">Failed</span>
            <strong>{failedCount}</strong>
          </div>
        </div>
      </div>

      <LabsAccessBar />

      <div class="readiness-strip">
        <span class="readiness-chip" class:ready={project.trim().length > 0}>Project</span>
        <span class="readiness-chip" class:ready={taskDescription.trim().length > 0}>Task</span>
        <span class="readiness-chip" class:ready={timeoutMinutes >= 5}>Timeout</span>
        <span class="readiness-chip" class:ready={hasAdminToken}>Token</span>
        <span class="readiness-chip" class:ready={config?.notes?.follow_up_supported}>Follow-up</span>
        <span class="readiness-hint">
          {#if configLoading}
            Loading spawn config…
          {:else if !hasAdminToken}
            Admin token required for spawn control
          {:else if config && !config.configured}
            {config.notes?.reason || 'Spawn orchestrator unavailable'}
          {:else if formReady}
            Ready to launch
          {:else}
            Project and task are required
          {/if}
        </span>
      </div>

      {#if config && !config.configured}
        <div class="integration-banner">
          {config.notes?.reason || 'Spawn orchestration is not configured on this HUD instance yet.'}
          {#if config.notes?.hint}
            {` ${config.notes.hint}`}
          {/if}
        </div>
      {:else if configError}
        <div class="integration-banner integration-banner-muted">
          Could not load spawn configuration: {configError}
        </div>
      {/if}

      <div class="form-grid">
        <label class="form-label">
          <span class="label-top">Agent</span>
          <select bind:value={agentType} class="form-select">
            {#if config?.agent_types?.length}
              {#each config.agent_types as option (option.id)}
                <option value={option.id} disabled={!option.available}>{option.name}</option>
              {/each}
            {:else}
              <option value="claude-code">Claude Code</option>
              <option value="codex">Codex</option>
              <option value="gemini">Gemini</option>
            {/if}
          </select>
          <span class="form-helper">Choose the worker best suited to the task.</span>
        </label>

        <label class="form-label">
          <span class="label-top">Project</span>
          <input bind:value={project} type="text" class="form-input" placeholder="loom-core" />
          <span class="form-helper">Repo or project slug used by the spawn backend.</span>
          {#if config?.projects?.length}
            <div class="project-suggestions">
              {#each config.projects.slice(0, 6) as option (option.name)}
                <button type="button" class="suggestion-chip" onclick={() => { project = option.name; }}>
                  {option.name}
                </button>
              {/each}
            </div>
          {/if}
        </label>

        <label class="form-label">
          <span class="label-top">Branch</span>
          <input bind:value={branch} type="text" class="form-input" placeholder="main or feature/spawn-polish" />
          <span class="form-helper">Optional. Leave blank to use the default branch.</span>
        </label>

        <label class="form-label">
          <span class="label-top">Timeout</span>
          <input bind:value={timeoutMinutes} type="number" class="form-input" min="5" max="480" />
          <span class="form-helper">Minutes before the run is cancelled automatically.</span>
        </label>
      </div>

      <label class="form-label form-task">
        <span class="label-top">Task</span>
        <textarea
          bind:value={taskDescription}
          class="form-textarea"
          rows="5"
          placeholder="Describe the expected outcome, constraints, and any files or subsystems the agent should focus on."
        ></textarea>
        <span class="form-helper">Be specific about the outcome. Good prompts reduce retries and dead-end runs.</span>
      </label>

      <label class="form-label-inline">
        <input type="checkbox" bind:checked={multiTurn} />
        <span>Allow follow-up messages so the agent can ask for clarification mid-run.</span>
      </label>

      {#if error}
        <div class="form-error">
          <span>{error}</span>
          <button type="button" class="error-dismiss" onclick={() => spawnStore.clearError()}>Dismiss</button>
        </div>
      {/if}

      <div class="form-footer">
        <button onclick={handleSpawn} class="spawn-button" disabled={!formReady || !hasAdminToken || spawning || config?.configured === false}>
          {spawning ? 'Spawning agent...' : 'Spawn Agent'}
        </button>
        <div class="form-footer-note">
          {#if !hasAdminToken}
            Add the Labs token above to enable protected spawn actions.
          {:else if formReady}
            The new run will appear immediately in Recent spawns.
          {:else}
            Fill in project and task to enable launch.
          {/if}
        </div>
      </div>
    </div>

    <aside class="spawn-sidecar">
      <div class="side-card">
        <div class="side-label">Ready Check</div>
        <div class="side-value">
          {#if !hasAdminToken}
            Token required
          {:else if config && !config.configured}
            Backend offline
          {:else if formReady}
            Launch-ready
          {:else}
            Missing context
          {/if}
        </div>
        <div class="side-copy">
          Strong spawns include a target project, an explicit outcome, and a clear timeout window.
        </div>
      </div>

      <div class="side-card">
        <div class="side-label">Backend Integration</div>
        <div class="side-copy">
          {#if config?.projects?.length}
            {config.notes?.project_count ?? config.projects.length} project target{(config.notes?.project_count ?? config.projects.length) === 1 ? '' : 's'} advertised by the backend.
          {:else if config && !config.configured}
            {config.notes?.reason || 'The HUD cannot currently reach a spawn orchestrator.'}
          {:else}
            Waiting for backend capability data.
          {/if}
        </div>
        <div class="tip-list">
          <div class="tip-item">Backend defaults hydrate the form when configuration loads.</div>
          <div class="tip-item">Live cost and token telemetry stream in over SSE instead of waiting for the next poll.</div>
          {#if config?.notes?.active_spawn_count}
            <div class="tip-item">{config.notes.active_spawn_count} active spawn{config.notes.active_spawn_count === 1 ? '' : 's'} currently tracked by the backend.</div>
          {/if}
          {#if config?.notes?.telemetry_requires_auth}
            <div class="tip-item">Protected telemetry and follow-up controls stay behind the Labs admin token.</div>
          {/if}
        </div>
      </div>

      <div class="side-card">
        <div class="side-label">Good Prompt Pattern</div>
        <div class="tip-list">
          <div class="tip-item">Name the subsystem or file area the agent should inspect first.</div>
          <div class="tip-item">State the expected result, not just the problem statement.</div>
          <div class="tip-item">Mention constraints like tests, branches, or services to avoid touching.</div>
        </div>
      </div>
    </aside>
  </section>

  <section class="spawn-results">
    <div class="results-header">
      <div>
        <div class="results-eyebrow">Recent activity</div>
        <h2 class="results-title">Recent spawns</h2>
        <p class="results-description">Runs stay here until they finish, fail, or are stopped.</p>
      </div>
      <div class="results-count">{filteredSpawns.length === spawns.length ? `${spawns.length} tracked` : `${filteredSpawns.length} / ${spawns.length}`}</div>
    </div>

    {#if spawns.length > 0}
      <div class="filter-bar">
        <div class="filter-chips">
          <button type="button" class="filter-chip" class:active={statusFilter === 'all'} onclick={() => (statusFilter = 'all')}>All ({spawns.length})</button>
          <button type="button" class="filter-chip" class:active={statusFilter === 'active'} onclick={() => (statusFilter = 'active')}>Active ({activeCount})</button>
          <button type="button" class="filter-chip" class:active={statusFilter === 'completed'} onclick={() => (statusFilter = 'completed')}>Completed ({completedCount})</button>
          <button type="button" class="filter-chip" class:active={statusFilter === 'failed'} onclick={() => (statusFilter = 'failed')}>Failed ({failedCount})</button>
        </div>
        <input
          type="text"
          class="filter-search"
          placeholder="Search project, task, agent..."
          bind:value={searchQuery}
        />
      </div>
    {/if}

    {#if spawns.length === 0}
      <div class="spawn-empty">
        <EmptyState
          icon={'\u{1F916}'}
          heading="No agent spawns yet"
          description="The first run will show live status, timing, budget usage, and a stop control here. Use the composer above to launch a scoped task."
        />
      </div>
    {:else if filteredSpawns.length === 0}
      <div class="spawn-empty">
        <EmptyState
          icon={'\u{1F50D}'}
          heading="No matching spawns"
          description="Try adjusting your filters or search query."
        />
      </div>
    {:else}
      <div class="spawns-list">
        {#each filteredSpawns as spawn (spawn.spawn_id)}
          <div
            class="spawn-row"
            class:active={spawn.status === 'running' || spawn.status === 'creating' || spawn.status === 'building'}
            role="button"
            tabindex="0"
            onclick={() => router.navigateDetail(spawn.spawn_id)}
            onkeydown={(e) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault();
                router.navigateDetail(spawn.spawn_id);
              }
            }}
          >
            <div class="spawn-header">
              <div class="spawn-head-main">
                <StatusDot status={spawn.status === 'running' ? 'healthy' : spawn.status === 'creating' || spawn.status === 'building' ? 'degraded' : spawn.status === 'failed' ? 'down' : 'idle'} />
                <span class="spawn-project">{spawn.request.project}</span>
                <span class="spawn-status" style="color: {statusColor(spawn.status)}">{spawn.status}</span>
              </div>

              <div class="spawn-head-actions">
                <span class="spawn-duration">{formatDuration(spawn.started_at, spawn.ended_at)}</span>
                {#if spawn.status === 'running' || spawn.status === 'creating' || spawn.status === 'building'}
                  <button
                    class="stop-button"
                    disabled={!hasAdminToken}
                    onclick={(e) => {
                      e.stopPropagation();
                      stopConfirmId = spawn.spawn_id;
                    }}
                  >Stop</button>
                {/if}
              </div>
            </div>

            {#if hasBudget(spawn)}
              {@const rt = rowTelemetry(spawn)}
              <div class="spawn-budgets">
                {#if spawn.request.max_cost_usd}
                  <BudgetBar
                    label="Cost"
                    current={rt?.total_cost_usd ?? 0}
                    max={spawn.request.max_cost_usd}
                    formatValue={formatCostShort}
                    costEstimated={rt?.cost_estimated ?? false}
                  />
                {/if}
                {#if spawn.request.max_turns}
                  <BudgetBar
                    label="Turns"
                    current={rt?.turn_count ?? 0}
                    max={spawn.request.max_turns}
                    formatValue={formatTurns}
                  />
                {/if}
              </div>
            {/if}

            <div class="spawn-task">{spawn.request.task_description}</div>
            <div class="spawn-meta">
              <span class="spawn-agent-type">{spawn.request.agent_type}</span>
              <span class="spawn-agent-id">{spawn.agent_id}</span>
              {#if spawn.error}
                <span class="spawn-error">{spawn.error}</span>
              {/if}
            </div>
          </div>
        {/each}
      </div>
    {/if}
  </section>

  <ConfirmDialog
    open={stopConfirmId !== null}
    title="Stop spawn?"
    message="This will terminate the running agent. The spawn cannot be resumed after stopping."
    confirmLabel="Stop"
    variant="danger"
    onConfirm={() => { const id = stopConfirmId; stopConfirmId = null; if (id) handleStop(id); }}
    onCancel={() => (stopConfirmId = null)}
  />
</div>

<style>
  .spawn-panel {
    display: flex;
    flex-direction: column;
    gap: var(--space-5);
  }

  .spawn-layout {
    display: grid;
    grid-template-columns: minmax(0, 1.65fr) minmax(280px, 0.9fr);
    gap: var(--space-4);
    align-items: start;
  }

  .spawn-form,
  .spawn-sidecar,
  .spawn-results {
    min-width: 0;
  }

  .spawn-form {
    display: flex;
    flex-direction: column;
    gap: var(--space-4);
    padding: clamp(18px, 2vw, 28px);
    background:
      radial-gradient(circle at top right, rgba(255, 107, 53, 0.08), transparent 26%),
      linear-gradient(180deg, rgba(255, 255, 255, 0.025), transparent 30%),
      var(--bg-secondary);
    border-radius: var(--radius-xl);
    border: 1px solid color-mix(in srgb, var(--accent) 18%, var(--border));
    position: relative;
    box-shadow: var(--shadow-sm);
  }

  .spawn-form::before {
    content: '';
    position: absolute;
    inset: 0;
    border-radius: inherit;
    background: var(--surface-highlight-accent);
    pointer-events: none;
  }

  .form-header {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    gap: var(--space-4);
    position: relative;
    z-index: 1;
  }

  .form-header::after {
    content: '';
    position: absolute;
    bottom: -12px;
    left: 0;
    right: 0;
    height: 1px;
    background: linear-gradient(90deg, transparent, rgba(255, 107, 53, 0.12) 25%, rgba(255, 107, 53, 0.08) 75%, transparent);
    pointer-events: none;
  }

  .form-heading {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    max-width: 52rem;
  }

  .form-eyebrow {
    font-size: var(--text-xs);
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--accent);
  }

  .form-title {
    font-weight: 700;
    font-size: clamp(24px, 2.8vw, 34px);
    line-height: 1.05;
    color: var(--fg-primary);
    margin: 0;
    max-width: 18ch;
  }

  .form-description {
    font-size: var(--text-base);
    line-height: 1.65;
    color: var(--fg-secondary);
    max-width: 62ch;
  }

  .form-summary {
    display: grid;
    grid-template-columns: repeat(3, minmax(86px, 1fr));
    gap: var(--space-2);
    min-width: min(320px, 100%);
  }

  .summary-pill {
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: var(--space-3);
    border-radius: var(--radius-lg);
    border: 1px solid var(--border);
    background: rgba(255, 255, 255, 0.02);
  }

  .summary-label {
    font-size: var(--text-2xs);
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-muted);
  }

  .summary-pill strong {
    font-size: var(--text-lg);
    font-family: var(--font-mono);
    color: var(--fg-primary);
  }

  .readiness-strip {
    position: relative;
    z-index: 1;
    display: flex;
    align-items: center;
    gap: var(--space-2);
    flex-wrap: wrap;
    padding-top: var(--space-1);
  }

  .readiness-chip {
    display: inline-flex;
    align-items: center;
    padding: 4px 9px;
    border-radius: var(--radius-full);
    border: 1px solid var(--border);
    background: rgba(255, 255, 255, 0.02);
    color: var(--fg-muted);
    font-size: var(--text-xs);
    font-family: var(--font-mono);
  }

  .readiness-chip.ready {
    border-color: rgba(34, 224, 118, 0.3);
    color: var(--success);
    background: rgba(34, 224, 118, 0.08);
  }

  .readiness-hint {
    margin-left: auto;
    font-size: var(--text-xs);
    color: var(--fg-secondary);
    font-family: var(--font-mono);
  }

  .form-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: var(--space-3);
    position: relative;
    z-index: 1;
  }

  .form-label {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    font-size: var(--text-sm);
    color: var(--fg-secondary);
    letter-spacing: var(--tracking-normal);
  }

  .form-task {
    position: relative;
    z-index: 1;
  }

  .label-top {
    font-size: var(--text-xs);
    font-weight: 700;
    letter-spacing: var(--tracking-wide);
    text-transform: uppercase;
    color: var(--fg-muted);
  }

  .form-label-inline {
    display: flex;
    flex-direction: row;
    align-items: center;
    gap: var(--space-2);
    font-size: var(--text-sm);
    color: var(--fg-secondary);
    letter-spacing: var(--tracking-normal);
    padding: var(--space-3);
    border-radius: var(--radius-lg);
    border: 1px solid var(--border-subtle);
    background: rgba(255, 255, 255, 0.02);
    position: relative;
    z-index: 1;
  }

  .form-input, .form-select, .form-textarea {
    padding: 10px 12px;
    background: color-mix(in srgb, var(--bg-primary) 72%, var(--bg-surface));
    border: 1px solid color-mix(in srgb, var(--border) 84%, white 16%);
    border-radius: var(--radius-md);
    color: var(--fg-primary);
    font-size: var(--text-sm);
    font-family: var(--font-mono);
    transition: border-color var(--transition-fast), box-shadow var(--transition-fast), background var(--transition-fast);
  }

  .form-input:focus, .form-select:focus, .form-textarea:focus {
    border-color: var(--border-active);
    background: color-mix(in srgb, var(--bg-primary) 64%, var(--bg-surface));
    box-shadow: 0 0 0 3px rgba(255, 107, 53, 0.08);
  }

  .form-textarea {
    resize: vertical;
    min-height: 8rem;
    line-height: 1.6;
  }

  .form-helper {
    font-size: var(--text-xs);
    color: var(--fg-muted);
    line-height: 1.5;
  }

  .project-suggestions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
  }

  .suggestion-chip {
    display: inline-flex;
    align-items: center;
    padding: 4px 9px;
    border-radius: var(--radius-full);
    border: 1px solid var(--border);
    background: rgba(255, 255, 255, 0.02);
    color: var(--fg-secondary);
    font-size: var(--text-xs);
    font-family: var(--font-mono);
    transition: border-color var(--transition-fast), color var(--transition-fast), background var(--transition-fast);
  }

  .suggestion-chip:hover {
    border-color: color-mix(in srgb, var(--accent) 34%, var(--border));
    color: var(--fg-primary);
    background: rgba(255, 255, 255, 0.04);
  }

  .integration-banner {
    position: relative;
    z-index: 1;
    padding: var(--space-3);
    border-radius: var(--radius-lg);
    border: 1px solid color-mix(in srgb, var(--warning) 26%, var(--border));
    background: color-mix(in srgb, var(--warning) 10%, var(--bg-secondary));
    color: var(--fg-secondary);
    font-size: var(--text-sm);
    line-height: 1.6;
  }

  .integration-banner-muted {
    border-color: color-mix(in srgb, var(--border-focus) 40%, var(--border));
    background: rgba(255, 255, 255, 0.02);
  }

  .form-error {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-2);
    font-size: var(--text-sm);
    color: var(--error);
    padding: var(--space-3);
    border-radius: var(--radius-md);
    border: 1px solid rgba(255, 61, 113, 0.22);
    background: rgba(255, 61, 113, 0.08);
    position: relative;
    z-index: 1;
  }

  .error-dismiss {
    flex-shrink: 0;
    padding: 2px 8px;
    border-radius: var(--radius-xs);
    border: 1px solid rgba(255, 61, 113, 0.3);
    background: transparent;
    color: var(--error);
    font-size: var(--text-xs);
    cursor: pointer;
    transition: background var(--transition-fast);
  }

  .error-dismiss:hover {
    background: rgba(255, 61, 113, 0.12);
  }

  .form-footer {
    display: flex;
    align-items: center;
    gap: var(--space-3);
    flex-wrap: wrap;
    position: relative;
    z-index: 1;
  }

  .spawn-button {
    padding: 12px 18px;
    background: var(--accent);
    color: var(--bg-primary);
    border: none;
    border-radius: var(--radius-md);
    font-size: var(--text-sm);
    font-weight: 600;
    cursor: pointer;
    transition: transform var(--transition-fast), opacity var(--transition-fast), box-shadow var(--transition-fast);
    letter-spacing: var(--tracking-normal);
    min-width: 170px;
  }

  .spawn-button:hover:not(:disabled) {
    opacity: 0.96;
    transform: translateY(-1px);
    box-shadow: 0 12px 28px rgba(255, 107, 53, 0.16), 0 0 10px var(--glow-accent);
  }

  .spawn-button:disabled {
    opacity: 0.45;
    cursor: not-allowed;
  }

  .form-footer-note {
    font-size: var(--text-xs);
    color: var(--fg-secondary);
    font-family: var(--font-mono);
  }

  .spawn-sidecar {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
  }

  .side-card {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    padding: var(--space-4);
    border-radius: var(--radius-xl);
    border: 1px solid var(--border);
    background:
      linear-gradient(180deg, rgba(255, 255, 255, 0.018), transparent 42%),
      var(--bg-secondary);
    box-shadow: var(--shadow-xs);
  }

  .side-label {
    font-size: var(--text-xs);
    font-weight: 700;
    letter-spacing: var(--tracking-wide);
    text-transform: uppercase;
    color: var(--fg-muted);
  }

  .side-value {
    font-size: clamp(18px, 2vw, 24px);
    font-weight: 700;
    color: var(--fg-primary);
  }

  .side-copy {
    font-size: var(--text-sm);
    color: var(--fg-secondary);
    line-height: 1.6;
  }

  .tip-list {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .tip-item {
    padding-left: var(--space-4);
    position: relative;
    font-size: var(--text-sm);
    color: var(--fg-secondary);
    line-height: 1.6;
  }

  .tip-item::before {
    content: '';
    position: absolute;
    top: 9px;
    left: 0;
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--accent);
    box-shadow: 0 0 8px var(--glow-accent);
  }

  .spawn-results {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
  }

  .results-header {
    display: flex;
    justify-content: space-between;
    align-items: end;
    gap: var(--space-3);
  }

  .results-eyebrow {
    font-size: var(--text-xs);
    font-weight: 700;
    letter-spacing: var(--tracking-wide);
    text-transform: uppercase;
    color: var(--fg-muted);
    margin-bottom: 4px;
  }

  .results-title {
    font-size: clamp(20px, 1.8vw, 26px);
    line-height: 1.1;
    margin: 0;
    color: var(--fg-primary);
  }

  .results-description {
    margin-top: 6px;
    font-size: var(--text-sm);
    color: var(--fg-secondary);
  }

  .results-count {
    padding: 6px 10px;
    border-radius: var(--radius-full);
    border: 1px solid var(--border);
    background: rgba(255, 255, 255, 0.02);
    font-size: var(--text-xs);
    color: var(--fg-secondary);
    font-family: var(--font-mono);
    white-space: nowrap;
  }

  .spawn-empty {
    border: 1px dashed color-mix(in srgb, var(--border-focus) 42%, var(--border));
    border-radius: var(--radius-xl);
    background:
      radial-gradient(circle at top, rgba(0, 200, 255, 0.05), transparent 48%),
      color-mix(in srgb, var(--bg-secondary) 84%, transparent);
    padding: var(--space-3);
  }

  .spawns-list {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
    gap: var(--space-3);
  }

  .spawn-row {
    padding: var(--space-4);
    background:
      linear-gradient(180deg, rgba(255, 255, 255, 0.018), transparent 40%),
      var(--bg-secondary);
    border-radius: var(--radius-lg);
    border: 1px solid var(--border);
    position: relative;
    transition: border-color var(--transition-fast), transform var(--transition-fast), box-shadow var(--transition-fast);
    cursor: pointer;
    min-height: 220px;
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
  }

  .spawn-row::before {
    content: '';
    position: absolute;
    inset: 0;
    border-radius: inherit;
    background: var(--surface-highlight);
    pointer-events: none;
  }

  .spawn-row:hover {
    border-color: var(--border-active);
    transform: translateY(-2px);
    box-shadow: 0 16px 36px rgba(0, 0, 0, 0.18);
  }

  .spawn-row:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }

  .spawn-row.active {
    border-color: rgba(34, 224, 118, 0.28);
    box-shadow: 0 0 16px var(--glow-success);
  }

  .spawn-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-3);
  }

  .spawn-head-main,
  .spawn-head-actions {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }

  .spawn-head-main {
    min-width: 0;
  }

  .spawn-budgets {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
  }

  .spawn-project {
    font-weight: 600;
    font-size: var(--text-base);
    color: var(--fg-primary);
    font-family: var(--font-mono);
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .spawn-status {
    font-size: var(--text-xs);
    font-weight: 500;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    padding: 4px 8px;
    border-radius: var(--radius-full);
    background: rgba(255, 255, 255, 0.03);
  }

  .spawn-duration {
    font-size: var(--text-sm);
    color: var(--fg-secondary);
    font-variant-numeric: tabular-nums;
    font-family: var(--font-mono);
    margin-left: auto;
  }

  .stop-button {
    padding: 4px 9px;
    font-size: var(--text-xs);
    background: transparent;
    border: 1px solid rgba(255, 61, 113, 0.2);
    color: var(--error);
    border-radius: var(--radius-xs);
    cursor: pointer;
    transition: background var(--transition-fast), border-color var(--transition-fast);
  }

  .stop-button:hover {
    background: var(--error-dim);
    border-color: var(--error);
  }

  .spawn-task {
    font-size: var(--text-sm);
    color: var(--fg-secondary);
    line-height: 1.65;
    display: -webkit-box;
    -webkit-line-clamp: 4;
    -webkit-box-orient: vertical;
    overflow: hidden;
    letter-spacing: var(--tracking-normal);
  }

  .spawn-meta {
    display: flex;
    gap: var(--space-2);
    font-size: var(--text-xs);
    color: var(--fg-dim);
    margin-top: auto;
    padding-top: var(--space-2);
    border-top: 1px solid var(--border-subtle);
    flex-wrap: wrap;
  }

  .spawn-error {
    color: var(--error);
  }

  .spawn-agent-type {
    padding: 2px 6px;
    border-radius: var(--radius-full);
    border: 1px solid var(--border-subtle);
    background: rgba(255, 255, 255, 0.03);
    font-family: var(--font-mono);
    font-size: var(--text-2xs);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-muted);
  }

  .spawn-agent-id {
    font-family: var(--font-mono);
  }

  .filter-bar {
    display: flex;
    align-items: center;
    gap: var(--space-3);
    flex-wrap: wrap;
  }

  .filter-chips {
    display: flex;
    gap: var(--space-1);
  }

  .filter-chip {
    padding: 5px 10px;
    border-radius: var(--radius-full);
    border: 1px solid var(--border);
    background: transparent;
    color: var(--fg-secondary);
    font-size: var(--text-xs);
    font-family: var(--font-mono);
    cursor: pointer;
    transition: border-color var(--transition-fast), color var(--transition-fast), background var(--transition-fast);
  }

  .filter-chip:hover {
    border-color: var(--border-active);
    color: var(--fg-primary);
  }

  .filter-chip.active {
    border-color: var(--accent);
    color: var(--accent);
    background: rgba(255, 107, 53, 0.08);
  }

  .filter-search {
    flex: 1;
    min-width: 180px;
    padding: 6px 12px;
    border-radius: var(--radius-md);
    border: 1px solid var(--border);
    background: var(--bg-primary);
    color: var(--fg-primary);
    font-size: var(--text-sm);
    font-family: var(--font-mono);
    transition: border-color var(--transition-fast);
  }

  .filter-search:focus {
    outline: none;
    border-color: var(--border-focus);
  }

  .filter-search::placeholder {
    color: var(--fg-dim);
  }

  @media (max-width: 980px) {
    .spawn-layout {
      grid-template-columns: 1fr;
    }

    .form-header {
      flex-direction: column;
    }

    .form-summary {
      width: 100%;
    }
  }

  @media (max-width: 720px) {
    .form-grid,
    .spawns-list {
      grid-template-columns: 1fr;
    }

    .results-header,
    .spawn-header {
      flex-direction: column;
      align-items: start;
    }

    .spawn-head-actions {
      width: 100%;
      justify-content: space-between;
    }

    .readiness-hint {
      margin-left: 0;
      width: 100%;
    }
  }
</style>
