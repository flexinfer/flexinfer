<script>
  import { eventStore } from '../stores/events.svelte.ts';
  import { stalenessStore } from '../stores/staleness.svelte.ts';

  /**
   * ConnectionBanner shows daemon connection state below the header.
   * Hidden when connected AND no stores are stale (zero chrome when healthy).
   *
   * States:
   *   reconnecting   (amber)  — SSE dropped, retry in progress
   *   disconnected   (red)    — SSE is down
   *   circuit-open   (amber)  — repeated reconnect failures, backing off
   *   stale          (amber)  — SSE connected but no snapshot from one or
   *                             more registered stores within staleAfter
   *                             (Slice B3 — catches silent SSE failures)
   */
  let state = $derived(eventStore.connectionState);
  let retryIn = $derived(eventStore.retryCountdown);
  let staleStores = $derived(stalenessStore.staleStores);
  let connectionVisible = $derived(state !== 'connected');
  let staleVisible = $derived(!connectionVisible && staleStores.length > 0);
  let visible = $derived(connectionVisible || staleVisible);
</script>

{#if visible}
  <div
    class="connection-banner"
    class:connection-reconnecting={state === 'reconnecting'}
    class:connection-disconnected={state === 'disconnected'}
    class:connection-circuit-open={state === 'circuit-open'}
    class:connection-stale={staleVisible}
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
    {:else if staleVisible}
      <span class="banner-icon">◷</span>
      <span class="banner-text">Stale data — no recent updates from {staleStores.join(', ')}</span>
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
