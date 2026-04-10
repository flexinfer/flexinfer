<script lang="ts">
  interface Props {
    open: boolean;
    title: string;
    message: string;
    confirmLabel?: string;
    cancelLabel?: string;
    variant?: 'danger' | 'warn' | 'default';
    onConfirm: () => void;
    onCancel: () => void;
  }

  let {
    open,
    title,
    message,
    confirmLabel = 'Confirm',
    cancelLabel = 'Cancel',
    variant = 'default',
    onConfirm,
    onCancel,
  }: Props = $props();

  function handleKeydown(event: KeyboardEvent): void {
    if (event.key === 'Escape') {
      onCancel();
    }
  }

  function handleBackdropClick(event: MouseEvent): void {
    if ((event.target as HTMLElement)?.classList?.contains('confirm-backdrop')) {
      onCancel();
    }
  }
</script>

{#if open}
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="confirm-backdrop" onkeydown={handleKeydown} onclick={handleBackdropClick}>
    <div class="confirm-dialog" role="alertdialog" aria-modal="true" aria-labelledby="confirm-title">
      <div class="confirm-title" id="confirm-title">{title}</div>
      <div class="confirm-message">{message}</div>
      <div class="confirm-actions">
        <button type="button" class="confirm-cancel" onclick={onCancel}>
          {cancelLabel}
        </button>
        <button
          type="button"
          class="confirm-btn"
          class:danger={variant === 'danger'}
          class:warn={variant === 'warn'}
          onclick={onConfirm}
        >
          {confirmLabel}
        </button>
      </div>
    </div>
  </div>
{/if}

<style>
  .confirm-backdrop {
    position: fixed;
    inset: 0;
    z-index: 9999;
    display: flex;
    align-items: center;
    justify-content: center;
    background: rgba(0, 0, 0, 0.6);
    backdrop-filter: blur(2px);
  }

  .confirm-dialog {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    max-width: 400px;
    width: 90%;
    padding: var(--space-4);
    background: var(--bg-primary);
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    box-shadow: 0 8px 32px rgba(0, 0, 0, 0.4);
  }

  .confirm-title {
    font-size: var(--text-base);
    font-weight: 600;
    color: var(--fg-primary);
  }

  .confirm-message {
    font-size: var(--text-sm);
    color: var(--fg-secondary);
    line-height: 1.5;
  }

  .confirm-actions {
    display: flex;
    justify-content: flex-end;
    gap: var(--space-2);
    margin-top: var(--space-1);
  }

  .confirm-cancel,
  .confirm-btn {
    padding: var(--space-1) var(--space-3);
    border-radius: var(--radius-xs);
    font-size: var(--text-sm);
    font-family: var(--font-mono);
    cursor: pointer;
    transition: background var(--transition-fast), border-color var(--transition-fast);
  }

  .confirm-cancel {
    background: transparent;
    border: 1px solid var(--border);
    color: var(--fg-secondary);
  }

  .confirm-cancel:hover {
    border-color: var(--border-active);
    color: var(--fg-primary);
  }

  .confirm-btn {
    background: transparent;
    border: 1px solid var(--border-focus);
    color: var(--accent);
  }

  .confirm-btn:hover {
    background: rgba(129, 240, 254, 0.1);
  }

  .confirm-btn.danger {
    border-color: rgba(255, 61, 113, 0.4);
    color: var(--error);
  }

  .confirm-btn.danger:hover {
    background: var(--error-dim);
    border-color: var(--error);
  }

  .confirm-btn.warn {
    border-color: rgba(245, 158, 11, 0.4);
    color: var(--color-warn, #f59e0b);
  }

  .confirm-btn.warn:hover {
    background: rgba(245, 158, 11, 0.1);
    border-color: var(--color-warn, #f59e0b);
  }
</style>
