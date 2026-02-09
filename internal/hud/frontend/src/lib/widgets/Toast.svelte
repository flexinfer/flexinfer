<script>
  import { toastStore } from '../stores/toasts.svelte.ts';

  const typeColors = {
    info: 'var(--info)',
    success: 'var(--success)',
    warning: 'var(--warning)',
    error: 'var(--error)',
  };

  const typeIcons = {
    info: 'ℹ',
    success: '✓',
    warning: '⚠',
    error: '✕',
  };
</script>

{#if toastStore.items.length > 0}
  <div class="toast-container">
    {#each toastStore.items as toast (toast.id)}
      <div
        class="toast"
        style="border-left-color: {typeColors[toast.type]};"
        role="alert"
      >
        <span class="toast-icon" style="color: {typeColors[toast.type]};">{typeIcons[toast.type]}</span>
        <span class="toast-message">{toast.message}</span>
        <button class="toast-dismiss" onclick={() => toastStore.dismiss(toast.id)}>✕</button>
      </div>
    {/each}
  </div>
{/if}

<style>
  .toast-container {
    position: fixed;
    top: 52px;
    right: 16px;
    display: flex;
    flex-direction: column;
    gap: 8px;
    z-index: 1000;
    pointer-events: none;
  }
  .toast {
    pointer-events: auto;
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 12px;
    background: var(--glass-bg);
    backdrop-filter: blur(var(--glass-blur));
    -webkit-backdrop-filter: blur(var(--glass-blur));
    border: 1px solid var(--glass-border);
    border-left: 3px solid var(--info);
    border-radius: var(--border-radius);
    font-size: 12px;
    color: var(--fg-primary);
    min-width: 240px;
    max-width: 380px;
    box-shadow: var(--shadow-md);
    animation: toastSlideIn 0.2s ease-out;
  }
  .toast-icon {
    flex-shrink: 0;
    font-size: 14px;
  }
  .toast-message {
    flex: 1;
  }
  .toast-dismiss {
    flex-shrink: 0;
    font-size: 10px;
    color: var(--fg-muted);
    padding: 2px 4px;
    border-radius: var(--radius-sm);
  }
  .toast-dismiss:hover {
    color: var(--fg-primary);
    background: var(--bg-tertiary);
  }
  @keyframes toastSlideIn {
    from { opacity: 0; transform: translateX(20px); }
    to   { opacity: 1; transform: translateX(0); }
  }
</style>
