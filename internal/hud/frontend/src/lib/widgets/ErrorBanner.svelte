<script>
  let { message = '', onDismiss = null } = $props();
</script>

{#if message}
  <div class="error-banner">
    <span class="error-icon">⚠</span>
    <span class="error-text">{message}</span>
    {#if onDismiss}
      <button class="error-dismiss" onclick={onDismiss}>✕</button>
    {/if}
  </div>
{/if}

<style>
  .error-banner {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 12px;
    background: rgba(230, 30, 63, 0.1);
    border: 1px solid rgba(230, 30, 63, 0.3);
    border-radius: var(--border-radius);
    font-size: 12px;
    color: var(--error);
    flex-shrink: 0;
  }
  .error-icon {
    flex-shrink: 0;
  }
  .error-text {
    flex: 1;
  }
  .error-dismiss {
    flex-shrink: 0;
    font-size: 12px;
    color: var(--error);
    opacity: 0.7;
    padding: 2px 4px;
    border-radius: var(--radius-sm);
  }
  .error-dismiss:hover {
    opacity: 1;
    background: rgba(230, 30, 63, 0.15);
  }
</style>
