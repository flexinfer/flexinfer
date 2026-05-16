<script lang="ts">
  /**
   * ServersPanel — composition shell for the Operations → Servers view.
   * The header, table, infra cards, and drawer live in
   * `lib/components/servers/*`; pure helpers + filter logic live in
   * `lib/utils/serversHelpers.ts`. Filter/sort state moved into
   * healthStore per the panel decomp contract
   * (`docs/HUD_PANEL_DECOMP.md`).
   */
  import { healthStore, type MergedServer } from '../stores/health.svelte.ts';
  import { rbacStore } from '../stores/rbac.svelte.ts';
  import { daemonMetricsStore } from '../stores/daemonMetrics.svelte.ts';
  import { otelStore } from '../stores/otel.svelte.ts';
  import FilterBar from './shared/FilterBar.svelte';
  import ServersHeader from './servers/ServersHeader.svelte';
  import ServersTable from './servers/ServersTable.svelte';
  import InfraCards from './servers/InfraCards.svelte';
  import RbacOtelCards from './servers/RbacOtelCards.svelte';
  import RequestMetricsCard from './servers/RequestMetricsCard.svelte';
  import ServerDetail from './servers/ServerDetail.svelte';
  import {
    filterServers,
    sortServers,
    categoryOptionsFrom,
  } from '../utils/serversHelpers';

  const otelPollingOwner = Symbol('ServersPanel');

  $effect(() => { rbacStore.startPolling(30000); return () => rbacStore.stopPolling(); });
  $effect(() => { otelStore.startPolling(30000, otelPollingOwner); return () => otelStore.stopPolling(otelPollingOwner); });
  $effect(() => { healthStore.startPolling(5000); return () => { healthStore.stopPolling(); }; });
  $effect(() => { daemonMetricsStore.startPolling(15000); return () => daemonMetricsStore.stopPolling(); });

  let servers = $derived(healthStore.servers ?? []);
  let categoryOptions = $derived(categoryOptionsFrom(servers));

  let filterDefs = $derived([
    {
      key: 'category',
      label: 'All Categories',
      value: healthStore.categoryFilter,
      options: categoryOptions,
    },
    {
      key: 'status',
      label: 'All Status',
      value: healthStore.statusFilter,
      options: [
        { value: 'healthy', label: 'Running' },
        { value: 'idle', label: 'Idle' },
        { value: 'degraded', label: 'Degraded' },
        { value: 'down', label: 'Down' },
      ],
    },
  ]);

  let filtered = $derived(filterServers(servers, healthStore.searchQuery, healthStore.categoryFilter, healthStore.statusFilter));
  let sorted = $derived(sortServers(filtered, healthStore.sortKey, healthStore.sortDir));

  let selectedServer = $state<MergedServer | null>(null);

  function handleSearch(val: string) { healthStore.setSearch(val); }
  function handleFilter(key: string, val: string) {
    if (key === 'category') healthStore.setCategoryFilter(val);
    else if (key === 'status') healthStore.setStatusFilter(val);
  }
  function selectServer(server: MergedServer) {
    selectedServer = selectedServer?.name === server.name ? null : server;
  }
</script>

<div class="panel servers-panel">
  <ServersHeader />

  <FilterBar
    search={healthStore.searchQuery}
    placeholder="Search servers..."
    filters={filterDefs}
    resultCount={filtered.length}
    onSearch={handleSearch}
    onFilter={handleFilter}
  />

  <div class="servers-layout">
    <ServersTable rows={sorted} onSelect={selectServer} />

    <aside class="servers-rail">
      <InfraCards />
      <RbacOtelCards />
      <RequestMetricsCard />
    </aside>
  </div>
</div>

<ServerDetail server={selectedServer} onClose={() => { selectedServer = null; }} />

<style>
  .servers-panel {
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  .servers-layout {
    flex: 1;
    min-height: 0;
    display: grid;
    grid-template-columns: minmax(0, 1fr) 340px;
    gap: var(--space-3);
  }

  .servers-rail {
    min-height: 0;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
  }

  @media (max-width: 1280px) {
    .servers-layout {
      grid-template-columns: 1fr;
    }
  }
</style>
