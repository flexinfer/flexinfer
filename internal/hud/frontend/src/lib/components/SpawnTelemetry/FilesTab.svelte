<script lang="ts">
  import type { FileChangeEntry, PaginatedResponse } from './types.ts';
  import { PAGE_LIMIT } from './types.ts';
  import { adminFetch } from '../../stores/labsAuth.svelte.ts';

  interface Props {
    spawnId: string;
  }

  let { spawnId }: Props = $props();

  let items = $state<FileChangeEntry[]>([]);
  let total = $state(0);
  let offset = $state(0);
  let loading = $state(false);
  let error = $state<string | null>(null);
  let initialized = $state(false);

  async function loadPage(nextOffset: number, append: boolean): Promise<void> {
    loading = true;
    error = null;
    try {
      const url = `/api/agent/spawn/${encodeURIComponent(spawnId)}/telemetry/files?limit=${PAGE_LIMIT}&offset=${nextOffset}`;
      const res = await adminFetch(url, {
        requireToken: true,
        action: 'Loading spawn file telemetry',
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = (await res.json()) as PaginatedResponse<FileChangeEntry>;
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

  function kindClass(kind: string): string {
    const k = (kind || '').toLowerCase();
    if (k.startsWith('add') || k === 'create') return 'kind-add';
    if (k.startsWith('mod')) return 'kind-mod';
    if (k.startsWith('del')) return 'kind-del';
    return 'kind-other';
  }

  function kindLabel(kind: string): string {
    const k = (kind || '').toLowerCase();
    if (k === 'create') return 'added';
    if (k === 'modify') return 'modified';
    if (k === 'delete') return 'deleted';
    return k || 'changed';
  }
</script>

<div class="tab-content">
  {#if error}
    <div class="tab-error">{error}</div>
  {:else if !initialized && loading}
    <div class="tab-loading">Loading file changes...</div>
  {:else if items.length === 0}
    <div class="tab-empty">No file changes recorded.</div>
  {:else}
    <div class="items-list">
      {#each items as fc, i (i)}
        <div class="file-row">
          <span class="kind-badge {kindClass(fc.kind)}">{kindLabel(fc.kind)}</span>
          <span class="file-path" title={fc.path}>{fc.path}</span>
          {#if typeof fc.lines_added === 'number' || typeof fc.lines_removed === 'number'}
            <span class="lines-delta">
              {#if typeof fc.lines_added === 'number'}<span class="lines-add">+{fc.lines_added}</span>{/if}
              {#if typeof fc.lines_removed === 'number'}<span class="lines-del">-{fc.lines_removed}</span>{/if}
            </span>
          {/if}
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
    gap: var(--space-1);
    max-height: 24rem;
    overflow-y: auto;
  }

  .file-row {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    padding: var(--space-1) 0;
    font-size: var(--text-sm);
    color: var(--fg-primary);
    border-bottom: 1px solid var(--border-subtle);
  }

  .kind-badge {
    font-size: var(--text-2xs, 9px);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    padding: 2px var(--space-1);
    border-radius: var(--radius-xs);
    border: 1px solid var(--border);
    min-width: 3.5rem;
    text-align: center;
    font-weight: 600;
  }

  .kind-add {
    color: var(--success);
    background: var(--success-dim);
    border-color: var(--success);
  }

  .kind-mod {
    color: var(--warning);
    background: var(--warning-dim);
    border-color: var(--warning);
  }

  .kind-del {
    color: var(--error);
    background: var(--error-dim);
    border-color: var(--error);
  }

  .kind-other {
    color: var(--fg-dim);
  }

  .file-path {
    flex: 1;
    color: var(--fg-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .lines-delta {
    display: flex;
    gap: var(--space-1);
    font-size: var(--text-xs);
    font-variant-numeric: tabular-nums;
  }

  .lines-add { color: var(--success); }
  .lines-del { color: var(--error); }

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
