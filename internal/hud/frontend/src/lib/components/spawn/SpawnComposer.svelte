<script lang="ts">
  /**
   * SpawnComposer — the launch form half of the Spawn panel. Owns its
   * own form state (agent type, project, branch, task, budgets, etc.)
   * and dispatches to spawnStore.spawn() on submit.
   */
  import { spawnStore } from '../../stores/spawn.svelte';
  import type { SpawnRequest } from '../../stores/spawn.svelte';
  import { labsAuthStore } from '../../stores/labsAuth.svelte.ts';
  import LabsAccessBar from '../shared/LabsAccessBar.svelte';

  let activeCount = $derived(spawnStore.activeSpawns.length);
  let completedCount = $derived(spawnStore.completedSpawns.length);
  let failedCount = $derived(spawnStore.spawns.filter((spawn) => spawn.status === 'failed').length);
  let spawning = $derived(spawnStore.spawning);
  let error = $derived(spawnStore.error);
  let config = $derived(spawnStore.config);
  let configLoading = $derived(spawnStore.configLoading);
  let configError = $derived(spawnStore.configError);
  let hasAdminToken = $derived(labsAuthStore.hasToken);

  let agentType = $state('claude-code');
  let project = $state('');
  let branch = $state('');
  let taskDescription = $state('');
  let timeoutMinutes = $state(60);
  let maxCostUsd = $state<number | undefined>(undefined);
  let maxTurns = $state<number | undefined>(undefined);
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
    if (maxCostUsd === undefined && config.defaults.max_cost_usd) {
      maxCostUsd = config.defaults.max_cost_usd;
    }
    if (maxTurns === undefined && config.defaults.max_turns) {
      maxTurns = config.defaults.max_turns;
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
    if (maxCostUsd !== undefined && maxCostUsd > 0) req.max_cost_usd = maxCostUsd;
    if (maxTurns !== undefined && maxTurns > 0) req.max_turns = maxTurns;
    const result = await spawnStore.spawn(req);
    if (result) {
      taskDescription = '';
      multiTurn = false;
    }
  }
</script>

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

    <label class="form-label">
      <span class="label-top">Max Cost (USD)</span>
      <input bind:value={maxCostUsd} type="number" class="form-input" min="0" step="0.01" placeholder="No limit" />
      <span class="form-helper">Optional budget cap. The agent stops when this cost is reached.</span>
    </label>

    <label class="form-label">
      <span class="label-top">Max Turns</span>
      <input bind:value={maxTurns} type="number" class="form-input" min="1" step="1" placeholder="No limit" />
      <span class="form-helper">Optional turn limit. One turn equals one model round-trip.</span>
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

<style>
  .spawn-form {
    min-width: 0;
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
    grid-template-columns: 1fr 1fr 1fr;
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

  @media (max-width: 980px) {
    .form-header {
      flex-direction: column;
    }

    .form-summary {
      width: 100%;
    }

    .form-grid {
      grid-template-columns: 1fr 1fr;
    }
  }

  @media (max-width: 720px) {
    .form-grid {
      grid-template-columns: 1fr;
    }

    .readiness-hint {
      margin-left: 0;
      width: 100%;
    }
  }
</style>
