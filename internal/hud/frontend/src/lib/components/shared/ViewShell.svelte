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
    gap: var(--space-1);
    padding: var(--space-2) var(--panel-padding) var(--space-1);
    background: transparent;
    flex-shrink: 0;
    position: relative;
    overflow-x: auto;
    scrollbar-width: none;
  }

  .view-tabs::-webkit-scrollbar {
    display: none;
  }

  .view-tab {
    display: flex;
    align-items: center;
    gap: var(--space-1);
    padding: 8px 12px;
    border-radius: var(--radius-md);
    font-size: var(--text-sm);
    font-weight: 500;
    color: var(--fg-muted);
    transition: background var(--transition-fast), color var(--transition-fast), border-color var(--transition-fast), box-shadow var(--transition-fast);
    position: relative;
    cursor: pointer;
    background: color-mix(in srgb, var(--bg-secondary) 88%, transparent);
    border: 1px solid transparent;
    letter-spacing: var(--tracking-normal);
    white-space: nowrap;
  }

  .view-tab:hover {
    background: color-mix(in srgb, var(--bg-tertiary) 88%, white 12%);
    color: var(--fg-primary);
    border-color: color-mix(in srgb, var(--border-focus) 72%, transparent);
  }

  .view-tab.active {
    background:
      linear-gradient(180deg, rgba(255, 255, 255, 0.02), transparent),
      var(--bg-tertiary);
    color: var(--fg-primary);
    font-weight: 600;
    border-color: color-mix(in srgb, var(--info) 24%, var(--border));
    box-shadow: 0 8px 18px rgba(0, 0, 0, 0.16);
  }

  .view-tab.active::after {
    content: '';
    position: absolute;
    bottom: 4px;
    left: var(--space-3);
    right: var(--space-3);
    height: 2px;
    background: var(--info);
    border-radius: 1px;
    box-shadow: 0 0 6px var(--glow-info);
  }

  .view-tab-label {
    white-space: nowrap;
  }

  .view-tab-key {
    font-size: 9px;
    font-family: var(--font-mono);
    color: var(--fg-muted);
    padding: 1px 4px;
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-xs);
    line-height: 1;
    opacity: 0.8;
    background: rgba(255, 255, 255, 0.02);
  }

  .view-tab.active .view-tab-key {
    opacity: 0.7;
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
