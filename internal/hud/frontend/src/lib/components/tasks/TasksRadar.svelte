<script lang="ts">
  /**
   * TasksRadar — 4-card sidecar (Dependency Radar, Risky Namespaces,
   * Active Blockers, Agent Pressure). Reads taskStore + coordinationStore
   * directly; no props beyond the count overrides passed by the panel.
   */
  import { taskStore } from '../../stores/tasks.svelte.ts';
  import { coordinationStore } from '../../stores/coordination.svelte.ts';

  let tasks = $derived(taskStore.tasks ?? []);
  let coordinationSummary = $derived(coordinationStore.summary);
  let riskyNamespaces = $derived(coordinationStore.riskyNamespaces);
  let activeBlockers = $derived(coordinationStore.activeBlockers);
  let attentionAgents = $derived(coordinationStore.topAttentionAgents);
  let unassignedCount = $derived(tasks.filter((task) => !(task as any).agent_id && !(task as any).agent).length);
  let staleBlockedCount = $derived(
    tasks.filter((task) => task.status === 'blocked' && (task as any).blocked_by?.length).length
  );
</script>

<aside class="tasks-rail">
  <section class="radar-card">
    <div class="radar-label">Dependency Radar</div>
    <div class="radar-value">{coordinationSummary.cross_agent_blockers} cross-agent blockers</div>
    <div class="radar-meta">{staleBlockedCount} dependency-bound · {unassignedCount} unassigned</div>
  </section>

  <section class="radar-card">
    <div class="radar-label">Risky Namespaces</div>
    {#if riskyNamespaces.length > 0}
      <div class="radar-stack">
        {#each riskyNamespaces.slice(0, 4) as namespace}
          <div class="radar-list-item">
            <span class="radar-item-title text-mono" title={namespace.namespace}>{namespace.namespace}</span>
            <span class="radar-item-meta">{namespace.blocked_tasks} blocked · {namespace.cross_agent_blockers} x-agent</span>
          </div>
        {/each}
      </div>
    {:else}
      <div class="radar-meta">No risky namespaces</div>
    {/if}
  </section>

  <section class="radar-card">
    <div class="radar-label">Active Blockers</div>
    {#if activeBlockers.length > 0}
      <div class="radar-stack">
        {#each activeBlockers.slice(0, 5) as blocker}
          <div class="radar-list-item">
            <span class="radar-item-title" title={blocker.task_title}>{blocker.task_title}</span>
            <span class="radar-item-meta">
              blocked by {blocker.blocked_by_task_title || blocker.blocked_by_task_id}
              {#if blocker.cross_agent} · cross-agent{/if}
            </span>
          </div>
        {/each}
      </div>
    {:else}
      <div class="radar-meta">No active blockers</div>
    {/if}
  </section>

  <section class="radar-card">
    <div class="radar-label">Agent Pressure</div>
    {#if attentionAgents.length > 0}
      <div class="radar-stack">
        {#each attentionAgents.slice(0, 4) as agent}
          <div class="radar-list-item">
            <span class="radar-item-title text-mono">{agent.agent_id}</span>
            <span class="radar-item-meta">{agent.blocked_tasks} blocked · {agent.claim_count} claims</span>
          </div>
        {/each}
      </div>
    {:else}
      <div class="radar-meta">No agents need attention</div>
    {/if}
  </section>
</aside>

<style>
  .tasks-rail {
    min-height: 0;
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    overflow-y: auto;
  }

  .radar-card {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    padding: var(--space-3);
    display: flex;
    flex-direction: column;
    gap: 4px;
    position: relative;
  }

  .radar-card::before {
    content: '';
    position: absolute;
    inset: 0;
    border-radius: inherit;
    background: var(--surface-highlight);
    pointer-events: none;
  }

  .radar-label {
    font-size: var(--text-xs);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-muted);
    font-weight: 600;
  }

  .radar-value {
    font-size: 18px;
    font-family: var(--font-mono);
    color: var(--fg-primary);
    font-weight: 700;
  }

  .radar-meta,
  .radar-list-item {
    font-size: var(--text-xs);
    color: var(--fg-muted);
    font-family: var(--font-mono);
  }

  .radar-stack {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .radar-list-item {
    display: flex;
    flex-direction: column;
    gap: 3px;
    padding-top: var(--space-2);
    border-top: 1px solid var(--border-subtle);
  }

  .radar-list-item:first-child {
    padding-top: 0;
    border-top: none;
  }

  .radar-item-title {
    color: var(--fg-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .radar-item-meta {
    color: var(--fg-dim);
  }
</style>
