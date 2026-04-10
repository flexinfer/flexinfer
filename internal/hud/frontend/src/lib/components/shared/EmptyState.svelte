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
    padding: clamp(36px, 10vh, 84px) var(--space-4);
    color: var(--fg-secondary);
    text-align: center;
    gap: var(--space-3);
    min-height: 220px;
    max-width: 520px;
    margin: 0 auto;
  }

  .empty.compact {
    padding: var(--space-6) var(--space-4);
    min-height: 140px;
    gap: var(--space-2);
  }

  .empty-icon {
    font-size: 34px;
    opacity: 0.9;
    width: 56px;
    height: 56px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border-radius: 50%;
    background: rgba(255, 255, 255, 0.03);
    border: 1px solid var(--border);
    box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.03);
  }

  .empty.compact .empty-icon {
    font-size: 24px;
    width: 40px;
    height: 40px;
  }

  .empty-heading {
    font-size: var(--text-lg);
    font-weight: 600;
    color: var(--fg-primary);
  }

  .empty.compact .empty-heading {
    font-size: var(--text-base);
  }

  .empty-description {
    font-size: var(--text-sm);
    color: var(--fg-secondary);
    max-width: 380px;
    line-height: 1.6;
  }

  .empty-action {
    margin-top: var(--space-2);
  }
</style>
