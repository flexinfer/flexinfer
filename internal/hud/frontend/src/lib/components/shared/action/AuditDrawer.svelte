<script lang="ts">
  import { actionStore, type ActionEntry } from '../../../stores/action.svelte.ts';

  function formatRelative(t: number): string {
    const diff = Math.floor((Date.now() - t) / 1000);
    if (diff < 5) return 'just now';
    if (diff < 60) return `${diff}s ago`;
    if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
    return `${Math.floor(diff / 3600)}h ago`;
  }

  function formatDuration(entry: ActionEntry): string {
    if (entry.endedAt == null) return '…';
    const ms = entry.endedAt - entry.startedAt;
    if (ms < 1000) return `${ms}ms`;
    return `${(ms / 1000).toFixed(1)}s`;
  }

  function statusLabel(status: ActionEntry['status']): string {
    switch (status) {
      case 'pending':     return 'PENDING';
      case 'success':     return 'OK';
      case 'error':       return 'ERROR';
      case 'rolled_back': return 'ROLLBACK';
    }
  }

  function handleKeydown(event: KeyboardEvent): void {
    if (event.key === 'Escape') actionStore.closeDrawer();
  }

  let entries = $derived(actionStore.entries);
  let open = $derived(actionStore.drawerOpen);
</script>

<svelte:window onkeydown={handleKeydown} />

{#if open}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
  <div class="audit-scrim" onclick={() => actionStore.closeDrawer()}></div>
  <aside class="audit-drawer" role="dialog" aria-label="Recent actions" aria-modal="true">
    <header class="audit-header">
      <div class="audit-title">Recent Actions</div>
      <div class="audit-actions">
        <button type="button" class="audit-btn ghost" onclick={() => actionStore.clear()} disabled={entries.length === 0}>
          Clear
        </button>
        <button type="button" class="audit-btn" onclick={() => actionStore.closeDrawer()} aria-label="Close audit drawer">
          ✕
        </button>
      </div>
    </header>

    {#if entries.length === 0}
      <div class="audit-empty">
        <div class="audit-empty-icon">▢</div>
        <div class="audit-empty-text">No actions yet this session.</div>
      </div>
    {:else}
      <ul class="audit-list">
        {#each entries as entry (entry.id)}
          <li class="audit-entry" data-status={entry.status}>
            <div class="audit-entry-head">
              <span class="audit-status" data-status={entry.status}>{statusLabel(entry.status)}</span>
              <span class="audit-label">{entry.label}</span>
              <button
                type="button"
                class="audit-entry-dismiss"
                onclick={() => actionStore.remove(entry.id)}
                aria-label="Remove entry"
              >✕</button>
            </div>
            <div class="audit-entry-meta">
              <span class="audit-source">{entry.source}</span>
              <span class="audit-divider">·</span>
              <span>{formatRelative(entry.startedAt)}</span>
              <span class="audit-divider">·</span>
              <span>{formatDuration(entry)}</span>
            </div>
            {#if entry.error}
              <div class="audit-error">{entry.error}</div>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}
  </aside>
{/if}

<style>
  .audit-scrim {
    position: fixed;
    inset: 0;
    background: rgba(6, 12, 16, 0.55);
    z-index: 900;
    animation: fadeIn 0.15s ease-out;
  }

  .audit-drawer {
    position: fixed;
    top: 0;
    right: 0;
    bottom: 0;
    width: min(420px, 92vw);
    background: var(--bg-secondary);
    border-left: 1px solid var(--border);
    box-shadow: -16px 0 32px rgba(0, 0, 0, 0.4);
    z-index: 901;
    display: flex;
    flex-direction: column;
    animation: slideIn 0.18s ease-out;
  }

  .audit-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-2);
    padding: var(--space-3) var(--space-4);
    border-bottom: 1px solid var(--border);
  }

  .audit-title {
    font-size: var(--text-sm);
    font-weight: 600;
    color: var(--fg-primary);
    letter-spacing: var(--tracking-wide);
    text-transform: uppercase;
  }

  .audit-actions {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }

  .audit-btn {
    padding: 4px 10px;
    border: 1px solid var(--border);
    background: transparent;
    color: var(--fg-secondary);
    border-radius: var(--radius-xs);
    font-size: var(--text-xs);
    font-family: var(--font-mono);
    cursor: pointer;
    transition: color var(--transition-fast), border-color var(--transition-fast), background var(--transition-fast);
  }

  .audit-btn:hover:not(:disabled) {
    color: var(--fg-primary);
    border-color: var(--border-active);
    background: var(--bg-tertiary);
  }

  .audit-btn:disabled {
    opacity: 0.4;
    cursor: not-allowed;
  }

  .audit-btn.ghost {
    border-color: var(--border-subtle);
  }

  .audit-empty {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: var(--space-2);
    color: var(--fg-muted);
  }

  .audit-empty-icon {
    font-size: 32px;
    opacity: 0.5;
  }

  .audit-empty-text {
    font-size: var(--text-sm);
  }

  .audit-list {
    flex: 1;
    overflow-y: auto;
    padding: var(--space-2) 0;
    margin: 0;
    list-style: none;
  }

  .audit-entry {
    display: flex;
    flex-direction: column;
    gap: 4px;
    padding: var(--space-2) var(--space-4);
    border-bottom: 1px solid var(--border-subtle);
  }

  .audit-entry:last-child {
    border-bottom: none;
  }

  .audit-entry-head {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }

  .audit-status {
    flex-shrink: 0;
    padding: 1px 6px;
    border-radius: var(--radius-xs);
    font-size: 9px;
    font-family: var(--font-mono);
    font-weight: 700;
    letter-spacing: 0.08em;
    background: var(--bg-tertiary);
    color: var(--fg-muted);
  }

  .audit-status[data-status='pending'] {
    background: var(--info-dim);
    color: var(--info);
  }

  .audit-status[data-status='success'] {
    background: var(--success-dim);
    color: var(--success);
  }

  .audit-status[data-status='error'] {
    background: var(--error-dim);
    color: var(--error);
  }

  .audit-status[data-status='rolled_back'] {
    background: var(--warning-dim);
    color: var(--warning);
  }

  .audit-label {
    flex: 1;
    min-width: 0;
    font-size: var(--text-sm);
    color: var(--fg-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .audit-entry-dismiss {
    flex-shrink: 0;
    font-size: 10px;
    color: var(--fg-muted);
    background: transparent;
    border: none;
    padding: 2px 4px;
    border-radius: var(--radius-xs);
    cursor: pointer;
  }

  .audit-entry-dismiss:hover {
    color: var(--fg-primary);
    background: var(--bg-tertiary);
  }

  .audit-entry-meta {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: var(--text-xs);
    color: var(--fg-muted);
    font-family: var(--font-mono);
  }

  .audit-source {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 240px;
  }

  .audit-divider {
    opacity: 0.5;
  }

  .audit-error {
    margin-top: 2px;
    padding: 4px 8px;
    background: var(--error-dim);
    border-left: 2px solid var(--error);
    border-radius: var(--radius-xs);
    color: var(--fg-primary);
    font-size: var(--text-xs);
    font-family: var(--font-mono);
    line-height: 1.4;
    word-break: break-word;
  }

  @keyframes fadeIn {
    from { opacity: 0; }
    to   { opacity: 1; }
  }

  @keyframes slideIn {
    from { transform: translateX(100%); }
    to   { transform: translateX(0); }
  }

  @media (max-width: 480px) {
    .audit-drawer {
      width: 100vw;
    }
  }
</style>
