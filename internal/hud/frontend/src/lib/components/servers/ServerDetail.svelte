<script lang="ts">
  /**
   * ServerDetail — drill-down drawer body for a single server.
   *
   * @type {{
   *   server: import('../../stores/health.svelte.ts').MergedServer | null,
   *   onClose: () => void,
   * }}
   */
  let { server, onClose } = $props();

  import { daemonMetricsStore } from '../../stores/daemonMetrics.svelte.ts';
  import StatusDot from '../../widgets/StatusDot.svelte';
  import SparkLine from '../../widgets/SparkLine.svelte';
  import DetailDrawer from '../shared/DetailDrawer.svelte';
  import { sanitizeText } from '../../utils/format.ts';
  import { formatLatency, percentile } from '../../utils/serversHelpers';

  let metricsMap = $derived(daemonMetricsStore.byName);
</script>

<DetailDrawer
  open={!!server}
  title={sanitizeText(server?.name ?? '')}
  subtitle={sanitizeText(server?.transport ?? '')}
  {onClose}
>
  {#snippet header()}
    {#if server}
      <div class="detail-stats">
        <div class="stat-chip">
          <StatusDot status={server.status ?? 'unknown'} />
          <span class="stat-chip-label">{server.status ?? 'unknown'}</span>
        </div>
        <div class="stat-chip">
          <span class="stat-chip-value">{server.tool_count ?? 0}</span>
          <span class="stat-chip-label">tools</span>
        </div>
        {#if server.latency != null}
          <div class="stat-chip">
            <span class="stat-chip-value">{formatLatency(server.latency)}</span>
            <span class="stat-chip-label">latency</span>
          </div>
        {/if}
      </div>
    {/if}
  {/snippet}

  {#if server}
    {#if server.description}
      <div class="section">
        <div class="section-title text-xs uppercase text-muted">Description</div>
        <p class="text-sm text-secondary">{sanitizeText(server.description)}</p>
      </div>
    {/if}
    {#if server.categories?.length}
      <div class="section">
        <div class="section-title text-xs uppercase text-muted">Categories</div>
        <div class="detail-cats">
          {#each server.categories as cat}
            <span class="cat-chip">{sanitizeText(cat)}</span>
          {/each}
        </div>
      </div>
    {/if}
    {#if server.latencyHistory?.length}
      <div class="section">
        <div class="section-title text-xs uppercase text-muted">Latency History</div>
        <SparkLine
          data={server.latencyHistory}
          width={340}
          height={60}
          color={server.status === 'healthy' ? 'var(--success)' : 'var(--warning)'}
        />
        <div class="percentile-row">
          <span class="percentile-chip">p50: {formatLatency(percentile(server.latencyHistory, 50))}</span>
          <span class="percentile-chip">p95: {formatLatency(percentile(server.latencyHistory, 95))}</span>
          <span class="percentile-chip">p99: {formatLatency(percentile(server.latencyHistory, 99))}</span>
        </div>
      </div>
    {/if}
    {#if metricsMap.get(server.name)}
      {@const sm = metricsMap.get(server.name)}
      <div class="section">
        <div class="section-title text-xs uppercase text-muted">Daemon Metrics</div>
        <div class="percentile-row">
          <span class="percentile-chip">reqs: {sm.request_count}</span>
          <span class="percentile-chip" class:text-error={sm.error_count > 0}>errors: {sm.error_count}</span>
          <span class="percentile-chip">p50: {formatLatency(sm.p50_ms)}</span>
          <span class="percentile-chip">p95: {formatLatency(sm.p95_ms)}</span>
          <span class="percentile-chip">p99: {formatLatency(sm.p99_ms)}</span>
        </div>
      </div>
    {/if}
    {#if server.consec_fails > 0}
      <div class="detail-error">
        <span class="error-label">CONSECUTIVE FAILURES:</span> {server.consec_fails}
      </div>
    {/if}
    {#if server.error_message}
      <div class="detail-error">
        <span class="error-label">ERROR:</span> {sanitizeText(server.error_message)}
      </div>
    {/if}
  {/if}
</DetailDrawer>

<style>
  .detail-cats {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
    margin-bottom: 6px;
  }

  .cat-chip {
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    padding: 2px 6px;
    background: var(--bg-tertiary);
    border-radius: var(--radius-sm);
    color: var(--fg-secondary);
    border: 1px solid var(--border-subtle);
  }

  .detail-error {
    color: var(--error);
    margin-top: 6px;
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    padding: 6px var(--space-2);
    background: var(--error-dim);
    border-radius: var(--radius-sm);
    border: 1px solid rgba(255, 61, 113, 0.2);
  }

  .error-label {
    font-weight: 700;
    letter-spacing: var(--tracking-wide);
  }

  .percentile-row {
    display: flex;
    gap: var(--space-2);
    margin-top: 6px;
  }

  .percentile-chip {
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    color: var(--fg-secondary);
    padding: 2px 6px;
    background: var(--bg-tertiary);
    border-radius: var(--radius-sm);
    border: 1px solid var(--border-subtle);
  }

  .text-error { color: var(--error); }
</style>
