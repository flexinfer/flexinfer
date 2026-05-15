<script lang="ts">
  interface Props {
    message: string;
    /** Optional secondary line (e.g. timestamp, source). */
    detail?: string;
    /** Show a retry button bound to this handler. */
    onRetry?: () => void;
    /** Show a dismiss button bound to this handler. */
    onDismiss?: () => void;
    /** Visual emphasis. Defaults to 'error'. */
    tone?: 'error' | 'warning';
    /** Tighter padding for inline use inside lists. */
    compact?: boolean;
  }

  let {
    message,
    detail,
    onRetry,
    onDismiss,
    tone = 'error',
    compact = false,
  }: Props = $props();
</script>

<div
  class="error-card"
  class:tone-warning={tone === 'warning'}
  class:compact
  role="alert"
>
  <div class="error-card-icon" aria-hidden="true">{tone === 'warning' ? '⚠' : '✕'}</div>
  <div class="error-card-body">
    <div class="error-card-message">{message}</div>
    {#if detail}
      <div class="error-card-detail">{detail}</div>
    {/if}
  </div>
  {#if onRetry || onDismiss}
    <div class="error-card-actions">
      {#if onRetry}
        <button type="button" class="error-card-btn" onclick={onRetry}>Retry</button>
      {/if}
      {#if onDismiss}
        <button type="button" class="error-card-btn ghost" onclick={onDismiss}>Dismiss</button>
      {/if}
    </div>
  {/if}
</div>

<style>
  .error-card {
    display: flex;
    align-items: flex-start;
    gap: var(--space-3);
    padding: var(--space-3) var(--space-4);
    background: var(--error-dim);
    border: 1px solid color-mix(in srgb, var(--error) 30%, transparent);
    border-radius: var(--radius-sm);
    font-size: var(--text-sm);
    color: var(--fg-primary);
  }

  .error-card.compact {
    padding: var(--space-2) var(--space-3);
    font-size: var(--text-xs);
  }

  .error-card.tone-warning {
    background: var(--warning-dim);
    border-color: color-mix(in srgb, var(--warning) 30%, transparent);
  }

  .error-card-icon {
    flex-shrink: 0;
    color: var(--error);
    font-family: var(--font-mono);
    font-weight: 700;
    line-height: 1.4;
  }

  .error-card.tone-warning .error-card-icon {
    color: var(--warning);
  }

  .error-card-body {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .error-card-message {
    color: var(--fg-primary);
    line-height: 1.4;
    word-break: break-word;
  }

  .error-card-detail {
    color: var(--fg-muted);
    font-size: var(--text-xs);
    font-family: var(--font-mono);
  }

  .error-card-actions {
    flex-shrink: 0;
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }

  .error-card-btn {
    padding: 4px 10px;
    border: 1px solid color-mix(in srgb, var(--error) 50%, transparent);
    background: transparent;
    color: var(--error);
    border-radius: var(--radius-xs);
    font-size: var(--text-xs);
    font-family: var(--font-mono);
    cursor: pointer;
    transition: background var(--transition-fast), border-color var(--transition-fast);
  }

  .error-card.tone-warning .error-card-btn {
    border-color: color-mix(in srgb, var(--warning) 50%, transparent);
    color: var(--warning);
  }

  .error-card-btn:hover {
    background: color-mix(in srgb, var(--error) 12%, transparent);
  }

  .error-card.tone-warning .error-card-btn:hover {
    background: color-mix(in srgb, var(--warning) 12%, transparent);
  }

  .error-card-btn.ghost {
    border-color: var(--border);
    color: var(--fg-secondary);
  }

  .error-card-btn.ghost:hover {
    border-color: var(--border-active);
    background: var(--bg-tertiary);
    color: var(--fg-primary);
  }
</style>
