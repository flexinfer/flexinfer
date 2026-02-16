<script>
  /**
   * BulkToolbar — sticky bottom toolbar for bulk actions on selected rows.
   *
   * @type {{
   *   count: number,
   *   actions: Array<{ label: string, variant?: string, onclick: () => void }>,
   *   onClearSelection: () => void,
   * }}
   */
  let {
    count = 0,
    actions = [],
    onClearSelection,
  } = $props();

  let visible = $derived(count > 0);
</script>

<div class="bulk-toolbar" class:visible aria-live="polite">
  <div class="bulk-left">
    <span class="bulk-count">{count} selected</span>
    <button class="btn btn-ghost btn-sm" onclick={onClearSelection}>Clear</button>
  </div>
  <div class="bulk-right">
    {#each actions as action}
      <button
        class="btn btn-sm {action.variant ? `btn-${action.variant}` : 'btn-ghost'}"
        onclick={action.onclick}
      >{action.label}</button>
    {/each}
  </div>
</div>

<style>
  .bulk-toolbar {
    position: sticky;
    bottom: 0;
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: var(--space-2) var(--space-3);
    background: var(--bg-secondary);
    border-top: 1px solid var(--border);
    backdrop-filter: blur(8px);
    box-shadow: var(--elevation-2);
    z-index: 10;
    transform: translateY(100%);
    opacity: 0;
    transition: transform 0.2s ease, opacity 0.2s ease;
    pointer-events: none;
  }

  .bulk-toolbar.visible {
    transform: translateY(0);
    opacity: 1;
    pointer-events: auto;
  }

  .bulk-left {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }

  .bulk-count {
    font-size: var(--text-sm);
    font-weight: 600;
    font-family: var(--font-mono);
    color: var(--fg-primary);
  }

  .bulk-right {
    display: flex;
    align-items: center;
    gap: var(--space-1);
  }
</style>
