<script>
  import { orchestrationStore } from '../../stores/orchestration.svelte.ts';
  import { presenceActionsStore } from '../../stores/presenceActions.svelte.ts';
  import EmptyState from '../shared/EmptyState.svelte';

  let { collapsed = $bindable(false) } = $props();

  let recommendations = $derived(orchestrationStore.recommendations);

  function dispatchRecommendation(rec) {
    presenceActionsStore.onOpenDispatchWithDefaults(rec.recommended_agent, rec.task_title, 'high');
  }

  function scoreColor(score) {
    if (score >= 0.8) return 'var(--success)';
    if (score >= 0.5) return 'var(--warning)';
    return 'var(--fg-muted)';
  }
</script>

<section class="dispatch-section">
  <div class="section-head">
    <button class="section-toggle" onclick={() => collapsed = !collapsed}>
      <span class="toggle-icon">{collapsed ? '\u25B6' : '\u25BC'}</span>
      <h3 class="section-title">Suggested assignments</h3>
      <span class="section-count">{recommendations.length}</span>
    </button>
    <div class="section-subtitle">
      AI-recommended agent-task pairings based on capacity and context
    </div>
  </div>

  {#if !collapsed}
    {#if recommendations.length > 0}
      <div class="rec-list">
        {#each recommendations as rec (rec.task_id)}
          <div class="rec-card">
            <div class="rec-top">
              <div class="rec-task">{rec.task_title}</div>
              <span class="rec-score" style="color: {scoreColor(rec.score)}">
                {Math.round(rec.score * 100)}%
              </span>
            </div>
            <div class="rec-mid">
              <span class="rec-arrow">{'\u2192'}</span>
              <span class="rec-agent">{rec.recommended_agent}</span>
              <span class="rec-reason">{rec.reason}</span>
            </div>
            <div class="rec-actions">
              <button class="btn btn-sm btn-dispatch" onclick={() => dispatchRecommendation(rec)}>
                Dispatch
              </button>
            </div>
          </div>
        {/each}
      </div>
    {:else}
      <EmptyState
        icon={'\u2728'}
        heading="No recommendations"
        description="The orchestrator has no pending task-agent pairings to suggest right now."
        compact
      />
    {/if}
  {/if}
</section>

<style>
  .rec-list {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .rec-card {
    padding: var(--space-2) var(--space-3);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--bg-tertiary);
  }

  .rec-top {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: var(--space-2);
  }

  .rec-task {
    font-weight: 600;
    font-size: var(--text-sm);
    color: var(--fg-primary);
  }

  .rec-score {
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    font-weight: 600;
    flex-shrink: 0;
  }

  .rec-mid {
    display: flex;
    align-items: center;
    gap: var(--space-1);
    margin: var(--space-1) 0;
    font-size: var(--text-xs);
  }

  .rec-arrow {
    color: var(--fg-muted);
  }

  .rec-agent {
    font-family: var(--font-mono);
    font-weight: 600;
    color: var(--accent);
    padding: 1px 6px;
    border-radius: var(--radius-sm);
    background: rgba(var(--accent-rgb, 100, 160, 255), 0.12);
  }

  .rec-reason {
    color: var(--fg-muted);
    flex: 1;
  }

  .rec-actions {
    display: flex;
    gap: var(--space-1);
    margin-top: var(--space-1);
  }

  .btn-dispatch {
    font-size: 10px;
    padding: 2px 8px;
    border-radius: var(--radius-sm);
    cursor: pointer;
    border: 1px solid var(--accent);
    background: transparent;
    color: var(--accent);
    transition: background var(--transition-fast);
  }

  .btn-dispatch:hover {
    background: var(--accent);
    color: var(--bg-primary);
  }

  .section-toggle {
    display: flex;
    align-items: center;
    gap: var(--space-1);
    background: none;
    border: none;
    cursor: pointer;
    padding: 0;
    color: inherit;
  }

  .toggle-icon {
    font-size: 10px;
    color: var(--fg-muted);
    width: 12px;
  }

  .section-toggle .section-title {
    margin: 0;
  }

  .section-count {
    font-family: var(--font-mono);
    font-size: 10px;
    background: var(--bg-tertiary);
    color: var(--fg-secondary);
    padding: 0 5px;
    border-radius: var(--radius-lg);
  }
</style>
