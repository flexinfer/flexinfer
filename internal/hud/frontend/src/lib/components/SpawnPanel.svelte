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
    gap: 1rem;
  }

  .spawn-form {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    padding: 0.75rem;
    background: var(--color-surface, #1a1a2e);
    border-radius: 8px;
    border: 1px solid var(--color-border, #2a2a4a);
  }

  .form-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
  }

  .form-title {
    font-weight: 600;
    font-size: 0.875rem;
    color: var(--color-text, #e0e0e0);
  }

  .active-count {
    font-size: 0.75rem;
    color: var(--color-success, #22c55e);
    font-variant-numeric: tabular-nums;
  }

  .form-row {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 0.5rem;
  }

  .form-label {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    font-size: 0.75rem;
    color: var(--color-text-secondary, #8b8ba0);
  }

  .form-label.full-width {
    grid-column: 1 / -1;
  }

  .form-input, .form-select, .form-textarea {
    padding: 0.375rem 0.5rem;
    background: var(--color-bg, #0f0f23);
    border: 1px solid var(--color-border, #2a2a4a);
    border-radius: 4px;
    color: var(--color-text, #e0e0e0);
    font-size: 0.8125rem;
    font-family: inherit;
  }

  .form-textarea {
    resize: vertical;
    min-height: 3rem;
  }

  .form-error {
    font-size: 0.75rem;
    color: var(--color-error, #ef4444);
    padding: 0.25rem 0;
  }

  .spawn-button {
    padding: 0.5rem 1rem;
    background: var(--color-accent, #6366f1);
    color: white;
    border: none;
    border-radius: 6px;
    font-size: 0.8125rem;
    font-weight: 600;
    cursor: pointer;
    transition: opacity 0.15s;
  }

  .spawn-button:hover:not(:disabled) {
    opacity: 0.9;
  }

  .spawn-button:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .spawns-list {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  .spawn-row {
    padding: 0.625rem;
    background: var(--color-surface, #1a1a2e);
    border-radius: 6px;
    border: 1px solid var(--color-border, #2a2a4a);
  }

  .spawn-row.active {
    border-color: var(--color-success, #22c55e);
    border-opacity: 0.3;
  }

  .spawn-header {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    margin-bottom: 0.25rem;
  }

  .spawn-project {
    font-weight: 600;
    font-size: 0.8125rem;
    color: var(--color-text, #e0e0e0);
  }

  .spawn-status {
    font-size: 0.75rem;
    font-weight: 500;
  }

  .spawn-duration {
    font-size: 0.75rem;
    color: var(--color-text-secondary, #8b8ba0);
    font-variant-numeric: tabular-nums;
    margin-left: auto;
  }

  .stop-button {
    padding: 0.125rem 0.5rem;
    font-size: 0.6875rem;
    background: transparent;
    border: 1px solid var(--color-error, #ef4444);
    color: var(--color-error, #ef4444);
    border-radius: 4px;
    cursor: pointer;
  }

  .stop-button:hover {
    background: var(--color-error, #ef4444);
    color: white;
  }

  .spawn-task {
    font-size: 0.75rem;
    color: var(--color-text-secondary, #8b8ba0);
    line-height: 1.4;
    margin-bottom: 0.25rem;
    display: -webkit-box;
    -webkit-line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }

  .spawn-meta {
    display: flex;
    gap: 0.5rem;
    font-size: 0.6875rem;
    color: var(--color-text-tertiary, #5b5b7a);
  }

  .spawn-error {
    color: var(--color-error, #ef4444);
  }
</style>
