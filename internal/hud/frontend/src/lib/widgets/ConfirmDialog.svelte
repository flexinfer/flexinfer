<script>
  import Modal from './Modal.svelte';

  let {
    open = false,
    title = 'Confirm',
    message = 'Are you sure?',
    confirmLabel = 'Confirm',
    cancelLabel = 'Cancel',
    destructive = false,
    onConfirm = () => {},
    onCancel = () => {},
  } = $props();
</script>

<Modal {open} {title} onClose={onCancel}>
  <div class="confirm-content">
    <p class="confirm-message">{message}</p>
    <div class="confirm-actions">
      <button class="btn btn-ghost" onclick={onCancel}>{cancelLabel}</button>
      <button
        class="btn {destructive ? 'btn-danger' : 'btn-primary'}"
        onclick={onConfirm}
      >
        {confirmLabel}
      </button>
    </div>
  </div>
</Modal>

<style>
  .confirm-content {
    display: flex;
    flex-direction: column;
    gap: 16px;
  }
  .confirm-message {
    font-size: 13px;
    color: var(--fg-secondary);
    line-height: 1.5;
  }
  .confirm-actions {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
  }
</style>
