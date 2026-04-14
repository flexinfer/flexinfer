<script>
  import { traceStore } from '../stores/traces.svelte.ts';
  import EmptyState from './shared/EmptyState.svelte';
  import Badge from '../widgets/Badge.svelte';
  import { formatTime } from '../utils/format.ts';

  $effect(() => {
    traceStore.startPolling(15000);
    return () => traceStore.stopPolling();
  });

  let query = $state('');
  let statusFilter = $state('all');

  let entries = $derived(traceStore.entries ?? []);
  let summary = $derived(traceStore.summary ?? {});

  let filtered = $derived.by(() => {
    let rows = entries;
    if (statusFilter !== 'all') {
      rows = rows.filter((entry) => {
        if (statusFilter === 'cached') return !!entry.cached;
        return entry.status === statusFilter;
      });
    }
    if (!query.trim()) return rows;
    const q = query.trim().toLowerCase();
    return rows.filter((entry) =>
      entry.server.toLowerCase().includes(q) ||
      entry.tool.toLowerCase().includes(q) ||
      (entry.agent_id ?? '').toLowerCase().includes(q) ||
      (entry.error ?? '').toLowerCase().includes(q),
    );
  });

  const statusChips = [
    { value: 'all', label: 'All' },
    { value: 'error', label: 'Errors' },
    { value: 'denied', label: 'Denied' },
    { value: 'cached', label: 'Cached' },
    { value: 'success', label: 'Success' },
  ];

  function statusVariant(status) {
    if (status === 'error') return 'error';
    if (status === 'denied') return 'warning';
    return 'success';
  }

  function formatDuration(ms) {
    if (!ms) return '0ms';
    if (ms < 1) return '<1ms';
    return `${Math.round(ms)}ms`;
  }

  function breakdown(entry) {
    const parts = [];
    if (entry.route_ms) parts.push(`route ${formatDuration(entry.route_ms)}`);
    if (entry.build_ms) parts.push(`build ${formatDuration(entry.build_ms)}`);
    if (entry.execute_ms) parts.push(`exec ${formatDuration(entry.execute_ms)}`);
    if (entry.send_ms) parts.push(`send ${formatDuration(entry.send_ms)}`);
    if (entry.recv_ms) parts.push(`recv ${formatDuration(entry.recv_ms)}`);
    return parts.join(' · ');
  }
</script>

<div class="panel traces-panel">
  <div class="traces-header">
    <div>
      <div class="panel-title">Tool Call Traces</div>
      <div class="panel-subtitle">Recent daemon-routed calls from the audit stream.</div>
    </div>
    <div class="summary-strip">
      <div class="summary-card">
        <span class="summary-label">P50</span>
        <strong>{formatDuration(summary.p50_ms ?? 0)}</strong>
      </div>
      <div class="summary-card">
        <span class="summary-label">P95</span>
        <strong>{formatDuration(summary.p95_ms ?? 0)}</strong>
      </div>
      <div class="summary-card">
        <span class="summary-label">Errors</span>
        <strong>{summary.errors ?? 0}</strong>
      </div>
      <div class="summary-card">
        <span class="summary-label">Slowest</span>
        <strong>{formatDuration(summary.slowest_ms ?? 0)}</strong>
      </div>
    </div>
  </div>

  <div class="traces-toolbar">
    <input
      type="text"
      class="panel-search-input"
      placeholder="Filter traces..."
      bind:value={query}
      data-panel-search="primary"
    />
    <div class="chip-row">
      {#each statusChips as chip}
        <button
          class="filter-chip"
          class:active={statusFilter === chip.value}
          onclick={() => { statusFilter = chip.value; }}
        >
          {chip.label}
        </button>
      {/each}
    </div>
  </div>

  {#if !traceStore.enabled}
    <EmptyState icon={'\u25A6'} heading="Trace stream unavailable" description="Enable daemon audit logging to populate recent tool-call traces." />
  {:else if filtered.length === 0}
    <EmptyState icon={'\u25A6'} heading="No traces matched" compact />
  {:else}
    <div class="trace-list">
      {#each filtered as entry, i (`${entry.timestamp}-${entry.server}-${entry.tool}-${i}`)}
        <div class="trace-row">
          <div class="trace-row-top">
            <div class="trace-id">
              <span class="trace-time">{formatTime(entry.timestamp)}</span>
              <span class="trace-server">{entry.server}</span>
              <span class="trace-tool">{entry.tool}</span>
            </div>
            <div class="trace-badges">
              <span class="trace-duration">{formatDuration(entry.duration_ms)}</span>
              <Badge text={entry.status} variant={statusVariant(entry.status)} />
              {#if entry.cached}
                <Badge text="cached" variant="info" />
              {/if}
              {#if entry.target}
                <Badge text={entry.target} variant="accent" />
              {/if}
            </div>
          </div>
          <div class="trace-row-meta">
            {#if entry.agent_id}
              <span class="meta-chip">{entry.agent_id}</span>
            {/if}
            {#if entry.pipeline_stage}
              <span class="meta-chip">{entry.pipeline_stage}</span>
            {/if}
            {#if breakdown(entry)}
              <span class="meta-chip breakdown">{breakdown(entry)}</span>
            {/if}
          </div>
          {#if entry.error}
            <div class="trace-error">{entry.error}</div>
          {/if}
        </div>
      {/each}
    </div>
  {/if}

  {#if traceStore.path}
    <div class="trace-footer">Source: {traceStore.path}</div>
  {/if}
</div>

<style>
  .traces-panel {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    min-height: 0;
  }

  .traces-header {
    display: flex;
    justify-content: space-between;
    gap: var(--space-3);
    align-items: flex-start;
    flex-wrap: wrap;
  }

  .panel-title {
    font-size: var(--text-sm);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
  }

  .panel-subtitle {
    color: var(--fg-muted);
    font-size: var(--text-sm);
    margin-top: 4px;
  }

  .summary-strip {
    display: flex;
    gap: var(--space-2);
    flex-wrap: wrap;
  }

  .summary-card {
    min-width: 88px;
    padding: 10px 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    background: color-mix(in srgb, var(--bg-secondary) 86%, transparent);
  }

  .summary-label {
    display: block;
    color: var(--fg-muted);
    font-size: var(--text-xs);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    margin-bottom: 4px;
  }

  .traces-toolbar {
    display: flex;
    gap: var(--space-2);
    align-items: center;
    flex-wrap: wrap;
  }

  .chip-row {
    display: flex;
    gap: var(--space-1);
    flex-wrap: wrap;
  }

  .filter-chip {
    padding: 6px 10px;
    border-radius: var(--radius-full);
    border: 1px solid var(--border-subtle);
    background: var(--bg-secondary);
    color: var(--fg-muted);
    font-size: var(--text-xs);
  }

  .filter-chip.active {
    color: var(--fg-primary);
    border-color: color-mix(in srgb, var(--info) 30%, var(--border));
    background: color-mix(in srgb, var(--info) 10%, var(--bg-tertiary));
  }

  .trace-list {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    overflow: auto;
    min-height: 0;
  }

  .trace-row {
    padding: 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    background: color-mix(in srgb, var(--bg-secondary) 88%, transparent);
  }

  .trace-row-top {
    display: flex;
    justify-content: space-between;
    gap: var(--space-2);
    flex-wrap: wrap;
    align-items: center;
  }

  .trace-id,
  .trace-badges,
  .trace-row-meta {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
    align-items: center;
  }

  .trace-time,
  .trace-duration,
  .trace-server,
  .meta-chip,
  .trace-footer {
    font-family: var(--font-mono);
    font-size: var(--text-xs);
  }

  .trace-tool {
    font-size: var(--text-sm);
    font-weight: 600;
  }

  .trace-server {
    color: var(--fg-secondary);
  }

  .trace-row-meta {
    margin-top: 8px;
  }

  .meta-chip {
    padding: 2px 6px;
    border-radius: var(--radius-sm);
    background: var(--bg-tertiary);
    border: 1px solid var(--border-subtle);
    color: var(--fg-secondary);
  }

  .meta-chip.breakdown {
    white-space: normal;
  }

  .trace-error {
    margin-top: 8px;
    color: var(--error);
    font-size: var(--text-sm);
  }

  .trace-footer {
    color: var(--fg-dim);
  }
</style>
