<script lang="ts">
  /**
   * RequestMetricsCard — request-count + latency-percentiles table from
   * the daemon prometheus metrics. Reads daemonMetricsStore directly.
   */
  import { daemonMetricsStore } from '../../stores/daemonMetrics.svelte.ts';
  import Badge from '../../widgets/Badge.svelte';
  import { formatLatency } from '../../utils/serversHelpers';
</script>

{#if daemonMetricsStore.servers.length > 0}
  <div class="infra-cards">
    <div class="infra-card infra-card-wide">
      <div class="infra-card-header">
        <span class="infra-card-title">Request Metrics</span>
        <Badge text="{daemonMetricsStore.totalRequests} reqs" variant="info" />
        {#if daemonMetricsStore.overallErrorRate > 0.01}
          <Badge text="{(daemonMetricsStore.overallErrorRate * 100).toFixed(1)}% errors" variant="error" />
        {/if}
      </div>
      <div class="infra-card-body">
        <div class="metrics-table-wrap">
          <table class="metrics-table">
            <thead>
              <tr>
                <th>Server</th>
                <th>Requests</th>
                <th>Errors</th>
                <th>p50</th>
                <th>p95</th>
                <th>p99</th>
                <th>In-Flight</th>
              </tr>
            </thead>
            <tbody>
              {#each daemonMetricsStore.servers as m (m.name)}
                <tr class:has-errors={m.error_count > 0}>
                  <td class="text-mono">{m.name}</td>
                  <td class="text-mono">{m.request_count}</td>
                  <td class="text-mono" class:text-error={m.error_count > 0}>{m.error_count || '—'}</td>
                  <td class="text-mono">{formatLatency(m.p50_ms)}</td>
                  <td class="text-mono">{formatLatency(m.p95_ms)}</td>
                  <td class="text-mono" class:text-warning={m.p99_ms > 5000}>{formatLatency(m.p99_ms)}</td>
                  <td class="text-mono">{m.in_flight || '—'}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  </div>
{/if}

<style>
  .infra-cards {
    display: grid;
    grid-template-columns: 1fr;
    gap: var(--space-3);
  }

  .infra-card {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    padding: var(--space-3) var(--space-4);
    position: relative;
  }

  .infra-card::before {
    content: '';
    position: absolute;
    inset: 0;
    border-radius: inherit;
    background: var(--surface-highlight);
    pointer-events: none;
  }

  .infra-card-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: var(--space-2);
  }

  .infra-card-title {
    font-size: var(--text-xs);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-muted);
  }

  .infra-card-body {
    font-size: var(--text-sm);
  }

  .infra-card-wide {
    width: 100%;
  }

  .metrics-table-wrap {
    overflow-x: auto;
  }

  .metrics-table {
    width: 100%;
    border-collapse: collapse;
    font-size: var(--text-xs);
  }

  .metrics-table th {
    text-align: left;
    padding: 4px var(--space-2);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-muted);
    border-bottom: 1px solid var(--border);
    white-space: nowrap;
  }

  .metrics-table td {
    padding: 4px var(--space-2);
    border-bottom: 1px solid var(--border-subtle);
    color: var(--fg-secondary);
    white-space: nowrap;
  }

  .metrics-table tr:hover {
    background: var(--bg-tertiary);
  }

  .metrics-table tr.has-errors {
    border-left: 2px solid var(--error);
  }

  .text-error { color: var(--error); }
  .text-warning { color: var(--warning); }
</style>
