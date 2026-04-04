<script>
  /**
   * DetailDrawer — slide-in panel from the right edge for drill-down detail views.
   * Traps focus when open, closes on Escape or backdrop click.
   *
   * @type {{
   *   open?: boolean,
   *   title?: string,
   *   subtitle?: string,
   *   width?: string,
   *   onClose?: () => void,
   *   header?: import('svelte').Snippet,
   *   footer?: import('svelte').Snippet,
   *   children: import('svelte').Snippet,
   * }}
   */
  let {
    open = false,
    title = '',
    subtitle = '',
    width = 'var(--drawer-width)',
    onClose = () => {},
    header,
    footer,
    children,
  } = $props();

  let drawerEl = $state(null);
  let previousFocus = $state(null);

  $effect(() => {
    if (open) {
      previousFocus = document.activeElement;
      // Focus first focusable element inside drawer after mount
      requestAnimationFrame(() => {
        if (drawerEl) {
          const first = drawerEl.querySelector('button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])');
          if (first) first.focus();
          else drawerEl.focus();
        }
      });
    } else if (previousFocus && typeof previousFocus.focus === 'function') {
      previousFocus.focus();
      previousFocus = null;
    }
  });

  function handleKeydown(e) {
    if (e.key === 'Escape') {
      e.stopPropagation();
      onClose();
    }
  }

  function handleBackdropClick() {
    onClose();
  }
</script>

{#if open}
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="drawer-backdrop" onclick={handleBackdropClick} onkeydown={handleKeydown}></div>
  <aside
    class="drawer"
    style:width={width}
    bind:this={drawerEl}
    role="dialog"
    aria-modal="true"
    aria-label={title || 'Detail panel'}
    tabindex="-1"
    onkeydown={handleKeydown}
  >
    <!-- Header -->
    <div class="drawer-header">
      <div class="drawer-header-text">
        {#if title}
          <h3 class="drawer-title">{title}</h3>
        {/if}
        {#if subtitle}
          <span class="drawer-subtitle">{subtitle}</span>
        {/if}
      </div>
      <button class="drawer-close btn btn-ghost" onclick={onClose} aria-label="Close detail panel">
        {'\u2715'}
      </button>
    </div>

    {#if header}
      <div class="drawer-header-extra">
        {@render header()}
      </div>
    {/if}

    <!-- Content -->
    <div class="drawer-content">
      {@render children()}
    </div>

    <!-- Footer -->
    {#if footer}
      <div class="drawer-footer">
        {@render footer()}
      </div>
    {/if}
  </aside>
{/if}

<style>
  .drawer-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 23, 26, 0.4);
    z-index: 200;
    animation: fadeIn 0.15s ease-out;
  }

  .drawer {
    position: fixed;
    top: 0;
    right: 0;
    bottom: 0;
    z-index: 201;
    background: var(--bg-secondary);
    border-left: 1px solid var(--border);
    box-shadow: var(--elevation-3);
    display: flex;
    flex-direction: column;
    animation: drawerSlideIn var(--duration-normal, 200ms) ease-out;
    outline: none;
  }

  @keyframes drawerSlideIn {
    from { transform: translateX(100%); }
    to   { transform: translateX(0); }
  }

  .drawer-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: var(--space-3) var(--space-4);
    border-bottom: 1px solid var(--border);
    flex-shrink: 0;
  }

  .drawer-header-text {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }

  .drawer-title {
    font-size: var(--text-base);
    font-weight: 600;
    color: var(--fg-primary);
    margin: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .drawer-subtitle {
    font-size: var(--text-xs);
    color: var(--fg-muted);
    font-family: var(--font-mono);
  }

  .drawer-close {
    flex-shrink: 0;
    font-size: var(--text-lg);
    padding: var(--space-1);
  }

  .drawer-header-extra {
    padding: var(--space-2) var(--space-4);
    border-bottom: 1px solid var(--border);
    flex-shrink: 0;
  }

  .drawer-content {
    flex: 1;
    overflow-y: auto;
    padding: var(--space-3) var(--space-4);
  }

  .drawer-footer {
    padding: var(--space-3) var(--space-4);
    border-top: 1px solid var(--border);
    flex-shrink: 0;
  }

  @media (max-width: 480px) {
    .drawer {
      width: 100vw !important;
      border-left: none;
    }
    .drawer-close {
      min-width: 44px;
      min-height: 44px;
      display: flex;
      align-items: center;
      justify-content: center;
    }
  }
</style>
