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
    gap: var(--space-4);
    padding: var(--space-4);
  }

  .panel-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    position: relative;
  }

  .panel-header::after {
    content: '';
    position: absolute;
    bottom: 0;
    left: 10%;
    right: 10%;
    height: 1px;
    background: linear-gradient(90deg, transparent, rgba(0, 200, 255, 0.06) 50%, transparent);
    pointer-events: none;
  }

  .panel-header h2 {
    margin: 0;
    font-size: var(--text-xl);
    letter-spacing: var(--tracking-tight);
  }

  .header-stats {
    display: flex;
    gap: var(--space-4);
  }

  .stat {
    display: flex;
    flex-direction: column;
    align-items: center;
  }

  .stat-value {
    font-size: var(--text-lg);
    font-weight: 600;
    font-family: var(--font-mono);
  }

  .stat-label {
    font-size: var(--text-sm);
    color: var(--fg-dim);
    letter-spacing: var(--tracking-wide);
    text-transform: uppercase;
  }

  .section {
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    padding: var(--space-3);
    position: relative;
  }

  .section::before {
    content: '';
    position: absolute;
    inset: 0;
    border-radius: inherit;
    background: var(--surface-highlight);
    pointer-events: none;
  }

  .section h3 {
    margin: 0 0 var(--space-2) 0;
    font-size: var(--text-base);
    letter-spacing: var(--tracking-normal);
  }

  .loading, .error-banner {
    padding: var(--space-4);
    text-align: center;
    border-radius: var(--radius-sm);
  }

  .error-banner {
    background: var(--error-dim);
    color: var(--error);
    border: 1px solid rgba(255, 61, 113, 0.2);
  }

  /* Status Grid */
  .status-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: var(--space-2);
  }

  .status-item {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 6px 0;
    border-bottom: 1px solid var(--border-subtle);
  }

  .status-label {
    font-size: var(--text-sm);
    color: var(--fg-secondary);
    letter-spacing: var(--tracking-normal);
  }

  .status-value {
    font-weight: 600;
    font-size: var(--text-sm);
    color: var(--fg-primary);
  }

  .text-mono {
    font-family: var(--font-mono);
    font-size: var(--text-sm);
  }

  /* Metrics Grid */
  .metrics-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
    gap: var(--space-3);
  }

  .metric-card {
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    padding: var(--space-3);
    text-align: center;
    background: var(--bg-secondary);
    position: relative;
    transition: border-color var(--transition-fast);
  }

  .metric-card::before {
    content: '';
    position: absolute;
    inset: 0;
    border-radius: inherit;
    background: var(--surface-highlight);
    pointer-events: none;
  }

  .metric-value {
    display: block;
    font-size: var(--text-xl);
    font-weight: 700;
    font-family: var(--font-mono);
    margin-bottom: var(--space-1);
    color: var(--fg-primary);
  }

  .metric-label {
    font-size: var(--text-sm);
    color: var(--fg-dim);
    letter-spacing: var(--tracking-wide);
    text-transform: uppercase;
  }

  .error-text { color: var(--error); }

  /* Domains */
  .domains-list {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
  }

  .domain-row {
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    overflow: hidden;
    transition: border-color var(--transition-fast);
  }

  .domain-header {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    width: 100%;
    padding: var(--space-2) var(--space-3);
    background: transparent;
    border: none;
    color: inherit;
    cursor: pointer;
    font-size: var(--text-sm);
    text-align: left;
    transition: background var(--transition-fast);
    letter-spacing: var(--tracking-normal);
  }

  .domain-header:hover {
    background: var(--bg-elevated);
  }

  .domain-expand {
    font-size: var(--text-2xs);
    color: var(--fg-muted);
    width: 1rem;
    flex-shrink: 0;
  }

  .domain-name {
    font-weight: 600;
    min-width: 120px;
    color: var(--fg-primary);
  }

  .domain-desc {
    flex: 1;
    color: var(--fg-secondary);
    font-size: var(--text-sm);
  }

  .domain-tools {
    padding: var(--space-2) var(--space-3) var(--space-3) 2rem;
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-1);
    border-top: 1px solid var(--border-subtle);
  }

  .tool-tag {
    font-size: var(--text-xs);
    padding: 2px 6px;
    border-radius: var(--radius-xs);
    background: var(--info-dim);
    border: 1px solid rgba(0, 200, 255, 0.18);
    font-family: var(--font-mono);
    color: var(--fg-secondary);
    letter-spacing: var(--tracking-normal);
  }

  /* History */
  .history-list {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
    max-height: 400px;
    overflow-y: auto;
  }

  .history-row {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    padding: 6px var(--space-2);
    border: 1px solid var(--border);
    border-radius: var(--radius-xs);
    font-size: var(--text-sm);
    transition: background var(--transition-fast);
    letter-spacing: var(--tracking-normal);
  }

  .history-row:hover {
    background: var(--bg-elevated);
  }

  .history-time {
    font-family: var(--font-mono);
    font-size: var(--text-sm);
    color: var(--fg-dim);
    min-width: 70px;
    flex-shrink: 0;
  }

  .history-query {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--fg-primary);
  }

  .history-domains {
    display: flex;
    gap: var(--space-1);
    flex-shrink: 0;
  }

  .domain-chip {
    font-size: var(--text-2xs);
    padding: 1px 5px;
    border-radius: var(--radius-xs);
    background: var(--bg-tertiary);
    color: var(--fg-secondary);
    font-family: var(--font-mono);
  }

  .history-latency {
    font-family: var(--font-mono);
    font-size: var(--text-sm);
    color: var(--fg-secondary);
    min-width: 50px;
    text-align: right;
    flex-shrink: 0;
  }

  .history-tokens {
    font-size: var(--text-xs);
    color: var(--fg-dim);
    min-width: 60px;
    text-align: right;
    flex-shrink: 0;
    font-family: var(--font-mono);
  }
</style>
