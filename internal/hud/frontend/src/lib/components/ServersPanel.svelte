<script>
  import { healthStore } from '../stores/health.svelte.ts';
  import StatusDot from '../widgets/StatusDot.svelte';
  import SparkLine from '../widgets/SparkLine.svelte';

  $effect(() => {
    healthStore.startPolling(5000);
    return () => { healthStore.stopPolling(); };
  });

  let servers = $derived(healthStore.servers ?? []);

  let searchQuery = $state('');
  let categoryFilter = $state('all');
  let statusFilter = $state('all');
  let sortColumn = $state('name');
  let sortAsc = $state(true);
  let selectedServer = $state(null);

  let categories = $derived.by(() => {
    const cats = new Set();
    servers.forEach(s => {
      (s.categories ?? []).forEach(c => cats.add(c));
    });
    return ['all', ...Array.from(cats).sort()];
  });

  let healthyCt = $derived(servers.filter(s => s.status === 'healthy').length);
  let idleCt = $derived(servers.filter(s => s.status === 'idle').length);
  let degradedCt = $derived(servers.filter(s => s.status === 'degraded').length);
  let downCt = $derived(servers.filter(s => s.status === 'down').length);

  let filtered = $derived.by(() => {
    let result = servers;

    if (searchQuery.trim()) {
      const q = searchQuery.toLowerCase().trim();
      result = result.filter(s =>
        (s.name ?? '').toLowerCase().includes(q) ||
        (s.description ?? '').toLowerCase().includes(q)
      );
    }

    if (categoryFilter !== 'all') {
      result = result.filter(s => (s.categories ?? []).includes(categoryFilter));
    }

    if (statusFilter !== 'all') {
      result = result.filter(s => s.status === statusFilter);
    }

    result = [...result].sort((a, b) => {
      let av = a[sortColumn] ?? '';
      let bv = b[sortColumn] ?? '';
      if (typeof av === 'number' && typeof bv === 'number') {
        return sortAsc ? av - bv : bv - av;
      }
      av = String(av).toLowerCase();
      bv = String(bv).toLowerCase();
      if (av < bv) return sortAsc ? -1 : 1;
      if (av > bv) return sortAsc ? 1 : -1;
      return 0;
    });

    return result;
  });

  function toggleSort(col) {
    if (sortColumn === col) {
      sortAsc = !sortAsc;
    } else {
      sortColumn = col;
      sortAsc = true;
    }
  }

  function sortIndicator(col) {
    if (sortColumn !== col) return '';
    return sortAsc ? ' \u25B2' : ' \u25BC';
  }

  function selectServer(server) {
    selectedServer = selectedServer?.name === server.name ? null : server;
  }

  function formatLatency(ms) {
    if (ms == null) return '---';
    if (ms < 1) return '<1ms';
    return ms.toFixed(0) + 'ms';
  }

  function formatRate(rate) {
    if (rate == null) return '---';
    return rate.toFixed(1);
  }

  function formatPct(pct) {
    if (pct == null) return '---';
    return pct.toFixed(2) + '%';
  }

  function errColor(pct) {
    if (pct == null) return 'var(--fg-muted)';
    if (pct >= 5) return 'var(--error)';
    if (pct >= 1) return 'var(--warning)';
    return 'var(--success)';
  }
</script>

<div class="panel servers-panel">
  <!-- Header bar -->
  <div class="header-bar">
    <div class="header-stats">
      <span class="header-total text-mono">{servers.length} servers</span>
      <span class="header-stat healthy-stat">
        <span class="dot dot-healthy"></span>
        {healthyCt} running
      </span>
      <span class="header-stat idle-stat">
        <span class="dot dot-idle"></span>
        {idleCt} idle
      </span>
      {#if degradedCt > 0}
        <span class="header-stat degraded-stat">
          <span class="dot dot-degraded"></span>
          {degradedCt} degraded
        </span>
      {/if}
      {#if downCt > 0}
        <span class="header-stat down-stat">
          <span class="dot dot-down"></span>
          {downCt} down
        </span>
      {/if}
    </div>
  </div>

  <!-- Filter row -->
  <div class="toolbar">
    <input
      type="text"
      placeholder="Search servers..."
      bind:value={searchQuery}
      class="search-input"
    />
    <select bind:value={categoryFilter}>
      {#each categories as cat}
        <option value={cat}>{cat === 'all' ? 'All Categories' : cat}</option>
      {/each}
    </select>
    <select bind:value={statusFilter}>
      <option value="all">All Status</option>
      <option value="healthy">Running</option>
      <option value="idle">Idle</option>
      <option value="degraded">Degraded</option>
      <option value="down">Down</option>
    </select>
    <div class="toolbar-spacer"></div>
    <span class="text-muted text-xs text-mono">{filtered.length} results</span>
  </div>

  <!-- Sortable table -->
  <div class="table-container">
    <div class="table-wrap">
      <table>
        <thead>
          <tr>
            <th class="sortable" onclick={() => toggleSort('name')}>
              Server{sortIndicator('name')}
            </th>
            <th class="sortable" onclick={() => toggleSort('status')}>
              Status{sortIndicator('status')}
            </th>
            <th class="sortable" onclick={() => toggleSort('latency')}>
              Latency{sortIndicator('latency')}
            </th>
            <th class="sortable" onclick={() => toggleSort('req_rate')}>
              Req/s{sortIndicator('req_rate')}
            </th>
            <th class="sortable" onclick={() => toggleSort('error_rate')}>
              Err%{sortIndicator('error_rate')}
            </th>
            <th class="sortable" onclick={() => toggleSort('tool_count')}>
              Tools{sortIndicator('tool_count')}
            </th>
            <th>Target</th>
            <th>Sparkline</th>
          </tr>
        </thead>
        <tbody>
          {#each filtered as server (server.name)}
            <tr
              class="server-row"
              class:selected={selectedServer?.name === server.name}
              onclick={() => selectServer(server)}
            >
              <td class="text-mono server-name">{server.name}</td>
              <td>
                <StatusDot status={server.status ?? 'unknown'} />
              </td>
              <td class="text-mono">{formatLatency(server.latency)}</td>
              <td class="text-mono">{formatRate(server.req_rate)}</td>
              <td class="text-mono" style="color: {errColor(server.error_rate)}">
                {formatPct(server.error_rate)}
              </td>
              <td class="text-mono">{server.tool_count ?? 0}</td>
              <td class="text-mono text-muted">{server.target ?? '---'}</td>
              <td class="sparkline-cell">
                {#if server.latencyHistory?.length}
                  <SparkLine
                    data={server.latencyHistory}
                    width={120}
                    height={24}
                    color={server.status === 'healthy' ? 'var(--success)' : server.status === 'degraded' ? 'var(--warning)' : 'var(--error)'}
                  />
                {:else}
                  <span class="text-muted text-xs">no data</span>
                {/if}
              </td>
            </tr>
          {:else}
            <tr>
              <td colspan="8" class="empty-cell">No servers match filters</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  </div>

  <!-- Footer: selected server detail -->
  {#if selectedServer}
    <div class="detail-footer">
      <div class="detail-header">
        <span class="detail-name text-mono">{selectedServer.name}</span>
        <StatusDot status={selectedServer.status ?? 'unknown'} />
      </div>
      {#if selectedServer.description}
        <p class="detail-description">{selectedServer.description}</p>
      {/if}
      {#if selectedServer.categories?.length}
        <div class="detail-cats">
          {#each selectedServer.categories as cat}
            <span class="cat-chip">{cat}</span>
          {/each}
        </div>
      {/if}
      {#if selectedServer.error_message}
        <div class="detail-error">
          <span class="error-label">ERROR:</span> {selectedServer.error_message}
        </div>
      {/if}
    </div>
  {/if}
</div>

<style>
  .servers-panel {
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  .header-bar {
    padding: 8px 0;
    border-bottom: 1px solid var(--border);
    margin-bottom: 0;
  }

  .header-stats {
    display: flex;
    align-items: center;
    gap: 16px;
    font-size: 12px;
  }

  .header-total {
    font-weight: 600;
    color: var(--fg-primary);
  }

  .header-stat {
    display: flex;
    align-items: center;
    gap: 4px;
    color: var(--fg-secondary);
  }

  .dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
  }

  .dot-healthy { background: var(--success); }
  .dot-idle { background: var(--fg-muted); }
  .dot-degraded { background: var(--warning); }
  .dot-down { background: var(--error); }

  .search-input {
    width: 200px;
  }

  .table-container {
    flex: 1;
    overflow-y: auto;
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--border-radius);
  }

  .sortable {
    cursor: pointer;
    user-select: none;
  }

  .sortable:hover {
    color: var(--fg-primary);
  }

  .server-row {
    cursor: pointer;
    transition: background 0.1s ease;
  }

  .server-row.selected td {
    background: rgba(88, 166, 255, 0.08);
    border-color: rgba(88, 166, 255, 0.2);
  }

  .server-name {
    color: var(--fg-primary);
    font-weight: 500;
  }

  .sparkline-cell {
    width: 130px;
    padding: 4px 10px;
  }

  .empty-cell {
    text-align: center;
    color: var(--fg-muted);
    padding: 32px 10px !important;
  }

  .detail-footer {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--border-radius);
    padding: 12px 16px;
    margin-top: 12px;
    font-size: 12px;
  }

  .detail-header {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 6px;
  }

  .detail-name {
    font-weight: 600;
    color: var(--fg-primary);
    font-size: 14px;
  }

  .detail-description {
    color: var(--fg-secondary);
    margin-bottom: 6px;
    line-height: 1.4;
  }

  .detail-cats {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
    margin-bottom: 6px;
  }

  .cat-chip {
    font-family: var(--font-mono);
    font-size: 10px;
    padding: 2px 6px;
    background: var(--bg-tertiary);
    border-radius: 4px;
    color: var(--fg-secondary);
  }

  .detail-error {
    color: var(--error);
    margin-top: 6px;
    font-family: var(--font-mono);
    font-size: 11px;
    padding: 6px 8px;
    background: rgba(248, 81, 73, 0.08);
    border-radius: 4px;
    border: 1px solid rgba(248, 81, 73, 0.2);
  }

  .error-label {
    font-weight: 700;
  }
</style>
