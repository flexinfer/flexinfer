<script>
  let { open = false, title = '', onClose = () => {}, children } = $props();

  function handleBackdropClick(e) {
    if (e.target === e.currentTarget) onClose();
  }

  function handleKeydown(e) {
    if (e.key === 'Escape') onClose();
  }
</script>

<svelte:window onkeydown={handleKeydown} />

{#if open}
  <div
    class="modal-backdrop"
    role="button"
    tabindex="0"
    aria-label="Close modal"
    onclick={handleBackdropClick}
    onkeydown={(e) => {
      if (e.key === 'Escape' || e.key === 'Enter' || e.key === ' ') handleBackdropClick(e);
    }}
  >
    <div class="modal" role="dialog" aria-modal="true" aria-label={title}>
      <div class="modal-header">
        <h3 class="modal-title">{title}</h3>
        <button class="modal-close" onclick={onClose}>✕</button>
      </div>
      <div class="modal-body">
        {@render children?.()}
      </div>
    </div>
  </div>
{/if}

<style>
  .modal-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.5);
    backdrop-filter: blur(8px);
    -webkit-backdrop-filter: blur(8px);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 900;
    animation: backdropFadeIn 0.15s ease;
  }
  .modal {
    background: var(--glass-bg);
    backdrop-filter: blur(var(--glass-blur));
    -webkit-backdrop-filter: blur(var(--glass-blur));
    border: 1px solid var(--glass-border);
    border-radius: var(--radius-lg);
    min-width: 380px;
    max-width: 540px;
    max-height: 80vh;
    display: flex;
    flex-direction: column;
    box-shadow: var(--shadow-lg);
    animation: modalSlideIn 0.2s ease-out;
  }
  .modal-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 12px 16px;
    border-bottom: 1px solid var(--border);
  }
  .modal-title {
    font-size: 14px;
    font-weight: 600;
    color: var(--fg-primary);
  }
  .modal-close {
    font-size: 14px;
    color: var(--fg-muted);
    padding: 4px 6px;
    border-radius: var(--radius-sm);
  }
  .modal-close:hover {
    color: var(--fg-primary);
    background: var(--bg-tertiary);
  }
  .modal-body {
    padding: 16px;
    overflow-y: auto;
  }
  @keyframes backdropFadeIn {
    from { opacity: 0; }
    to   { opacity: 1; }
  }
  @keyframes modalSlideIn {
    from { opacity: 0; transform: scale(0.95) translateY(-10px); }
    to   { opacity: 1; transform: scale(1) translateY(0); }
  }
</style>
