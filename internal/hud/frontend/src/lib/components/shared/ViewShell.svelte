<script>
  /**
   * ViewShell — wrapper for a grouped view that provides sub-navigation tabs.
   * Renders a segmented control bar when the view has multiple sub-panels.
   *
   * @type {{
   *   subViews: Array<{ id: string, label: string, key: string }>,
   *   activeSubView: string,
   *   onSwitch: (id: string) => void,
   *   children: import('svelte').Snippet,
   * }}
   */
  let {
    subViews = [],
    activeSubView = '',
    onSwitch = () => {},
    children,
  } = $props();

  let showTabs = $derived(subViews.length > 1);
</script>

<div class="view-shell">
  {#if showTabs}
    <nav class="view-tabs" aria-label="Sub-navigation">
      {#each subViews as sv}
        <button
          class="view-tab"
          class:active={activeSubView === sv.id}
          onclick={() => onSwitch(sv.id)}
          aria-current={activeSubView === sv.id ? 'page' : undefined}
          title="{sv.label} ({sv.key})"
        >
          <span class="view-tab-label">{sv.label}</span>
          <kbd class="view-tab-key">{sv.key}</kbd>
        </button>
      {/each}
    </nav>
  {/if}

  <div class="view-content">
    {@render children()}
  </div>
</div>

<style>
  .view-shell {
    display: flex;
    flex-direction: column;
    flex: 1;
    overflow: hidden;
  }

  .view-tabs {
    display: flex;
    align-items: center;
    gap: 2px;
    padding: var(--space-1) var(--panel-padding);
    background: var(--bg-secondary);
    border-bottom: 1px solid var(--border);
    flex-shrink: 0;
  }

  .view-tab {
    display: flex;
    align-items: center;
    gap: var(--space-1);
    padding: var(--space-1) var(--space-3);
    border-radius: var(--radius-sm);
    font-size: var(--text-sm);
    font-weight: 500;
    color: var(--fg-muted);
    transition: background var(--transition-fast), color var(--transition-fast);
    position: relative;
    cursor: pointer;
    background: none;
    border: none;
  }

  .view-tab:hover {
    background: var(--bg-tertiary);
    color: var(--fg-secondary);
  }

  .view-tab.active {
    background: var(--bg-tertiary);
    color: var(--fg-primary);
  }

  .view-tab.active::after {
    content: '';
    position: absolute;
    bottom: -5px;
    left: var(--space-2);
    right: var(--space-2);
    height: 2px;
    background: var(--info);
    border-radius: 1px;
  }

  .view-tab-label {
    white-space: nowrap;
  }

  .view-tab-key {
    font-size: 9px;
    font-family: var(--font-mono);
    color: var(--fg-muted);
    padding: 0 3px;
    border: 1px solid var(--border);
    border-radius: 2px;
    line-height: 1.2;
    opacity: 0.6;
  }

  .view-tab.active .view-tab-key {
    opacity: 0.8;
  }

  .view-content {
    flex: 1;
    overflow: hidden;
    display: flex;
    flex-direction: column;
  }

  @media (max-width: 768px) {
    .view-tabs {
      overflow-x: auto;
      scrollbar-width: none;
      -webkit-overflow-scrolling: touch;
    }
    .view-tabs::-webkit-scrollbar {
      display: none;
    }
    .view-tab-key {
      display: none;
    }
    .view-tab {
      min-height: 44px;
      flex-shrink: 0;
    }
  }

  @media (max-width: 480px) {
    .view-tabs {
      padding: var(--space-1) var(--space-2);
    }
  }
</style>
