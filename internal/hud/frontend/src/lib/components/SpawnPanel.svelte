<script lang="ts">
  import { spawnStore } from '../stores/spawn.svelte';
  import type { SpawnRequest, SpawnState } from '../stores/spawn.svelte';
  import StatusDot from '../widgets/StatusDot.svelte';
  import EmptyState from './shared/EmptyState.svelte';

  $effect(() => {
    spawnStore.startPolling(10000);
    return () => { spawnStore.stopPolling(); };
  });

  let spawns = $derived(spawnStore.spawns);
  let activeCount = $derived(spawnStore.activeSpawns.length);
  let loading = $derived(spawnStore.loading);
  let spawning = $derived(spawnStore.spawning);
  let error = $derived(spawnStore.error);

  // Form state
  let agentType = $state('claude-code');
  let project = $state('');
  let branch = $state('');
  let taskDescription = $state('');
  let timeoutMinutes = $state(60);

  async function handleSpawn() {
    if (!project || !taskDescription) return;
    const req: SpawnRequest = {
      agent_type: agentType,
      project,
      task_description: taskDescription,
      timeout_minutes: timeoutMinutes,
    };
    if (branch) req.branch = branch;
    const result = await spawnStore.spawn(req);
    if (result) {
      taskDescription = '';
    }
  }

  async function handleStop(spawnId: string) {
    await spawnStore.stop(spawnId);
  }

  function statusColor(status: string): string {
    switch (status) {
      case 'running': return 'var(--color-success, #22c55e)';
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
</script>

<div class="panel spawn-panel">
  <!-- Spawn Form -->
  <div class="spawn-form">
    <div class="form-header">
      <span class="form-title">Spawn Agent</span>
      <span class="active-count">{activeCount} active</span>
    </div>

    <div class="form-row">
      <label class="form-label">
        Agent
        <select bind:value={agentType} class="form-select">
          <option value="claude-code">Claude Code</option>
          <option value="codex" disabled>Codex</option>
          <option value="gemini" disabled>Gemini</option>
        </select>
      </label>
      <label class="form-label">
        Project
        <input bind:value={project} type="text" class="form-input" placeholder="loom-core" />
      </label>
    </div>

    <div class="form-row">
      <label class="form-label">
        Branch
        <input bind:value={branch} type="text" class="form-input" placeholder="main (optional)" />
      </label>
      <label class="form-label">
        Timeout
        <input bind:value={timeoutMinutes} type="number" class="form-input" min="5" max="480" />
      </label>
    </div>

    <label class="form-label full-width">
      Task
      <textarea bind:value={taskDescription} class="form-textarea" rows="3"
        placeholder="Describe the task for the agent..."></textarea>
    </label>

    {#if error}
      <div class="form-error">{error}</div>
    {/if}

    <button onclick={handleSpawn} class="spawn-button"
      disabled={!project || !taskDescription || spawning}>
      {spawning ? 'Spawning...' : 'Spawn Agent'}
    </button>
  </div>

  <!-- Active Spawns -->
  {#if spawns.length === 0}
    <EmptyState icon={'\u{1F916}'} heading="No agent spawns yet" compact />
  {:else}
    <div class="spawns-list">
      {#each spawns as spawn (spawn.spawn_id)}
        <div class="spawn-row" class:active={spawn.status === 'running' || spawn.status === 'creating'}>
          <div class="spawn-header">
            <StatusDot status={spawn.status === 'running' ? 'healthy' : spawn.status === 'creating' ? 'degraded' : spawn.status === 'failed' ? 'down' : 'idle'} />
            <span class="spawn-project">{spawn.request.project}</span>
            <span class="spawn-status" style="color: {statusColor(spawn.status)}">{spawn.status}</span>
            <span class="spawn-duration">{formatDuration(spawn.started_at, spawn.ended_at)}</span>
            {#if spawn.status === 'running' || spawn.status === 'creating'}
              <button class="stop-button" onclick={() => handleStop(spawn.spawn_id)}>Stop</button>
            {/if}
          </div>
          <div class="spawn-task">{spawn.request.task_description}</div>
          <div class="spawn-meta">
            <span class="spawn-agent-id">{spawn.agent_id}</span>
            {#if spawn.error}
              <span class="spawn-error">{spawn.error}</span>
            {/if}
          </div>
        </div>
      {/each}
    </div>
  {/if}
</div>

<style>
  .spawn-panel {
    display: flex;
    flex-direction: column;
    gap: var(--space-4);
  }

  .spawn-form {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    padding: var(--space-3);
    background: var(--bg-secondary);
    border-radius: var(--radius-md);
    border: 1px solid var(--border);
    position: relative;
  }

  .spawn-form::before {
    content: '';
    position: absolute;
    inset: 0;
    border-radius: inherit;
    background: var(--surface-highlight);
    pointer-events: none;
  }

  .form-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    position: relative;
  }

  .form-header::after {
    content: '';
    position: absolute;
    bottom: 0;
    left: 10%;
    right: 10%;
    height: 1px;
    background: linear-gradient(90deg, transparent, rgba(0, 200, 255, 0.06) 50%, transparent);
    pointer-events: none;
  }

  .form-title {
    font-weight: 600;
    font-size: var(--text-base);
    color: var(--fg-primary);
  }

  .active-count {
    font-size: var(--text-sm);
    color: var(--success);
    font-variant-numeric: tabular-nums;
  }

  .form-row {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: var(--space-2);
  }

  .form-label {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
    font-size: var(--text-sm);
    color: var(--fg-secondary);
    letter-spacing: var(--tracking-normal);
  }

  .form-label.full-width {
    grid-column: 1 / -1;
  }

  .form-input, .form-select, .form-textarea {
    padding: 6px var(--space-2);
    background: var(--bg-primary);
    border: 1px solid var(--border);
    border-radius: var(--radius-xs);
    color: var(--fg-primary);
    font-size: var(--text-sm);
    font-family: var(--font-mono);
    transition: border-color var(--transition-fast);
  }

  .form-input:focus, .form-select:focus, .form-textarea:focus {
    border-color: var(--border-active);
  }

  .form-textarea {
    resize: vertical;
    min-height: 3rem;
  }

  .form-error {
    font-size: var(--text-sm);
    color: var(--error);
    padding: var(--space-1) 0;
  }

  .spawn-button {
    padding: var(--space-2) var(--space-4);
    background: var(--accent);
    color: var(--bg-primary);
    border: none;
    border-radius: var(--radius-sm);
    font-size: var(--text-sm);
    font-weight: 600;
    cursor: pointer;
    transition: opacity var(--transition-fast), box-shadow var(--transition-fast);
    letter-spacing: var(--tracking-normal);
  }

  .spawn-button:hover:not(:disabled) {
    opacity: 0.9;
    box-shadow: 0 0 6px var(--glow-accent);
  }

  .spawn-button:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .spawns-list {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .spawn-row {
    padding: var(--space-3);
    background: var(--bg-secondary);
    border-radius: var(--radius-sm);
    border: 1px solid var(--border);
    position: relative;
    transition: border-color var(--transition-fast);
  }

  .spawn-row::before {
    content: '';
    position: absolute;
    inset: 0;
    border-radius: inherit;
    background: var(--surface-highlight);
    pointer-events: none;
  }

  .spawn-row.active {
    border-color: rgba(34, 224, 118, 0.2);
    box-shadow: 0 0 6px var(--glow-success);
  }

  .spawn-header {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    margin-bottom: var(--space-1);
  }

  .spawn-project {
    font-weight: 600;
    font-size: var(--text-sm);
    color: var(--fg-primary);
    font-family: var(--font-mono);
  }

  .spawn-status {
    font-size: var(--text-sm);
    font-weight: 500;
  }

  .spawn-duration {
    font-size: var(--text-sm);
    color: var(--fg-secondary);
    font-variant-numeric: tabular-nums;
    font-family: var(--font-mono);
    margin-left: auto;
  }

  .stop-button {
    padding: 2px var(--space-2);
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
    line-height: 1.4;
    margin-bottom: var(--space-1);
    display: -webkit-box;
    -webkit-line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
    letter-spacing: var(--tracking-normal);
  }

  .spawn-meta {
    display: flex;
    gap: var(--space-2);
    font-size: var(--text-xs);
    color: var(--fg-dim);
  }

  .spawn-error {
    color: var(--error);
  }
</style>
