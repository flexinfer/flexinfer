<script lang="ts">
  import type { AgentErrorEntry, PaginatedResponse } from './types.ts';
  import { PAGE_LIMIT } from './types.ts';
  import { adminFetch } from '../../stores/labsAuth.svelte.ts';

  interface Props {
    spawnId: string;
  }

  let { spawnId }: Props = $props();

  let items = $state<AgentErrorEntry[]>([]);
  let total = $state(0);
  let offset = $state(0);
  let loading = $state(false);
  let error = $state<string | null>(null);
  let initialized = $state(false);

  async function loadPage(nextOffset: number, append: boolean): Promise<void> {
    loading = true;
    error = null;
    try {
      const url = `/api/agent/spawn/${encodeURIComponent(spawnId)}/telemetry/errors?limit=${PAGE_LIMIT}&offset=${nextOffset}`;
      const res = await adminFetch(url, {
        requireToken: true,
        action: 'Loading spawn error telemetry',
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = (await res.json()) as PaginatedResponse<AgentErrorEntry>;
      const page = data.items ?? [];
      items = append ? [...items, ...page] : page;
      total = data.total ?? items.length;
      offset = nextOffset + page.length;
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      loading = false;
      initialized = true;
    }
  }

  $effect(() => {
    if (!spawnId) return;
    items = [];
    total = 0;
    offset = 0;
    initialized = false;
    loadPage(0, false);
  });

  let canLoadMore = $derived(offset < total);

  function typeClass(t: string): string {
    const k = (t || '').toLowerCase();
    if (k === 'fatal' || k === 'execution') return 'type-fatal';
    if (k === 'permission_denied') return 'type-perm';
    if (k === 'rate_limit') return 'type-rate';
    if (k === 'max_turns' || k === 'max_budget') return 'type-budget';
    if (k === 'tool_failure') return 'type-tool';
    return 'type-other';
  }

  function formatTime(ts: string): string {
    if (!ts) return '—';
    try {
      const d = new Date(ts);
      return d.toLocaleTimeString();
    } catch {
      return ts;
    }
  }
</script>

<div class="tab-content">
  {#if error}
    <div class="tab-error">{error}</div>
  {:else if !initialized && loading}
    <div class="tab-loading">Loading errors...</div>
  {:else if items.length === 0}
    <div class="tab-empty">No errors recorded.</div>
  {:else}
    <div class="items-list">
      {#each items as err, i (i)}
        <div class="error-row">
          <div class="error-header">
            <span class="type-badge {typeClass(err.type)}">{err.type}</span>
            <span class="timestamp">{formatTime(err.time)}</span>
          </div>
          <div class="error-message">{err.message}</div>
        </div>
      {/each}
    </div>
    {#if canLoadMore}
      <button class="load-more" onclick={() => loadPage(offset, true)} disabled={loading}>
        {loading ? 'Loading...' : `Load more (${total - offset} remaining)`}
      </button>
    {:else if total > 0}
      <div class="tab-footer">Showing {items.length} of {total}</div>
    {/if}
  {/if}
</div>

<style>
  .tab-content {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    padding: var(--space-3);
    background: var(--bg-secondary);
    border-radius: var(--radius-md);
    border: 1px solid var(--border);
    font-family: var(--font-mono);
  }

  .tab-loading,
  .tab-empty {
    padding: var(--space-2);
    color: var(--fg-secondary);
    font-size: var(--text-sm);
  }

  .tab-error {
    padding: var(--space-2);
    color: var(--error);
    font-size: var(--text-sm);
  }

  .items-list {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    max-height: 24rem;
    overflow-y: auto;
  }

  .error-row {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
    padding: var(--space-2);
    border: 1px solid var(--border);
    border-radius: var(--radius-xs);
    background: var(--bg-primary);
  }

  .error-header {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }

  .type-badge {
    font-size: var(--text-2xs, 9px);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    padding: 2px var(--space-1);
    border-radius: var(--radius-xs);
    border: 1px solid var(--border);
    font-weight: 600;
  }

  .type-fatal {
    color: var(--error);
    background: var(--error-dim);
    border-color: var(--error);
  }

  .type-perm {
    color: var(--warning);
    background: var(--warning-dim);
    border-color: var(--warning);
  }

  .type-rate {
    color: var(--warning);
    background: var(--warning-dim);
    border-color: var(--warning);
  }

  .type-budget {
    color: var(--accent);
    background: var(--accent-dim);
    border-color: var(--accent);
  }

  .type-tool {
    color: var(--info);
    background: var(--info-dim);
    border-color: var(--info);
  }

  .type-other {
    color: var(--fg-secondary);
  }

  .timestamp {
    margin-left: auto;
    font-size: var(--text-xs);
    color: var(--fg-dim);
    font-variant-numeric: tabular-nums;
  }

  .error-message {
    font-size: var(--text-sm);
    color: var(--fg-primary);
    white-space: pre-wrap;
    word-break: break-word;
    line-height: 1.4;
  }

  .load-more {
    padding: var(--space-1) var(--space-3);
    background: transparent;
    border: 1px solid var(--border);
    border-radius: var(--radius-xs);
    color: var(--fg-secondary);
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    cursor: pointer;
    align-self: flex-start;
    transition: border-color var(--transition-fast), color var(--transition-fast);
  }

  .load-more:hover:not(:disabled) {
    border-color: var(--border-active);
    color: var(--fg-primary);
  }

  .load-more:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .tab-footer {
    font-size: var(--text-xs);
    color: var(--fg-dim);
  }
</style>
