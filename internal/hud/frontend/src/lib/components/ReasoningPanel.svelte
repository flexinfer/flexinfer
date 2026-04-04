<script>
  import { reasoningStore } from '../stores/reasoning.svelte.ts';
  import { toastStore } from '../stores/toasts.svelte.ts';
  import { formatTime, relativeTime, statusVariant, confidenceColor } from '../utils/format.ts';
  import Badge from '../widgets/Badge.svelte';
  import Modal from '../widgets/Modal.svelte';
  import EmptyState from './shared/EmptyState.svelte';

  $effect(() => {
    reasoningStore.startPolling(15000);
    return () => { reasoningStore.stopPolling(); };
  });

  let chains = $derived(reasoningStore.chains ?? []);

  // Chain detail expansion
  let expandedChains = $state({});
  let loadingChain = $state(null);

  async function toggleChain(chain) {
    if (expandedChains[chain.id]) {
      const next = { ...expandedChains };
      delete next[chain.id];
      expandedChains = next;
      return;
    }
    loadingChain = chain.id;
    const detail = await reasoningStore.getChainDetail(chain.id);
    if (detail) {
      expandedChains = { ...expandedChains, [chain.id]: detail };
    }
    loadingChain = null;
  }

  // Create chain modal
  let showCreateModal = $state(false);
  let newTitle = $state('');
  let newDescription = $state('');
  let creating = $state(false);

  async function submitCreateChain() {
    if (!newTitle.trim()) return;
    creating = true;
    const ok = await reasoningStore.createChain(newTitle.trim(), newDescription.trim());
    creating = false;
    if (ok) {
      toastStore.success('Reasoning chain created');
      showCreateModal = false;
      newTitle = '';
      newDescription = '';
    } else {
      toastStore.error('Failed to create chain');
    }
  }

</script>

<div class="panel reasoning-panel">
  <!-- Header -->
  <div class="header-bar">
    <div class="header-stats">
      <span class="header-total text-mono">{chains.length} chains</span>
      <span class="header-stat">
        <span class="dot dot-active"></span>
        {reasoningStore.activeChains.length} active
      </span>
      <span class="header-stat">
        <span class="dot dot-completed"></span>
        {reasoningStore.completedChains.length} completed
      </span>
    </div>
    <button class="btn btn-sm" onclick={() => { showCreateModal = true; }}>+ Chain</button>
  </div>

  <!-- Chain list -->
  <div class="chain-list">
    {#each chains as chain (chain.id)}
      <div class="chain-card" class:expanded={expandedChains[chain.id]}>
        <!-- Chain header (clickable) -->
        <button class="chain-header" onclick={() => toggleChain(chain)}>
          <span class="chain-chevron">{expandedChains[chain.id] ? '▼' : '▶'}</span>
          <span class="chain-title text-mono">{chain.title}</span>
          <Badge text={chain.status} variant={statusVariant(chain.status)} />
          <span class="chain-meta text-muted text-xs">{chain.step_count} steps</span>
          {#if chain.confidence != null}
            <span class="confidence-pill" style:background={confidenceColor(chain.confidence)}>
              {(chain.confidence * 100).toFixed(0)}%
            </span>
          {/if}
          <span class="chain-time text-muted text-xs text-mono">{relativeTime(chain.created_at)}</span>
        </button>

        <!-- Expanded steps -->
        {#if expandedChains[chain.id]}
          <div class="chain-steps">
            {#each expandedChains[chain.id].steps ?? [] as step, i (step.id ?? i)}
              <div class="step-row">
                <div class="step-number">{i + 1}</div>
                <div class="step-content">
                  <div class="step-description">{step.description}</div>
                  {#if step.evidence}
                    <div class="step-evidence text-sm text-muted">{step.evidence}</div>
                  {/if}
                </div>
                <div class="step-confidence">
                  <div class="confidence-bar-track">
                    <div
                      class="confidence-bar-fill"
                      style:width="{(step.confidence * 100).toFixed(0)}%"
                      style:background={confidenceColor(step.confidence)}
                    ></div>
                  </div>
                  <span class="confidence-label text-mono text-xs" style:color={confidenceColor(step.confidence)}>
                    {(step.confidence * 100).toFixed(0)}%
                  </span>
                </div>
              </div>
            {:else}
              <div class="empty-steps text-muted text-xs">No steps recorded</div>
            {/each}
          </div>
        {:else if loadingChain === chain.id}
          <div class="chain-loading">
            <div class="loading-bar"><div class="loading-bar-inner"></div></div>
          </div>
        {/if}
      </div>
    {:else}
      <EmptyState icon={'\u2699'} heading="No reasoning chains" description="Create a chain or seed one via MCP tool" />
    {/each}
  </div>
</div>

<!-- Create Chain Modal -->
<Modal title="New Reasoning Chain" open={showCreateModal} onClose={() => { showCreateModal = false; }}>
  <div class="form-field">
    <label class="form-label" for="reasoning-chain-title">Title</label>
    <input id="reasoning-chain-title" type="text" bind:value={newTitle} placeholder="Chain title..." />
  </div>
  <div class="form-field">
    <label class="form-label" for="reasoning-chain-description">Description</label>
    <textarea id="reasoning-chain-description" bind:value={newDescription} placeholder="What is being reasoned about..." rows="3"></textarea>
  </div>
  <div class="form-actions">
    <button class="btn btn-ghost" onclick={() => { showCreateModal = false; }}>Cancel</button>
    <button class="btn btn-primary" onclick={submitCreateChain} disabled={creating || !newTitle.trim()}>
      {creating ? 'Creating...' : 'Create Chain'}
    </button>
  </div>
</Modal>

<style>
  .reasoning-panel {
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  .header-bar {
    padding: var(--space-2) 0;
    border-bottom: 1px solid var(--border);
    margin-bottom: var(--space-2);
    display: flex;
    align-items: center;
    justify-content: space-between;
    position: relative;
  }

  .header-bar::after {
    content: '';
    position: absolute;
    bottom: 0;
    left: 10%;
    right: 10%;
    height: 1px;
    background: linear-gradient(90deg, transparent, rgba(0, 200, 255, 0.06) 50%, transparent);
    pointer-events: none;
  }

  .header-stats {
    display: flex;
    align-items: center;
    gap: var(--space-4);
    font-size: var(--text-sm);
  }

  .header-total {
    font-weight: 600;
    color: var(--fg-primary);
  }

  .header-stat {
    display: flex;
    align-items: center;
    gap: var(--space-1);
    color: var(--fg-secondary);
  }

  .dot {
    width: var(--space-2);
    height: var(--space-2);
    border-radius: 50%;
  }

  .dot-active { background: var(--success); }
  .dot-completed { background: var(--info); }

  /* Chain list */

  .chain-list {
    flex: 1;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .chain-card {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    overflow: hidden;
    transition: border-color var(--transition-fast);
    position: relative;
  }

  .chain-card::before {
    content: '';
    position: absolute;
    inset: 0;
    border-radius: inherit;
    background: var(--surface-highlight);
    pointer-events: none;
  }

  .chain-card.expanded {
    border-color: rgba(0, 200, 255, 0.18);
    box-shadow: 0 0 6px var(--glow-info);
  }

  .chain-header {
    display: flex;
    align-items: center;
    gap: var(--space-3);
    width: 100%;
    padding: var(--space-3) var(--space-4);
    font-size: var(--text-base);
    text-align: left;
    color: var(--fg-primary);
    cursor: pointer;
    border: none;
    background: transparent;
    transition: background var(--transition-fast);
    letter-spacing: var(--tracking-normal);
  }

  .chain-header:hover {
    background: var(--bg-elevated);
  }

  .chain-chevron {
    font-size: var(--text-xs);
    color: var(--fg-muted);
    width: 14px;
    flex-shrink: 0;
  }

  .chain-title {
    font-weight: 600;
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .chain-meta {
    flex-shrink: 0;
  }

  .confidence-pill {
    font-size: var(--text-xs);
    font-family: var(--font-mono);
    font-weight: 600;
    color: var(--bg-primary);
    padding: 1px 6px;
    border-radius: var(--radius-md);
    flex-shrink: 0;
  }

  .chain-time {
    flex-shrink: 0;
  }

  /* Steps */

  .chain-steps {
    border-top: 1px solid var(--border-subtle);
    padding: var(--space-3) var(--space-4);
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
  }

  .step-row {
    display: flex;
    align-items: flex-start;
    gap: var(--space-3);
  }

  .step-number {
    width: 24px;
    height: 24px;
    border-radius: 50%;
    background: var(--bg-tertiary);
    border: 1px solid var(--border);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: var(--text-xs);
    font-family: var(--font-mono);
    font-weight: 600;
    color: var(--fg-secondary);
    flex-shrink: 0;
  }

  .step-content {
    flex: 1;
    min-width: 0;
  }

  .step-description {
    font-size: var(--text-base);
    color: var(--fg-primary);
    line-height: 1.4;
    letter-spacing: var(--tracking-normal);
  }

  .step-evidence {
    margin-top: var(--space-1);
    font-style: italic;
    line-height: 1.3;
  }

  .step-confidence {
    flex-shrink: 0;
    width: 80px;
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    gap: 2px;
  }

  .confidence-bar-track {
    width: 100%;
    height: 4px;
    background: var(--bg-primary);
    border-radius: 2px;
    overflow: hidden;
  }

  .confidence-bar-fill {
    height: 100%;
    border-radius: 2px;
    transition: width var(--transition-slow);
  }

  .confidence-label {
    font-weight: 600;
  }

  .empty-steps {
    padding: var(--space-2) 0;
    text-align: center;
  }

  .chain-loading {
    padding: var(--space-2) var(--space-4);
  }

</style>
