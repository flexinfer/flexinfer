<script>
  import { catalogStore } from '../stores/catalog.svelte.ts';
  import PanelShell from './shared/PanelShell.svelte';
  import FilterBar from './shared/FilterBar.svelte';
  import MetricCard from './shared/MetricCard.svelte';

  $effect(() => {
    catalogStore.startPolling(30000);
    return () => { catalogStore.stopPolling(); };
  });

  let servers = $derived(catalogStore.filteredServers);
  let sortKey = $state('name');
  let sortDir = $state('asc');
  let viewMode = $state('table');
  let expandedServer = $state('');
  let hasActiveFilters = $derived(
    catalogStore.searchQuery.trim() !== '' ||
    catalogStore.categoryFilter !== 'all' ||
    catalogStore.statusFilter !== 'all'
  );

  let sorted = $derived.by(() => {
    const items = [...servers];
    items.sort((a, b) => {
      let cmp = 0;
      if (sortKey === 'name') cmp = a.name.localeCompare(b.name);
      else if (sortKey === 'enabled') cmp = (a.enabled === b.enabled ? 0 : a.enabled ? -1 : 1);
      else if (sortKey === 'running') cmp = (a.running === b.running ? 0 : a.running ? -1 : 1);
      return sortDir === 'asc' ? cmp : -cmp;
    });
    return items;
  });

  let categoryOptions = $derived(
    catalogStore.categories.map((c) => ({ value: c, label: c }))
  );

  let filters = $derived([
    {
      key: 'category',
      label: 'Category',
      options: categoryOptions,
      value: catalogStore.categoryFilter === 'all' ? '' : catalogStore.categoryFilter,
    },
  ]);

  const statusChips = [
    { value: 'all', label: 'All' },
    { value: 'enabled', label: 'Enabled' },
    { value: 'disabled', label: 'Disabled' },
    { value: 'running', label: 'Running' },
  ];

  function handleSearch(val) {
    catalogStore.search(val);
  }

  function handleFilter(key, value) {
    if (key === 'category') {
      catalogStore.filterByCategory(value || 'all');
    }
  }

  function clearFilters() {
    catalogStore.resetFilters();
  }

  function handleSort(key) {
    if (sortKey === key) {
      sortDir = sortDir === 'asc' ? 'desc' : 'asc';
    } else {
      sortKey = key;
      sortDir = 'asc';
    }
  }

  function toggleExpand(name) {
    expandedServer = expandedServer === name ? '' : name;
  }

  async function toggleServer(name, currentEnabled) {
    await catalogStore.toggleServer(name, !currentEnabled);
  }

  function toggleConsequence(srv) {
    if (srv.enabled) {
      return srv.running ? 'Disabling will stop this running server' : 'Disabling will prevent this server from starting';
    }
    return 'Enabling will allow this server to accept connections';
  }

  function hintSummary(values) {
    return (values ?? []).join(' · ') || 'None';
  }
</script>

<PanelShell
  title="Server Catalog"
  icon={'\u2630'}
  count={catalogStore.servers.length}
  loading={catalogStore.loading}
  empty={sorted.length === 0}
  emptyIcon={'\u25A1'}
  emptyMessage={hasActiveFilters ? 'No servers match the current filters' : 'No servers registered'}
  emptyHint={hasActiveFilters ? 'Clear search or category filters to broaden the catalog' : 'Check the registry path or sync the catalog'}
>
  {#snippet header()}
    <div class="catalog-header">
      <div class="catalog-metrics">
        <MetricCard label="Total" value={catalogStore.servers.length} compact />
        <MetricCard label="Enabled" value={catalogStore.enabledCount} color="var(--success)" compact />
        <MetricCard label="Disabled" value={catalogStore.disabledCount} color="var(--fg-muted)" compact />
        <MetricCard label="Running" value={catalogStore.runningCount} color="var(--info)" badge={catalogStore.runningCount === catalogStore.enabledCount ? 'all up' : ''} badgeVariant="success" compact />
      </div>
    </div>
  {/snippet}

  {#snippet toolbar()}
    <div class="catalog-toolbar">
      <FilterBar
        search={catalogStore.searchQuery}
        placeholder="Search servers by name, description, or category\u2026"
        {filters}
        resultCount={sorted.length}
        onSearch={handleSearch}
        onFilter={handleFilter}
        onClear={clearFilters}
      />
      <div class="catalog-controls">
        <div class="status-chips">
          {#each statusChips as chip (chip.value)}
            <button
              class="status-chip"
              class:active={catalogStore.statusFilter === chip.value}
              onclick={() => catalogStore.filterByStatus(chip.value)}
            >
              {chip.label}
              {#if chip.value === 'enabled'}<span class="chip-count">{catalogStore.enabledCount}</span>{/if}
              {#if chip.value === 'disabled'}<span class="chip-count">{catalogStore.disabledCount}</span>{/if}
              {#if chip.value === 'running'}<span class="chip-count">{catalogStore.runningCount}</span>{/if}
            </button>
          {/each}
        </div>
        <div class="view-toggle">
          <button class="view-btn" class:active={viewMode === 'table'} onclick={() => { viewMode = 'table'; }} title="Table view">{'\u2630'}</button>
          <button class="view-btn" class:active={viewMode === 'cards'} onclick={() => { viewMode = 'cards'; }} title="Card view">{'\u25A3'}</button>
        </div>
      </div>
    </div>
  {/snippet}

  {#snippet emptyAction()}
    {#if hasActiveFilters}
      <button class="btn btn-ghost btn-sm" onclick={clearFilters}>Clear filters</button>
    {/if}
  {/snippet}

  {#if viewMode === 'table'}
    <div class="catalog-table-wrap">
      <table class="catalog-table">
        <thead>
          <tr>
            <th class="sortable" onclick={() => handleSort('enabled')}>
              State {sortKey === 'enabled' ? (sortDir === 'asc' ? '\u25B2' : '\u25BC') : ''}
            </th>
            <th class="sortable" onclick={() => handleSort('name')}>
              Server {sortKey === 'name' ? (sortDir === 'asc' ? '\u25B2' : '\u25BC') : ''}
            </th>
            <th>Description</th>
            <th>Categories</th>
            <th>Tools</th>
            <th class="sortable" onclick={() => handleSort('running')}>
              Runtime {sortKey === 'running' ? (sortDir === 'asc' ? '\u25B2' : '\u25BC') : ''}
            </th>
            <th>Action</th>
          </tr>
        </thead>
        <tbody>
          {#each sorted as srv (srv.name)}
            <tr class:disabled-row={!srv.enabled}>
              <td class="cell-state">
                <span class="status-dot" class:on={srv.enabled} class:off={!srv.enabled}></span>
                <span class="state-label" class:state-enabled={srv.enabled} class:state-disabled={!srv.enabled}>
                  {srv.enabled ? 'enabled' : 'disabled'}
                </span>
              </td>
              <td class="cell-name">
                <button class="name-btn" onclick={() => toggleExpand(srv.name)}>
                  <span class="expand-icon" class:expanded={expandedServer === srv.name}>{'\u25B8'}</span>
                  {srv.name}
                </button>
              </td>
              <td class="cell-desc" title={srv.description}>{srv.description || '\u2014'}</td>
              <td class="cell-cats">
                {#each srv.categories ?? [] as cat}
                  <span class="cat-badge">{cat}</span>
                {/each}
              </td>
              <td class="cell-tools">
                <span class="tool-count">{srv.tool_count ?? 0}</span>
              </td>
              <td>
                {#if srv.running}
                  <span class="runtime-tag runtime-running">running</span>
                {:else}
                  <span class="runtime-tag runtime-stopped">stopped</span>
                {/if}
              </td>
              <td>
                <button
                  class="toggle-btn"
                  class:toggle-disable={srv.enabled}
                  class:toggle-enable={!srv.enabled}
                  onclick={() => toggleServer(srv.name, srv.enabled)}
                  title={toggleConsequence(srv)}
                >
                  {srv.enabled ? 'Disable' : 'Enable'}
                </button>
              </td>
            </tr>
            {#if expandedServer === srv.name}
              <tr class="detail-row">
                <td colspan="7">
                  <div class="detail-content">
                    <div class="detail-section">
                      <span class="detail-label">Description</span>
                      <span class="detail-value">{srv.description || 'No description provided'}</span>
                    </div>
                    <div class="detail-section">
                      <span class="detail-label">Categories</span>
                      <span class="detail-value">{(srv.categories ?? []).join(', ') || 'None'}</span>
                    </div>
                    <div class="detail-section">
                      <span class="detail-label">Tools</span>
                      <span class="detail-value">{srv.tool_count ?? 0} static tool{(srv.tool_count ?? 0) === 1 ? '' : 's'}</span>
                    </div>
                    <div class="detail-section">
                      <span class="detail-label">Command</span>
                      <span class="detail-value detail-mono">{srv.command || 'No resolved command'}</span>
                    </div>
                    <div class="detail-section">
                      <span class="detail-label">Env hints</span>
                      <span class="detail-value detail-mono">{hintSummary(srv.env_hints)}</span>
                    </div>
                    <div class="detail-section">
                      <span class="detail-label">Config hints</span>
                      <span class="detail-value detail-mono">{hintSummary(srv.config_hints)}</span>
                    </div>
                    <div class="detail-section">
                      <span class="detail-label">State</span>
                      <span class="detail-value">{srv.enabled ? 'Enabled' : 'Disabled'} · {srv.running ? 'Running' : 'Stopped'}</span>
                    </div>
                    <div class="detail-section">
                      <span class="detail-label">Toggle effect</span>
                      <span class="detail-value detail-consequence">{toggleConsequence(srv)}</span>
                    </div>
                  </div>
                </td>
              </tr>
            {/if}
          {/each}
        </tbody>
      </table>
    </div>
  {:else}
    <div class="catalog-cards">
      {#each sorted as srv (srv.name)}
        <div class="server-card" class:server-card-disabled={!srv.enabled}>
          <div class="server-card-head">
            <span class="status-dot" class:on={srv.enabled} class:off={!srv.enabled}></span>
            <span class="server-card-name">{srv.name}</span>
            {#if srv.running}
              <span class="runtime-tag runtime-running">running</span>
            {:else}
              <span class="runtime-tag runtime-stopped">stopped</span>
            {/if}
          </div>
          <div class="server-card-desc">{srv.description || 'No description'}</div>
          <div class="server-card-meta">
            <span class="tool-count">{srv.tool_count ?? 0} tool{(srv.tool_count ?? 0) === 1 ? '' : 's'}</span>
            {#if srv.command}
              <span class="command-snippet">{srv.command}</span>
            {/if}
          </div>
          {#if (srv.categories ?? []).length > 0}
            <div class="server-card-cats">
              {#each srv.categories ?? [] as cat}
                <span class="cat-badge">{cat}</span>
              {/each}
            </div>
          {/if}
          {#if (srv.env_hints ?? []).length > 0 || (srv.config_hints ?? []).length > 0}
            <div class="server-card-hints">
              {#if (srv.env_hints ?? []).length > 0}
                <span class="hint-line">env: {hintSummary(srv.env_hints)}</span>
              {/if}
              {#if (srv.config_hints ?? []).length > 0}
                <span class="hint-line">config: {hintSummary(srv.config_hints)}</span>
              {/if}
            </div>
          {/if}
          <div class="server-card-foot">
            <span class="toggle-hint">{toggleConsequence(srv)}</span>
            <button
              class="toggle-btn"
              class:toggle-disable={srv.enabled}
              class:toggle-enable={!srv.enabled}
              onclick={() => toggleServer(srv.name, srv.enabled)}
            >
              {srv.enabled ? 'Disable' : 'Enable'}
            </button>
          </div>
        </div>
      {/each}
    </div>
  {/if}

  {#if catalogStore.registryPath}
    <div class="catalog-footer">
      <span class="text-muted text-mono text-xs">Registry: {catalogStore.registryPath}</span>
    </div>
  {/if}
</PanelShell>

<style>
  .catalog-header {
    padding: var(--space-2) var(--space-3) var(--space-1);
  }

  .catalog-metrics {
    display: grid;
    grid-template-columns: repeat(4, 1fr);
    gap: var(--space-2);
  }

  .catalog-toolbar {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .catalog-controls {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-2);
    padding: 0 var(--space-3);
  }

  .status-chips {
    display: flex;
    gap: 4px;
  }

  .status-chip {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 3px 10px;
    border-radius: 999px;
    border: 1px solid var(--border);
    background: transparent;
    color: var(--fg-secondary);
    font-size: var(--text-xs);
    font-family: var(--font-mono);
    cursor: pointer;
    transition: all var(--transition-fast);
  }

  .status-chip:hover {
    border-color: var(--fg-muted);
    color: var(--fg-primary);
  }

  .status-chip.active {
    border-color: var(--accent);
    background: color-mix(in srgb, var(--accent) 12%, transparent);
    color: var(--accent);
    font-weight: 600;
    box-shadow: 0 0 6px var(--glow-accent);
  }

  .chip-count {
    font-size: var(--text-2xs);
    opacity: 0.7;
  }

  .view-toggle {
    display: flex;
    gap: 2px;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    overflow: hidden;
  }

  .view-btn {
    padding: 4px 8px;
    background: transparent;
    border: none;
    color: var(--fg-muted);
    cursor: pointer;
    font-size: var(--text-sm);
    transition: background var(--transition-fast), color var(--transition-fast);
  }

  .view-btn:hover {
    background: var(--bg-elevated);
    color: var(--fg-primary);
  }

  .view-btn.active {
    background: var(--bg-tertiary);
    color: var(--fg-primary);
    box-shadow: 0 0 6px var(--glow-accent);
  }

  /* ---- Table View ---- */
  .catalog-table-wrap {
    overflow-x: auto;
    flex: 1;
  }

  .catalog-table {
    width: 100%;
    border-collapse: collapse;
    font-size: var(--text-sm);
  }

  .catalog-table thead {
    position: sticky;
    top: 0;
    z-index: 2;
    background: var(--bg-secondary);
  }

  .catalog-table th {
    text-align: left;
    padding: var(--space-2) var(--space-3);
    font-size: var(--text-xs);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-muted);
    border-bottom: 1px solid var(--border);
    white-space: nowrap;
    user-select: none;
  }

  .catalog-table th.sortable {
    cursor: pointer;
  }

  .catalog-table th.sortable:hover {
    color: var(--fg-primary);
  }

  .catalog-table td {
    padding: var(--space-2) var(--space-3);
    border-bottom: 1px solid var(--border-subtle);
    color: var(--fg-secondary);
    vertical-align: middle;
  }

  .catalog-table tr:hover:not(.detail-row) {
    background: var(--bg-tertiary);
  }

  .disabled-row {
    opacity: 0.5;
  }

  .disabled-row:hover {
    opacity: 0.7;
  }

  .cell-state {
    white-space: nowrap;
  }

  .cell-name {
    white-space: nowrap;
  }

  .name-btn {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    background: none;
    border: none;
    padding: 0;
    cursor: pointer;
    font-family: var(--font-mono);
    font-weight: 500;
    font-size: inherit;
    color: var(--fg-primary);
  }

  .name-btn:hover {
    color: var(--accent);
  }

  .expand-icon {
    font-size: var(--text-2xs);
    color: var(--fg-muted);
    transition: transform var(--transition-fast);
  }

  .expand-icon.expanded {
    transform: rotate(90deg);
  }

  .cell-desc {
    max-width: 300px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .cell-cats {
    display: flex;
    gap: 4px;
    flex-wrap: wrap;
  }

  .cell-tools {
    white-space: nowrap;
  }

  .tool-count {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-width: 2.5rem;
    padding: 2px 8px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border);
    background: var(--bg-tertiary);
    color: var(--fg-primary);
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    font-weight: 600;
  }

  .state-label {
    margin-left: 6px;
    font-size: var(--text-xs);
    font-family: var(--font-mono);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
  }

  .state-enabled {
    color: var(--success);
  }

  .state-disabled {
    color: var(--fg-muted);
  }

  .runtime-tag {
    font-size: var(--text-xs);
    font-family: var(--font-mono);
    padding: 1px 6px;
    border-radius: var(--radius-sm);
  }

  .runtime-running {
    color: var(--success);
    background: var(--success-dim);
  }

  .runtime-stopped {
    color: var(--fg-muted);
    background: var(--bg-tertiary);
  }

  /* ---- Detail Row ---- */
  .detail-row td {
    padding: 0;
    border-bottom: 1px solid var(--border);
  }

  .detail-content {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: var(--space-2);
    padding: var(--space-2) var(--space-3) var(--space-3);
    background: color-mix(in srgb, var(--bg-tertiary) 50%, transparent);
    border-top: 1px dashed var(--border);
  }

  .detail-section {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .detail-label {
    font-size: var(--text-2xs);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-dim);
  }

  .detail-value {
    font-size: var(--text-sm);
    color: var(--fg-secondary);
    line-height: var(--leading-normal);
  }

  .detail-mono {
    font-family: var(--font-mono);
  }

  .detail-consequence {
    color: var(--warning);
    font-style: italic;
  }

  /* ---- Toggle Buttons ---- */
  .toggle-btn {
    font-size: var(--text-xs);
    padding: 3px 10px;
    border-radius: var(--radius-sm);
    cursor: pointer;
    border: 1px solid var(--border);
    background: var(--bg-tertiary);
    color: var(--fg-secondary);
    font-weight: 500;
    transition: background var(--transition-fast), color var(--transition-fast), border-color var(--transition-fast);
  }

  .toggle-btn:hover {
    color: var(--fg-primary);
  }

  .toggle-disable {
    border-color: rgba(255, 61, 113, 0.2);
    color: var(--error);
  }

  .toggle-disable:hover {
    background: var(--error);
    color: var(--bg-primary);
    border-color: var(--error);
  }

  .toggle-enable {
    border-color: rgba(34, 224, 118, 0.2);
    color: var(--success);
  }

  .toggle-enable:hover {
    background: var(--success);
    color: var(--bg-primary);
    border-color: var(--success);
  }

  /* ---- Card View ---- */
  .catalog-cards {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
    gap: var(--space-3);
    padding: var(--space-2) var(--space-3);
  }

  .server-card {
    position: relative;
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    padding: var(--space-3);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    background: var(--bg-secondary);
    transition: border-color var(--transition-fast), box-shadow var(--transition-fast);
  }

  .server-card::before {
    content: '';
    position: absolute;
    inset: 0;
    border-radius: inherit;
    background: var(--surface-highlight);
    pointer-events: none;
  }

  .server-card:hover {
    border-color: color-mix(in srgb, var(--accent) 40%, var(--border) 60%);
    box-shadow: 0 0 8px var(--glow-accent);
  }

  .server-card-disabled {
    opacity: 0.55;
  }

  .server-card-disabled:hover {
    opacity: 0.75;
  }

  .server-card-head {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }

  .server-card-name {
    font-family: var(--font-mono);
    font-weight: 600;
    font-size: var(--text-sm);
    color: var(--fg-primary);
    flex: 1;
  }

  .server-card-desc {
    font-size: var(--text-sm);
    color: var(--fg-muted);
    line-height: var(--leading-normal);
    display: -webkit-box;
    -webkit-line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }

  .server-card-meta {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    flex-wrap: wrap;
  }

  .command-snippet {
    font-size: var(--text-xs);
    font-family: var(--font-mono);
    color: var(--fg-secondary);
    padding: 2px 6px;
    border-radius: var(--radius-sm);
    background: var(--bg-tertiary);
    border: 1px solid var(--border-subtle);
  }

  .server-card-cats {
    display: flex;
    gap: 4px;
    flex-wrap: wrap;
  }

  .server-card-hints {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .hint-line {
    font-size: var(--text-xs);
    color: var(--fg-dim);
    font-family: var(--font-mono);
    line-height: var(--leading-tight);
  }

  .server-card-foot {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-2);
    margin-top: auto;
    padding-top: var(--space-2);
    border-top: 1px solid var(--border);
  }

  .toggle-hint {
    font-size: var(--text-xs);
    color: var(--fg-dim);
    font-style: italic;
    flex: 1;
  }

  /* ---- Shared ---- */
  .status-dot {
    display: inline-block;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    flex-shrink: 0;
  }

  .status-dot.on {
    background: var(--success);
    box-shadow: 0 0 4px var(--success);
  }

  .status-dot.off {
    background: var(--fg-muted);
  }

  .cat-badge {
    display: inline-block;
    font-size: var(--text-xs);
    padding: 1px 5px;
    border-radius: var(--radius-sm);
    background: var(--bg-tertiary);
    color: var(--fg-muted);
    font-family: var(--font-mono);
  }

  .catalog-footer {
    padding: var(--space-2) var(--space-3) 0;
    border-top: 1px solid var(--border);
    margin-top: var(--space-2);
  }

  @media (max-width: 640px) {
    .catalog-metrics {
      grid-template-columns: repeat(2, 1fr);
    }

    .catalog-controls {
      flex-direction: column;
      align-items: flex-start;
    }

    .detail-content {
      grid-template-columns: 1fr;
    }
  }
</style>
