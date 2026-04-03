<script>
  import { otelStore } from '../stores/otel.svelte.ts';
  import Badge from '../widgets/Badge.svelte';

  $effect(() => {
    otelStore.startPolling(60000);
    return () => otelStore.stopPolling();
  });

  let surfaces = $derived(Object.entries(otelStore.surfaces));
</script>

<section class="otel-status">
  <h3>OpenTelemetry</h3>

  {#if otelStore.loading && !otelStore.data}
    <p class="muted">Loading...</p>
  {:else if otelStore.error}
    <p class="error">{otelStore.error}</p>
  {:else if otelStore.data}
    <div class="status-grid">
      <div class="status-row">
        <span class="label">Endpoint</span>
        <span class="value">{otelStore.endpoint || 'not configured'}</span>
      </div>
      <div class="status-row">
        <span class="label">Protocol</span>
        <span class="value">{otelStore.protocol || 'n/a'}</span>
      </div>
      <div class="status-row">
        <span class="label">Traces</span>
        <span class="value">
          {#if otelStore.enabled}
            <Badge variant="success">enabled</Badge>
          {:else}
            <Badge variant="muted">disabled</Badge>
          {/if}
        </span>
      </div>
      <div class="status-row">
        <span class="label">Metrics</span>
        <span class="value">
          {#if otelStore.meterEnabled}
            <Badge variant="success">enabled</Badge>
          {:else}
            <Badge variant="muted">disabled</Badge>
          {/if}
        </span>
      </div>
      <div class="status-row">
        <span class="label">Sample Rate</span>
        <span class="value">{(otelStore.sampleRate * 100).toFixed(0)}%</span>
      </div>
      <div class="status-row">
        <span class="label">Trace Coverage</span>
        <span class="value">{otelStore.traceCoverage}</span>
      </div>
    </div>

    {#if surfaces.length > 0}
      <details class="surfaces">
        <summary>Trace Surfaces ({otelStore.surfaceCount}/{otelStore.totalSurfaces})</summary>
        <ul>
          {#each surfaces as [name, enabled]}
            <li class:active={enabled} class:inactive={!enabled}>
              <span class="dot" class:green={enabled} class:gray={!enabled}></span>
              {name.replace(/_/g, ' ')}
            </li>
          {/each}
        </ul>
      </details>
    {/if}
  {:else}
    <p class="muted">No OTel data available</p>
  {/if}
</section>

<style>
  .otel-status {
    padding: 0.5rem 0;
  }
  h3 {
    margin: 0 0 0.5rem;
    font-size: 0.85rem;
    font-weight: 600;
    color: var(--text-primary, #e0e0e0);
  }
  .status-grid {
    display: grid;
    gap: 0.25rem;
  }
  .status-row {
    display: flex;
    justify-content: space-between;
    align-items: center;
    font-size: 0.8rem;
    padding: 0.15rem 0;
  }
  .label {
    color: var(--text-secondary, #999);
  }
  .value {
    color: var(--text-primary, #e0e0e0);
    font-family: var(--font-mono, monospace);
    font-size: 0.75rem;
  }
  .surfaces {
    margin-top: 0.5rem;
    font-size: 0.8rem;
  }
  .surfaces summary {
    cursor: pointer;
    color: var(--text-secondary, #999);
  }
  .surfaces ul {
    list-style: none;
    padding: 0.25rem 0 0 0.5rem;
    margin: 0;
  }
  .surfaces li {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    padding: 0.1rem 0;
    font-size: 0.75rem;
  }
  .dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    flex-shrink: 0;
  }
  .dot.green { background: var(--color-success, #4caf50); }
  .dot.gray { background: var(--color-muted, #666); }
  .active { color: var(--text-primary, #e0e0e0); }
  .inactive { color: var(--text-secondary, #999); }
  .muted { color: var(--text-secondary, #999); font-size: 0.8rem; }
  .error { color: var(--color-error, #ff5252); font-size: 0.8rem; }
</style>
