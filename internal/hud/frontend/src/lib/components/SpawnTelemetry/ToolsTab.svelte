<script lang="ts">
  import type { ToolCallEntry, PaginatedResponse } from './types.ts';
  import { PAGE_LIMIT } from './types.ts';

  interface Props {
    spawnId: string;
  }

  let { spawnId }: Props = $props();

  let items = $state<ToolCallEntry[]>([]);
  let total = $state(0);
  let offset = $state(0);
  let loading = $state(false);
  let error = $state<string | null>(null);
  let initialized = $state(false);

  async function loadPage(nextOffset: number, append: boolean): Promise<void> {
    loading = true;
    error = null;
    try {
      const url = `/api/agent/spawn/${encodeURIComponent(spawnId)}/telemetry/tools?limit=${PAGE_LIMIT}&offset=${nextOffset}`;
      const res = await fetch(url);
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = (await res.json()) as PaginatedResponse<ToolCallEntry>;
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

  function formatDuration(ms: number | undefined): string {
    if (!ms || ms < 0) return '—';
    if (ms < 1000) return `${ms}ms`;
    return `${(ms / 1000).toFixed(1)}s`;
  }

  function isSuccess(tc: ToolCallEntry): boolean {
    if (tc.error) return false;
    if (tc.exit_code !== undefined && tc.exit_code !== 0) return false;
    return true;
  }
</script>

<div class="tab-content">
  {#if error}
    <div class="tab-error">{error}</div>
  {:else if !initialized && loading}
    <div class="tab-loading">Loading tool calls...</div>
  {:else if items.length === 0}
    <div class="tab-empty">No tool calls recorded.</div>
  {:else}
    <div class="items-list">
      {#each items as tc, i (i)}
        <div class="tool-row" class:error={!isSuccess(tc)}>
          <span class="status-icon" title={isSuccess(tc) ? 'success' : tc.error || `exit ${tc.exit_code}`}>
            {isSuccess(tc) ? '\u2713' : '\u2717'}
          </span>
          <span class="tool-name">{tc.name}</span>
          {#if tc.server_name}
            <span class="server-name">{tc.server_name}</span>
          {/if}
          <span class="duration">{formatDuration(tc.duration_ms)}</span>
        </div>
        {#if tc.error}
          <div class="tool-error">{tc.error}</div>
        {/if}
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
    gap: var(--space-1);
    max-height: 24rem;
    overflow-y: auto;
  }

  .tool-row {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    padding: var(--space-1) 0;
    font-size: var(--text-sm);
    color: var(--fg-primary);
    border-bottom: 1px solid var(--border-subtle);
  }

  .tool-row.error .tool-name {
    color: var(--error);
  }

  .status-icon {
    width: 14px;
    text-align: center;
    font-weight: 600;
    color: var(--success);
    font-variant-numeric: tabular-nums;
  }

  .tool-row.error .status-icon {
    color: var(--error);
  }

  .tool-name {
    color: var(--fg-primary);
    font-weight: 500;
  }

  .server-name {
    color: var(--fg-dim);
    font-size: var(--text-xs);
  }

  .duration {
    margin-left: auto;
    color: var(--fg-secondary);
    font-variant-numeric: tabular-nums;
    font-size: var(--text-xs);
  }

  .tool-error {
    padding: var(--space-1) var(--space-2);
    font-size: var(--text-xs);
    color: var(--error);
    background: var(--error-dim);
    border-radius: var(--radius-xs);
    margin-bottom: var(--space-1);
    white-space: pre-wrap;
    word-break: break-word;
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
