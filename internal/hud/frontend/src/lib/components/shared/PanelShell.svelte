<script>
  /**
   * PanelShell — standard panel wrapper providing consistent header,
   * optional filter bar, scrollable content area, and empty state fallback.
   *
   * @type {{
   *   title: string,
   *   icon?: string,
   *   count?: number | null,
   *   loading?: boolean,
   *   empty?: boolean,
   *   emptyIcon?: string,
   *   emptyMessage?: string,
   *   emptyHint?: string,
   *   header?: import('svelte').Snippet,
   *   toolbar?: import('svelte').Snippet,
   *   actions?: import('svelte').Snippet,
   *   emptyAction?: import('svelte').Snippet,
   *   children: import('svelte').Snippet,
   * }}
   */
  let {
    title,
    icon = '',
    count = null,
    loading = false,
    empty = false,
    emptyIcon = '\u25A1',
    emptyMessage = 'No data yet',
    emptyHint = '',
    header,
    toolbar,
    actions,
    emptyAction,
    children,
  } = $props();
</script>

<section class="panel-shell" aria-label={title}>
  <!-- Panel header -->
  <div class="panel-shell-header">
    <div class="panel-shell-title-row">
      {#if icon}
        <span class="panel-shell-icon">{icon}</span>
      {/if}
      <h2 class="panel-shell-title">{title}</h2>
      {#if count != null}
        <span class="panel-shell-count">{count}</span>
      {/if}
    </div>
    {#if actions}
      <div class="panel-shell-actions">
        {@render actions()}
      </div>
    {/if}
  </div>

  <!-- Optional header slot (extra info below title) -->
  {#if header}
    <div class="panel-shell-header-extra">
      {@render header()}
    </div>
  {/if}

  <!-- Optional toolbar/filter bar -->
  {#if toolbar}
    <div class="panel-shell-toolbar">
      {@render toolbar()}
    </div>
  {/if}

  <!-- Loading bar -->
  {#if loading}
    <div class="loading-bar">
      <div class="loading-bar-inner"></div>
    </div>
  {/if}

  <!-- Content area -->
  <div class="panel-shell-content">
    {#if empty && !loading}
      <div class="empty-state">
        <div class="empty-state-icon">{emptyIcon}</div>
        <div class="empty-state-message">{emptyMessage}</div>
        {#if emptyHint}
          <div class="empty-state-hint">{emptyHint}</div>
        {/if}
        {#if emptyAction}
          <div class="empty-state-action">
            {@render emptyAction()}
          </div>
        {/if}
      </div>
    {:else}
      {@render children()}
    {/if}
  </div>
</section>

<style>
  .panel-shell {
    display: flex;
    flex-direction: column;
    flex: 1;
    overflow: hidden;
    padding: var(--panel-padding);
  }

  .panel-shell-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: var(--space-4);
    flex-shrink: 0;
    gap: var(--space-3);
  }

  .panel-shell-title-row {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }

  .panel-shell-icon {
    font-size: var(--text-lg);
    color: var(--fg-muted);
  }

  .panel-shell-title {
    font-size: clamp(18px, 1.7vw, 24px);
    font-weight: 700;
    color: var(--fg-primary);
    margin: 0;
    letter-spacing: var(--tracking-tight);
  }

  .panel-shell-count {
    font-size: var(--text-xs);
    font-family: var(--font-mono);
    font-weight: 600;
    color: var(--fg-secondary);
    background: var(--bg-tertiary);
    padding: 2px 8px;
    border-radius: var(--radius-full);
    border: 1px solid var(--border);
  }

  .panel-shell-actions {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }

  .panel-shell-header-extra {
    margin-bottom: var(--space-4);
    flex-shrink: 0;
    padding: var(--space-3) var(--space-4);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-lg);
    background:
      linear-gradient(180deg, rgba(255, 255, 255, 0.02), transparent),
      color-mix(in srgb, var(--bg-secondary) 92%, transparent);
  }

  .panel-shell-toolbar {
    flex-shrink: 0;
    margin-bottom: var(--space-4);
    padding: var(--space-2);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-lg);
    background:
      linear-gradient(180deg, rgba(255, 255, 255, 0.02), transparent),
      color-mix(in srgb, var(--bg-secondary) 92%, transparent);
  }

  .panel-shell-content {
    flex: 1;
    overflow-y: auto;
    overflow-x: hidden;
  }

  .empty-state {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    padding: clamp(40px, 12vh, 96px) var(--space-4);
    color: var(--fg-muted);
    text-align: center;
    gap: var(--space-3);
    min-height: 260px;
    border: 1px dashed color-mix(in srgb, var(--border-focus) 40%, var(--border));
    border-radius: var(--radius-xl);
    background:
      radial-gradient(circle at top, rgba(0, 200, 255, 0.05), transparent 45%),
      color-mix(in srgb, var(--bg-secondary) 80%, transparent);
  }

  .empty-state-icon {
    font-size: 32px;
    opacity: 0.85;
    width: 56px;
    height: 56px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border-radius: 50%;
    background: rgba(255, 255, 255, 0.03);
    border: 1px solid var(--border);
  }

  .empty-state-message {
    font-size: var(--text-lg);
    font-weight: 600;
    color: var(--fg-primary);
  }

  .empty-state-hint {
    font-size: var(--text-sm);
    opacity: 0.85;
    max-width: 420px;
    line-height: 1.6;
  }

  .empty-state-action {
    margin-top: var(--space-2);
  }
</style>
