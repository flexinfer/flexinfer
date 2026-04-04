<script>
  import Badge from '../widgets/Badge.svelte';
  import EmptyState from './shared/EmptyState.svelte';

  let status = $state(null);
  let domains = $state(null);
  let history = $state(null);
  let metrics = $state(null);
  let loading = $state(true);
  let error = $state('');
  let pollTimer = $state(null);
  let expandedDomain = $state(null);

  async function fetchAll() {
    try {
      const [sRes, dRes, hRes, mRes] = await Promise.all([
        fetch('/api/weaver/status'),
        fetch('/api/weaver/domains'),
        fetch('/api/weaver/history'),
        fetch('/api/weaver/metrics'),
      ]);
      if (!sRes.ok) throw new Error(`Status: HTTP ${sRes.status}`);
      status = await sRes.json();
      domains = dRes.ok ? await dRes.json() : null;
      history = hRes.ok ? await hRes.json() : null;
      metrics = mRes.ok ? await mRes.json() : null;
      error = '';
    } catch (e) {
      error = e.message ?? 'Failed to fetch weaver data';
    } finally {
      loading = false;
    }
  }

  function startPolling() {
    fetchAll();
    pollTimer = setInterval(fetchAll, 5000);
  }

  function stopPolling() {
    if (pollTimer) {
      clearInterval(pollTimer);
      pollTimer = null;
    }
  }

  $effect(() => {
    startPolling();
    return () => stopPolling();
  });

  // Derived
  let enabled = $derived(status?.enabled ?? false);
  let routerModel = $derived(status?.router_model ?? domains?.router_model ?? '-');
  let subagentModel = $derived(status?.subagent_model ?? domains?.subagent_model ?? '-');
  let domainList = $derived(domains?.domains ?? status?.domains ?? []);
  let entries = $derived(history?.entries ?? []);
  let totalQueries = $derived(metrics?.total_queries ?? 0);
  let avgLatency = $derived(metrics?.avg_latency_ms ?? 0);
  let errorRate = $derived(metrics?.error_rate ?? 0);
  let totalTokens = $derived(metrics?.total_tokens ?? 0);
  let errorCount = $derived(metrics?.error_count ?? 0);

  function toggleDomain(name) {
    expandedDomain = expandedDomain === name ? null : name;
  }

  function statusVariant(s) {
    if (s === 'ok' || s === 'success') return 'success';
    if (s === 'error') return 'critical';
    return 'neutral';
  }

  function fmtLatency(ms) {
    if (ms < 1000) return `${Math.round(ms)}ms`;
    return `${(ms / 1000).toFixed(1)}s`;
  }

  function fmtTime(ts) {
    if (!ts) return '-';
    const d = new Date(ts);
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
  }

  function truncate(str, max) {
    if (!str) return '-';
    return str.length > max ? str.slice(0, max) + '...' : str;
  }
</script>

<div class="weaver-panel">
  <header class="panel-header">
    <h2>Weaver</h2>
    <div class="header-stats">
      <span class="stat">
        <span class="stat-value">{totalQueries}</span>
        <span class="stat-label">queries</span>
      </span>
      <span class="stat">
        <span class="stat-value">{fmtLatency(avgLatency)}</span>
        <span class="stat-label">avg latency</span>
      </span>
      <span class="stat">
        <span class="stat-value">{domainList.length}</span>
        <span class="stat-label">domains</span>
      </span>
    </div>
  </header>

  {#if loading}
    <div class="loading">Loading weaver data...</div>
  {:else if error}
    <div class="error-banner">{error}</div>
  {:else}

    <!-- Status Card -->
    <section class="section">
      <h3>Status</h3>
      <div class="status-grid">
        <div class="status-item">
          <span class="status-label">Enabled</span>
          <Badge variant={enabled ? 'success' : 'neutral'}>{enabled ? 'Active' : 'Disabled'}</Badge>
        </div>
        <div class="status-item">
          <span class="status-label">Router Model</span>
          <span class="status-value text-mono">{routerModel}</span>
        </div>
        <div class="status-item">
          <span class="status-label">Subagent Model</span>
          <span class="status-value text-mono">{subagentModel}</span>
        </div>
        <div class="status-item">
          <span class="status-label">Domains</span>
          <span class="status-value">{domainList.length}</span>
        </div>
      </div>
    </section>

    <!-- Metrics Summary -->
    <section class="section">
      <h3>Metrics</h3>
      <div class="metrics-grid">
        <div class="metric-card">
          <span class="metric-value">{totalQueries}</span>
          <span class="metric-label">Total Queries</span>
        </div>
        <div class="metric-card">
          <span class="metric-value">{fmtLatency(avgLatency)}</span>
          <span class="metric-label">Avg Latency</span>
        </div>
        <div class="metric-card">
          <span class="metric-value" class:error-text={errorRate > 0.1}>{(errorRate * 100).toFixed(1)}%</span>
          <span class="metric-label">Error Rate</span>
        </div>
        <div class="metric-card">
          <span class="metric-value">{totalTokens.toLocaleString()}</span>
          <span class="metric-label">Total Tokens</span>
        </div>
        <div class="metric-card">
          <span class="metric-value" class:error-text={errorCount > 0}>{errorCount}</span>
          <span class="metric-label">Errors</span>
        </div>
      </div>
    </section>

    <!-- Domains Table -->
    <section class="section">
      <h3>Domains</h3>
      {#if domainList.length === 0}
        <EmptyState message="No domains configured" />
      {:else}
        <div class="domains-list">
          {#each domainList as domain}
            <div class="domain-row">
              <button class="domain-header" onclick={() => toggleDomain(domain.name)}>
                <span class="domain-expand">{expandedDomain === domain.name ? '\u25BC' : '\u25B6'}</span>
                <span class="domain-name">{domain.name}</span>
                <span class="domain-desc">{domain.description ?? ''}</span>
                <Badge variant="info">{domain.tools?.length ?? 0} tools</Badge>
              </button>
              {#if expandedDomain === domain.name && domain.tools?.length}
                <div class="domain-tools">
                  {#each domain.tools as tool}
                    <span class="tool-tag">{tool}</span>
                  {/each}
                </div>
              {/if}
            </div>
          {/each}
        </div>
      {/if}
    </section>

    <!-- Query History -->
    <section class="section">
      <h3>Recent Queries</h3>
      {#if entries.length === 0}
        <EmptyState message="No queries recorded yet" />
      {:else}
        <div class="history-list">
          {#each entries as entry}
            <div class="history-row">
              <span class="history-time">{fmtTime(entry.timestamp)}</span>
              <span class="history-query">{truncate(entry.query, 60)}</span>
              <span class="history-domains">
                {#each (entry.domains ?? []) as d}
                  <span class="domain-chip">{d}</span>
                {/each}
              </span>
              <span class="history-latency">{fmtLatency(entry.latency_ms ?? 0)}</span>
              <span class="history-tokens">{(entry.total_tokens ?? 0).toLocaleString()} tok</span>
              <Badge variant={statusVariant(entry.status)}>{entry.status}</Badge>
            </div>
          {/each}
        </div>
      {/if}
    </section>
  {/if}
</div>

<style>
  .weaver-panel {
    display: flex;
    flex-direction: column;
    gap: 1rem;
    padding: 1rem;
  }

  .panel-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
  }

  .panel-header h2 {
    margin: 0;
    font-size: 1.25rem;
  }

  .header-stats {
    display: flex;
    gap: 1rem;
  }

  .stat {
    display: flex;
    flex-direction: column;
    align-items: center;
  }

  .stat-value {
    font-size: 1.1rem;
    font-weight: 600;
  }

  .stat-label {
    font-size: 0.75rem;
    opacity: 0.7;
  }

  .section {
    border: 1px solid var(--border-color, #333);
    border-radius: 8px;
    padding: 0.75rem;
  }

  .section h3 {
    margin: 0 0 0.5rem 0;
    font-size: 0.95rem;
  }

  .loading, .error-banner {
    padding: 1rem;
    text-align: center;
    border-radius: 6px;
  }

  .error-banner {
    background: rgba(220, 38, 38, 0.1);
    color: #ef4444;
  }

  /* Status Grid */
  .status-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 0.5rem;
  }

  .status-item {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 0.4rem 0;
    border-bottom: 1px solid rgba(255,255,255,0.05);
  }

  .status-label { font-size: 0.85rem; opacity: 0.8; }
  .status-value { font-weight: 600; font-size: 0.85rem; }
  .text-mono { font-family: monospace; font-size: 0.8rem; }

  /* Metrics Grid */
  .metrics-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
    gap: 0.75rem;
  }

  .metric-card {
    border: 1px solid var(--border-color, #333);
    border-radius: 6px;
    padding: 0.75rem;
    text-align: center;
    background: var(--card-bg, rgba(255,255,255,0.03));
  }

  .metric-value {
    display: block;
    font-size: 1.25rem;
    font-weight: 700;
    margin-bottom: 0.25rem;
  }

  .metric-label {
    font-size: 0.75rem;
    opacity: 0.7;
  }

  .error-text { color: #ef4444; }

  /* Domains */
  .domains-list {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
  }

  .domain-row {
    border: 1px solid var(--border-color, #333);
    border-radius: 6px;
    overflow: hidden;
  }

  .domain-header {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    width: 100%;
    padding: 0.5rem 0.75rem;
    background: transparent;
    border: none;
    color: inherit;
    cursor: pointer;
    font-size: 0.85rem;
    text-align: left;
  }

  .domain-header:hover {
    background: rgba(255,255,255,0.03);
  }

  .domain-expand {
    font-size: 0.65rem;
    opacity: 0.5;
    width: 1rem;
    flex-shrink: 0;
  }

  .domain-name {
    font-weight: 600;
    min-width: 120px;
  }

  .domain-desc {
    flex: 1;
    opacity: 0.7;
    font-size: 0.8rem;
  }

  .domain-tools {
    padding: 0.5rem 0.75rem 0.75rem 2rem;
    display: flex;
    flex-wrap: wrap;
    gap: 0.35rem;
    border-top: 1px solid rgba(255,255,255,0.05);
  }

  .tool-tag {
    font-size: 0.7rem;
    padding: 0.15rem 0.4rem;
    border-radius: 3px;
    background: rgba(59, 130, 246, 0.15);
    border: 1px solid rgba(59, 130, 246, 0.3);
    font-family: monospace;
  }

  /* History */
  .history-list {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    max-height: 400px;
    overflow-y: auto;
  }

  .history-row {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.4rem 0.5rem;
    border: 1px solid var(--border-color, #333);
    border-radius: 4px;
    font-size: 0.8rem;
  }

  .history-time {
    font-family: monospace;
    font-size: 0.75rem;
    opacity: 0.6;
    min-width: 70px;
    flex-shrink: 0;
  }

  .history-query {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .history-domains {
    display: flex;
    gap: 0.2rem;
    flex-shrink: 0;
  }

  .domain-chip {
    font-size: 0.65rem;
    padding: 0.1rem 0.3rem;
    border-radius: 3px;
    background: rgba(255,255,255,0.08);
  }

  .history-latency {
    font-family: monospace;
    font-size: 0.75rem;
    min-width: 50px;
    text-align: right;
    flex-shrink: 0;
  }

  .history-tokens {
    font-size: 0.7rem;
    opacity: 0.6;
    min-width: 60px;
    text-align: right;
    flex-shrink: 0;
  }
</style>
