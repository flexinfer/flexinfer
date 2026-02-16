<script>
  import { healthStore } from '../stores/health.svelte.ts';
  import StatusDot from '../widgets/StatusDot.svelte';
  import SparkLine from '../widgets/SparkLine.svelte';
  import Badge from '../widgets/Badge.svelte';
  import FilterBar from './shared/FilterBar.svelte';
  import DataTable from './shared/DataTable.svelte';
  import DetailDrawer from './shared/DetailDrawer.svelte';
  import EmptyState from './shared/EmptyState.svelte';

  $effect(() => {
    healthStore.startPolling(5000);
    return () => { healthStore.stopPolling(); };
  });

  let servers = $derived(healthStore.servers ?? []);

  // --- Tunnel + Cache state ---
  let tunnels = $state([]);
  let cacheStats = $state(null);
  let infraLoading = $state(false);

  async function fetchInfraStats() {
    infraLoading = true;
    const [t, c] = await Promise.all([
      healthStore.fetchTunnels(),
      healthStore.fetchCacheStats(),
    ]);
    tunnels = t;
    cacheStats = c;
    infraLoading = false;
  }

  $effect(() => {
    fetchInfraStats();
    const timer = setInterval(fetchInfraStats, 30000);
    return () => clearInterval(timer);
  });

  // --- Filters & Sort ---
  let searchQuery = $state('');
  let categoryFilter = $state('');
  let statusFilter = $state('');
  let sortKey = $state('name');
  let sortDir = $state('asc');
  let selectedServer = $state(null);

  let categoryOptions = $derived.by(() => {
    const cats = new Set();
    servers.forEach(s => {
      (s.categories ?? []).forEach(c => cats.add(c));
    });
    return Array.from(cats).sort().map(c => ({ value: c, label: c }));
  });

  let filterDefs = $derived([
    {
      key: 'category',
      label: 'All Categories',
      value: categoryFilter,
      options: categoryOptions,
    },
    {
      key: 'status',
      label: 'All Status',
      value: statusFilter,
      options: [
        { value: 'healthy', label: 'Running' },
        { value: 'idle', label: 'Idle' },
        { value: 'degraded', label: 'Degraded' },
        { value: 'down', label: 'Down' },
      ],
    },
  ]);

  function handleSearch(val) {
    searchQuery = val;
  }

  function handleFilter(key, val) {
    if (key === 'category') categoryFilter = val;
    else if (key === 'status') statusFilter = val;
  }

  function clearFilters() {
    searchQuery = '';
    categoryFilter = '';
    statusFilter = '';
  }

  let hasActiveFilters = $derived(
    searchQuery.trim() !== '' || categoryFilter !== '' || statusFilter !== ''
  );

  let healthyCt = $derived(servers.filter(s => s.status === 'healthy').length);
  let idleCt = $derived(servers.filter(s => s.status === 'idle').length);
  let degradedCt = $derived(servers.filter(s => s.status === 'degraded').length);
  let downCt = $derived(servers.filter(s => s.status === 'down').length);
  let totalTools = $derived(servers.reduce((sum, s) => sum + (s.tool_count ?? 0), 0));

  let filtered = $derived.by(() => {
    let result = servers;

    if (searchQuery.trim()) {
      const q = searchQuery.toLowerCase().trim();
      result = result.filter(s =>
        (s.name ?? '').toLowerCase().includes(q) ||
        (s.description ?? '').toLowerCase().includes(q)
      );
    }

    if (categoryFilter) {
      result = result.filter(s => (s.categories ?? []).includes(categoryFilter));
    }

    if (statusFilter) {
      result = result.filter(s => s.status === statusFilter);
    }

    return result;
  });

  // Sorted rows for DataTable
  const STATUS_SORT_ORDER = { healthy: 0, idle: 1, degraded: 2, down: 3 };

  let sorted = $derived.by(() => {
    const rows = [...filtered];
    rows.sort((a, b) => {
      let av, bv;
      if (sortKey === 'status') {
        av = STATUS_SORT_ORDER[a.status] ?? 9;
        bv = STATUS_SORT_ORDER[b.status] ?? 9;
      } else {
        av = a[sortKey] ?? '';
        bv = b[sortKey] ?? '';
      }
      let cmp;
      if (typeof av === 'number' && typeof bv === 'number') {
        cmp = av - bv;
      } else {
        cmp = String(av).toLowerCase().localeCompare(String(bv).toLowerCase());
      }
      return sortDir === 'desc' ? -cmp : cmp;
    });
    return rows;
  });

  function handleSort(key, dir) {
    sortKey = key;
    sortDir = dir;
  }

  // DataTable column definitions
  const columns = [
    { key: 'name', label: 'Server', sortable: true },
    { key: 'status', label: 'Status', sortable: true, width: '80px' },
    { key: 'latency', label: 'Latency', sortable: true, width: '80px' },
    { key: 'tool_count', label: 'Tools', sortable: true, width: '60px' },
    { key: 'target', label: 'Target' },
    { key: 'sparkline', label: 'Sparkline', width: '130px' },
  ];

  function selectServer(server) {
    selectedServer = selectedServer?.name === server.name ? null : server;
  }

  function formatLatency(ms) {
    if (ms == null) return '---';
    if (ms < 1) return '<1ms';
    return ms.toFixed(0) + 'ms';
  }

  function tunnelStateVariant(state) {
    if (state === 'connected') return 'success';
    if (state === 'connecting' || state === 'reconnecting') return 'warning';
    return 'error';
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
      <span class="header-stat tools-stat">
        <span class="tools-icon">{'\u2699'}</span>
        {totalTools} tools
      </span>
    </div>
  </div>

  <!-- Filter row (shared component) -->
  <FilterBar
    search={searchQuery}
    placeholder="Search servers..."
    filters={filterDefs}
    resultCount={filtered.length}
    onSearch={handleSearch}
    onFilter={handleFilter}
  />

  <!-- Sortable table -->
  <div class="table-container">
    {#if filtered.length === 0 && healthStore.lastUpdated}
      <EmptyState
        icon={'\u2665'}
        heading="No servers match filters"
        description="Try adjusting your search or filter criteria."
        compact
      >
        {#snippet action()}
          {#if hasActiveFilters}
            <button class="btn btn-ghost" onclick={clearFilters}>Clear filters</button>
          {/if}
        {/snippet}
      </EmptyState>
    {:else}
      <DataTable
        {columns}
        rows={sorted}
        {sortKey}
        {sortDir}
        loading={!healthStore.lastUpdated}
        skeletonRows={5}
        idKey="name"
        onSort={handleSort}
        onRowClick={selectServer}
      >
        {#snippet row({ row: server })}
          <td class="text-mono server-name">{server.name}</td>
          <td>
            <StatusDot status={server.status ?? 'unknown'} />
          </td>
          <td class="text-mono">{#key server.latency}<span class="data-updated">{formatLatency(server.latency)}</span>{/key}</td>
          <td class="text-mono">{server.tool_count ?? 0}</td>
          <td class="text-mono text-muted target-cell">{server.target ?? '---'}</td>
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
        {/snippet}
      </DataTable>
    {/if}
  </div>

  <!-- Infrastructure cards row: Tunnels + Cache -->
  <div class="infra-cards">
    <!-- Tunnels Card -->
    <div class="infra-card">
      <div class="infra-card-header">
        <span class="infra-card-title">SSH Tunnels</span>
        {#if tunnels.length > 0}
          <Badge text="{tunnels.length} active" variant="info" />
        {:else}
          <Badge text="none" variant="info" />
        {/if}
      </div>
      <div class="infra-card-body">
        {#if tunnels.length > 0}
          <div class="tunnel-list">
            {#each tunnels as tunnel}
              <div class="tunnel-row">
                <StatusDot status={tunnel.state === 'connected' ? 'healthy' : tunnel.state === 'connecting' ? 'degraded' : 'down'} />
                <span class="text-mono tunnel-name">{tunnel.name}</span>
                <span class="text-muted text-xs">{tunnel.remote_host}</span>
                <Badge text={tunnel.state} variant={tunnelStateVariant(tunnel.state)} />
                {#if tunnel.uptime}
                  <span class="text-muted text-xs">up {tunnel.uptime}</span>
                {/if}
                {#if tunnel.reconnects > 0}
                  <span class="text-xs reconnect-count">{'\u21BB'} {tunnel.reconnects}</span>
                {/if}
              </div>
            {/each}
          </div>
        {:else}
          <span class="text-muted text-xs">No active tunnels</span>
        {/if}
      </div>
    </div>

    <!-- Cache Stats Card -->
    <div class="infra-card">
      <div class="infra-card-header">
        <span class="infra-card-title">Response Cache</span>
        {#if cacheStats}
          <Badge text="{cacheStats.entries} entries" variant="info" />
        {/if}
      </div>
      <div class="infra-card-body">
        {#if cacheStats}
          <div class="cache-grid">
            <div class="cache-stat">
              <span class="cache-stat-value text-mono">{cacheStats.entries}</span>
              <span class="cache-stat-label">Entries</span>
            </div>
            {#if cacheStats.size}
              <div class="cache-stat">
                <span class="cache-stat-value text-mono">{cacheStats.size}</span>
                <span class="cache-stat-label">Size</span>
              </div>
            {/if}
            <div class="cache-stat">
              <span class="cache-stat-value text-mono" style:color={cacheStats.hit_rate > 0.5 ? 'var(--success)' : 'var(--fg-secondary)'}>{(cacheStats.hit_rate * 100).toFixed(1)}%</span>
              <span class="cache-stat-label">Hit Rate</span>
            </div>
          </div>
        {:else if infraLoading}
          <span class="text-muted text-xs">Loading...</span>
        {:else}
          <span class="text-muted text-xs">Unavailable</span>
        {/if}
      </div>
    </div>
  </div>


</div>

<!-- Server Detail Drawer -->
<DetailDrawer
  open={!!selectedServer}
  title={selectedServer?.name ?? ''}
  subtitle={selectedServer?.target ?? ''}
  onClose={() => { selectedServer = null; }}
>
  {#snippet header()}
    {#if selectedServer}
      <div class="detail-stats">
        <div class="stat-chip">
          <StatusDot status={selectedServer.status ?? 'unknown'} />
          <span class="stat-chip-label">{selectedServer.status ?? 'unknown'}</span>
        </div>
        <div class="stat-chip">
          <span class="stat-chip-value">{selectedServer.tool_count ?? 0}</span>
          <span class="stat-chip-label">tools</span>
        </div>
        {#if selectedServer.latency != null}
          <div class="stat-chip">
            <span class="stat-chip-value">{formatLatency(selectedServer.latency)}</span>
            <span class="stat-chip-label">latency</span>
          </div>
        {/if}
      </div>
    {/if}
  {/snippet}

  {#if selectedServer}
    {#if selectedServer.description}
      <div class="section">
        <div class="section-title text-xs uppercase text-muted">Description</div>
        <p class="text-sm text-secondary">{selectedServer.description}</p>
      </div>
    {/if}
    {#if selectedServer.categories?.length}
      <div class="section">
        <div class="section-title text-xs uppercase text-muted">Categories</div>
        <div class="detail-cats">
          {#each selectedServer.categories as cat}
            <span class="cat-chip">{cat}</span>
          {/each}
        </div>
      </div>
    {/if}
    {#if selectedServer.latencyHistory?.length}
      <div class="section">
        <div class="section-title text-xs uppercase text-muted">Latency History</div>
        <SparkLine
          data={selectedServer.latencyHistory}
          width={340}
          height={60}
          color={selectedServer.status === 'healthy' ? 'var(--success)' : 'var(--warning)'}
        />
      </div>
    {/if}
    {#if selectedServer.error_message}
      <div class="detail-error">
        <span class="error-label">ERROR:</span> {selectedServer.error_message}
      </div>
    {/if}
  {/if}
</DetailDrawer>

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

  .tools-icon {
    font-size: 11px;
  }

  .table-container {
    flex: 1;
    overflow-y: auto;
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--border-radius);
  }

  .server-name {
    color: var(--fg-primary);
    font-weight: 500;
  }

  .target-cell {
    max-width: 200px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .sparkline-cell {
    width: 130px;
    padding: 4px 10px;
  }

  /* --- Infrastructure Cards --- */

  .infra-cards {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 12px;
    margin-top: 12px;
  }

  .infra-card {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--border-radius);
    padding: 12px 16px;
  }

  .infra-card-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 8px;
  }

  .infra-card-title {
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    color: var(--fg-muted);
  }

  .infra-card-body {
    font-size: 12px;
  }

  .tunnel-list {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .tunnel-row {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .tunnel-name {
    font-size: 12px;
    font-weight: 500;
    color: var(--fg-primary);
  }

  .reconnect-count {
    color: var(--warning);
    font-family: var(--font-mono);
  }

  .cache-grid {
    display: flex;
    gap: 24px;
  }

  .cache-stat {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 2px;
  }

  .cache-stat-value {
    font-size: 18px;
    font-weight: 600;
    color: var(--fg-primary);
  }

  .cache-stat-label {
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.3px;
    color: var(--fg-muted);
  }

  /* --- Detail Drawer (shared classes in theme.css) --- */

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
    border-radius: var(--radius-sm);
    color: var(--fg-secondary);
  }

  .detail-error {
    color: var(--error);
    margin-top: 6px;
    font-family: var(--font-mono);
    font-size: 11px;
    padding: 6px 8px;
    background: rgba(230, 30, 63, 0.08);
    border-radius: var(--radius-sm);
    border: 1px solid rgba(230, 30, 63, 0.2);
  }

  .error-label {
    font-weight: 700;
  }
</style>
