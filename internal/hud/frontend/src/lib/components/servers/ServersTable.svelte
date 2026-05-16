<script lang="ts">
  /**
   * ServersTable — wraps shared/DataTable with the server-specific columns
   * and row snippet, plus the empty-with-active-filters affordance.
   *
   * @type {{
   *   rows: import('../../stores/health.svelte.ts').MergedServer[],
   *   onSelect: (server: import('../../stores/health.svelte.ts').MergedServer) => void,
   * }}
   */
  let { rows, onSelect } = $props();

  import { healthStore } from '../../stores/health.svelte.ts';
  import { sanitizeText } from '../../utils/format.ts';
  import { formatLatency } from '../../utils/serversHelpers';
  import StatusDot from '../../widgets/StatusDot.svelte';
  import SparkLine from '../../widgets/SparkLine.svelte';
  import Badge from '../../widgets/Badge.svelte';
  import DataTable from '../shared/DataTable.svelte';
  import EmptyState from '../shared/EmptyState.svelte';

  const columns = [
    { key: 'name', label: 'Server', sortable: true, width: '140px' },
    { key: 'status', label: 'Status', sortable: true, width: '80px' },
    { key: 'consec_fails', label: 'Fails', sortable: true, width: '50px' },
    { key: 'latency', label: 'Latency', sortable: true, width: '80px' },
    { key: 'tool_count', label: 'Tools', sortable: true, width: '60px' },
    { key: 'transport', label: 'Transport', width: '80px' },
    { key: 'sparkline', label: 'Sparkline', width: '102px' },
  ];

  let hasActiveFilters = $derived(
    healthStore.searchQuery.trim() !== '' || healthStore.categoryFilter !== '' || healthStore.statusFilter !== ''
  );
</script>

<div class="table-container">
  {#if rows.length === 0 && healthStore.lastUpdated}
    <EmptyState
      icon={'♥'}
      heading="No servers match filters"
      description="Try adjusting your search or filter criteria."
      compact
    >
      {#snippet action()}
        {#if hasActiveFilters}
          <button class="btn btn-ghost" onclick={() => healthStore.clearFilters()}>Clear filters</button>
        {/if}
      {/snippet}
    </EmptyState>
  {:else}
    <DataTable
      {columns}
      {rows}
      sortKey={healthStore.sortKey}
      sortDir={healthStore.sortDir}
      stableLayout={true}
      loading={!healthStore.lastUpdated}
      skeletonRows={5}
      idKey="name"
      onSort={(key, dir) => healthStore.setSort(key, dir)}
      onRowClick={(row) => onSelect(row)}
    >
      {#snippet row({ row: server })}
        <td class="server-name-cell">
          <span class="text-mono server-name" title={sanitizeText(server.name)}>{sanitizeText(server.name)}</span>
          {#if server.categories?.length > 0}
            <span class="server-cats">{#each server.categories as cat}<Badge text={cat} variant="info" />{/each}</span>
          {/if}
        </td>
        <td>
          <StatusDot status={server.status ?? 'unknown'} />
        </td>
        <td class="text-mono" class:fail-warn={server.consec_fails > 0}>{server.consec_fails > 0 ? server.consec_fails : ''}</td>
        <td class="text-mono">{#key server.latency}<span class="data-updated">{formatLatency(server.latency)}</span>{/key}</td>
        <td class="text-mono">{server.tool_count ?? 0}</td>
        <td class="text-mono text-muted transport-cell" title={sanitizeText(server.transport || '---')}>
          {sanitizeText(server.transport || '---')}
        </td>
        <td class="sparkline-cell">
          {#if server.latencyHistory?.length}
            <SparkLine
              data={server.latencyHistory}
              width={92}
              height={20}
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

<style>
  .table-container {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    position: relative;
  }

  .table-container::before {
    content: '';
    position: absolute;
    inset: 0;
    border-radius: inherit;
    background: var(--surface-highlight);
    pointer-events: none;
    z-index: 1;
  }

  .server-name {
    color: var(--fg-primary);
    font-weight: 500;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .transport-cell {
    max-width: 80px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .sparkline-cell {
    width: 102px;
    padding: 4px 8px;
    display: flex;
    align-items: center;
    justify-content: flex-start;
    min-height: 24px;
  }

  .server-name-cell {
    display: flex;
    flex-direction: column;
    gap: 2px;
    overflow: hidden;
  }

  .server-cats {
    display: flex;
    gap: 3px;
    flex-wrap: wrap;
  }

  .fail-warn {
    color: var(--warning);
    font-weight: 600;
  }

  @media (max-width: 768px) {
    .sparkline-cell {
      display: none;
    }
  }
</style>
