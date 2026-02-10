<script>
  import { eventStore } from '../stores/events.svelte.ts';

  /**
   * ConnectionBanner shows daemon connection state below the header.
   * Hidden when connected (zero chrome when healthy).
   * States: reconnecting (amber), disconnected (red), circuit-open (amber).
   */
  let state = $derived(eventStore.connectionState);
  let retryIn = $derived(eventStore.retryCountdown);
  let visible = $derived(state !== 'connected');
</script>

{#if visible}
  <div
    class="connection-banner"
    class:connection-reconnecting={state === 'reconnecting'}
    class:connection-disconnected={state === 'disconnected'}
    class:connection-circuit-open={state === 'circuit-open'}
    role="status"
    aria-live="polite"
  >
    {#if state === 'reconnecting'}
      <span class="banner-icon">⟳</span>
      <span class="banner-text">Reconnecting<span class="animated-dots"></span></span>
    {:else if state === 'disconnected'}
      <span class="banner-icon">⚠</span>
      <span class="banner-text">Disconnected from daemon</span>
      {#if retryIn > 0}
        <span class="banner-countdown">Retry in {retryIn}s</span>
      {/if}
    {:else if state === 'circuit-open'}
      <span class="banner-icon">◉</span>
      <span class="banner-text">Circuit breaker open — cooling down</span>
      {#if retryIn > 0}
        <span class="banner-countdown">{retryIn}s</span>
      {/if}
    {/if}
  </div>
{/if}

<style>
  .connection-banner {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 4px 12px;
    font-size: 11px;
    flex-shrink: 0;
    z-index: 99;
    animation: fadeIn 0.2s ease-out;
  }

  .banner-icon {
    font-size: 12px;
    flex-shrink: 0;
  }

  .banner-text {
    flex: 1;
    font-weight: 500;
  }

  .banner-countdown {
    font-family: var(--font-mono);
    font-size: 10px;
    opacity: 0.8;
  }
</style>
