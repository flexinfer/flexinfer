<script>
  /**
   * EmptyState — consistent empty state display with icon, heading,
   * description, and optional action button.
   *
   * @type {{
   *   icon?: string,
   *   heading?: string,
   *   description?: string,
   *   compact?: boolean,
   *   action?: import('svelte').Snippet,
   * }}
   */
  let {
    icon = '\u25A1',
    heading = 'No data yet',
    description = '',
    compact = false,
    action,
  } = $props();
</script>

<div class="empty" class:compact aria-label={heading}>
  <div class="empty-icon">{icon}</div>
  <div class="empty-heading">{heading}</div>
  {#if description}
    <div class="empty-description">{description}</div>
  {/if}
  {#if action}
    <div class="empty-action">
      {@render action()}
    </div>
  {/if}
</div>

<style>
  .empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    padding: var(--space-12) var(--space-4);
    color: var(--fg-muted);
    text-align: center;
    gap: var(--space-3);
    min-height: 200px;
  }

  .empty.compact {
    padding: var(--space-6) var(--space-4);
    min-height: 100px;
    gap: var(--space-2);
  }

  .empty-icon {
    font-size: 32px;
    opacity: 0.4;
  }

  .empty.compact .empty-icon {
    font-size: 24px;
  }

  .empty-heading {
    font-size: var(--text-base);
    font-weight: 500;
  }

  .empty.compact .empty-heading {
    font-size: var(--text-sm);
  }

  .empty-description {
    font-size: var(--text-sm);
    opacity: 0.6;
    max-width: 320px;
    line-height: var(--leading-normal);
  }

  .empty-action {
    margin-top: var(--space-2);
  }
</style>
