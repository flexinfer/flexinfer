<script>
  import { catalogStore } from '../stores/catalog.svelte.ts';
  import PanelShell from './shared/PanelShell.svelte';
  import FilterBar from './shared/FilterBar.svelte';

  $effect(() => {
    catalogStore.startPolling(30000);
    return () => { catalogStore.stopPolling(); };
  });

  let servers = $derived(catalogStore.filteredServers);
  let sortKey = $state('name');
  let sortDir = $state('asc');
  let runningCount = $derived(catalogStore.servers.filter((s) => s.running).length);
  let hasActiveFilters = $derived(catalogStore.searchQuery.trim() !== '' || catalogStore.categoryFilter !== 'all');

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

  function handleSearch(val) {
    catalogStore.search(val);
  }

  function handleFilter(key, value) {
    if (key === 'category') {
      catalogStore.filterByCategory(value || 'all');
    }
  }

  function clearFilters() {
    catalogStore.search('');
    catalogStore.filterByCategory('all');
  }

  function handleSort(key) {
    if (sortKey === key) {
      sortDir = sortDir === 'asc' ? 'desc' : 'asc';
    } else {
      sortKey = key;
      sortDir = 'asc';
    }
  }

  async function toggleServer(name, currentEnabled) {
    await catalogStore.toggleServer(name, !currentEnabled);
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
    <div class="catalog-intro">
      <div class="catalog-summary">
        <div class="catalog-summary-eyebrow">Registry overview</div>
        <div class="catalog-summary-line">
          {catalogStore.enabledCount} enabled · {catalogStore.disabledCount} disabled · {runningCount} running
        </div>
        <div class="catalog-summary-copy">
          This is the authoritative operator view of deployed server surfaces. Search it to understand capability, readiness, and what can be safely toggled.
        </div>
      </div>
    </div>
  {/snippet}

  {#snippet toolbar()}
    <FilterBar
      search={catalogStore.searchQuery}
      placeholder="Search servers by name, description, or category\u2026"
      {filters}
      resultCount={sorted.length}
      onSearch={handleSearch}
      onFilter={handleFilter}
      onClear={clearFilters}
    />
  {/snippet}

  {#snippet emptyAction()}
    {#if hasActiveFilters}
      <button class="btn btn-ghost btn-sm" onclick={clearFilters}>Clear filters</button>
    {/if}
  {/snippet}

  <div class="catalog-table-wrap">
    <table class="catalog-table">
      <thead>
        <tr>
          <th class="sortable" onclick={() => handleSort('enabled')}>
            State {sortKey === 'enabled' ? (sortDir === 'asc' ? '\u25B2' : '\u25BC') : ''}
          </th>
          <th class="sortable" onclick={() => handleSort('name')}>
            Registry entry {sortKey === 'name' ? (sortDir === 'asc' ? '\u25B2' : '\u25BC') : ''}
          </th>
          <th>Capability</th>
          <th>Tags</th>
          <th class="sortable" onclick={() => handleSort('running')}>
            Runtime {sortKey === 'running' ? (sortDir === 'asc' ? '\u25B2' : '\u25BC') : ''}
          </th>
          <th>Toggle</th>
        </tr>
      </thead>
      <tbody>
        {#each sorted as srv (srv.name)}
          <tr class:disabled={!srv.enabled}>
            <td class="cell-state">
              <span class="status-dot" class:on={srv.enabled} class:off={!srv.enabled}></span>
              <span class="state-label" class:enabled={srv.enabled} class:disabled={!srv.enabled}>
                {srv.enabled ? 'enabled' : 'disabled'}
              </span>
            </td>
            <td class="cell-name">{srv.name}</td>
            <td class="cell-desc">{srv.description || '\u2014'}</td>
            <td class="cell-cats">
              {#each srv.categories ?? [] as cat}
                <span class="badge">{cat}</span>
              {/each}
            </td>
            <td>
              {#if srv.running}
                <span class="running-tag">running</span>
              {:else}
                <span class="stopped-tag">stopped</span>
              {/if}
            </td>
            <td>
              <button
                class="btn btn-sm"
                class:btn-danger={srv.enabled}
                class:btn-primary={!srv.enabled}
                onclick={() => toggleServer(srv.name, srv.enabled)}
              >
                {srv.enabled ? 'Disable' : 'Enable'}
              </button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>

  {#if catalogStore.registryPath}
    <div class="catalog-footer">
      <span class="text-muted text-mono text-xs">Registry: {catalogStore.registryPath}</span>
    </div>
  {/if}
</PanelShell>

<style>
  .catalog-intro {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    padding: var(--space-2) var(--space-3) var(--space-1);
  }

  .catalog-summary {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
    padding: var(--space-3);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    background: linear-gradient(180deg, color-mix(in srgb, var(--bg-tertiary) 72%, transparent), var(--bg-secondary));
  }

  .catalog-summary-eyebrow {
    font-size: var(--text-xs);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-muted);
    font-weight: 600;
  }

  .catalog-summary-line {
    font-size: var(--text-base);
    font-weight: 600;
    color: var(--fg-primary);
  }

  .catalog-summary-copy {
    font-size: var(--text-sm);
    color: var(--fg-muted);
    max-width: 72ch;
    line-height: var(--leading-normal);
  }

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
    border-bottom: 1px solid var(--border);
    color: var(--fg-secondary);
    vertical-align: middle;
  }

  .catalog-table tr:hover {
    background: var(--bg-tertiary);
  }

  .catalog-table tr.disabled {
    opacity: 0.5;
  }

  .cell-state {
    white-space: nowrap;
  }

  .cell-name {
    font-family: var(--font-mono);
    font-weight: 500;
    color: var(--fg-primary);
    white-space: nowrap;
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

  .state-label {
    margin-left: 6px;
    font-size: var(--text-xs);
    font-family: var(--font-mono);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
  }

  .state-label.enabled {
    color: var(--success);
  }

  .state-label.disabled {
    color: var(--fg-muted);
  }

  .running-tag {
    font-size: var(--text-xs);
    color: var(--success);
    font-family: var(--font-mono);
  }

  .stopped-tag {
    font-size: var(--text-xs);
    color: var(--fg-muted);
    font-family: var(--font-mono);
  }

  .btn-sm {
    font-size: var(--text-xs);
    padding: 2px 8px;
    border-radius: var(--radius-sm);
    cursor: pointer;
    border: 1px solid var(--border);
    background: var(--bg-tertiary);
    color: var(--fg-secondary);
    transition: background var(--transition-fast);
  }

  .btn-sm:hover {
    background: var(--bg-hover);
    color: var(--fg-primary);
  }

  .btn-sm.btn-danger {
    border-color: var(--error);
    color: var(--error);
  }

  .btn-sm.btn-danger:hover {
    background: var(--error);
    color: var(--bg-primary);
  }

  .btn-sm.btn-primary {
    border-color: var(--success);
    color: var(--success);
  }

  .btn-sm.btn-primary:hover {
    background: var(--success);
    color: var(--bg-primary);
  }

  .status-dot {
    display: inline-block;
    width: 8px;
    height: 8px;
    border-radius: 50%;
  }

  .status-dot.on {
    background: var(--success);
    box-shadow: 0 0 4px var(--success);
  }

  .status-dot.off {
    background: var(--fg-muted);
  }

  .badge {
    display: inline-block;
    font-size: 10px;
    padding: 1px 4px;
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
</style>
