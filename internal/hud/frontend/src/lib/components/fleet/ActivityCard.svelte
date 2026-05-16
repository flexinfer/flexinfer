<script>
  /**
   * ActivityCard — recent stream entries shown alongside the fleet table.
   * Reads from streamStore directly (no props): keeps the parent panel
   * shell free of stream wiring.
   */
  import { streamStore } from '../../stores/stream.svelte.ts';
  import { formatTime, sanitizeText, entryVariant } from '../../utils/format.ts';
  import Badge from '../../widgets/Badge.svelte';
  import DataTable from '../shared/DataTable.svelte';
  import EmptyState from '../shared/EmptyState.svelte';

  let entries = $derived(streamStore.entries ?? []);
  let recentActivity = $derived(entries.slice(0, 10));

  const columns = [
    { key: 'time', label: 'Time', width: '70px' },
    { key: 'type', label: 'Type', width: '80px' },
    { key: 'agent', label: 'Agent', width: '90px' },
    { key: 'title', label: 'Title' },
  ];
</script>

<div class="card activity-card">
  <div class="card-header">
    <span class="card-title">Recent Activity</span>
    {#if recentActivity.length > 0}
      <span class="count-badge">{recentActivity.length}</span>
    {/if}
  </div>
  {#if recentActivity.length === 0}
    <EmptyState icon={'○'} heading="No recent activity" compact />
  {:else}
    <DataTable {columns} rows={recentActivity} stableLayout={true} idKey="id">
      {#snippet row({ row: entry })}
        <td class="activity-time text-mono">{formatTime(entry.timestamp)}</td>
        <td><Badge text={sanitizeText(entry.entry_type ?? 'note')} variant={entryVariant(entry.entry_type)} /></td>
        <td class="activity-agent text-mono" title={sanitizeText(entry.agent ?? '---')}>{sanitizeText(entry.agent ?? '---')}</td>
        <td class="activity-title truncate" title={sanitizeText(entry.title ?? entry.content?.slice(0, 120) ?? '---')}>
          {sanitizeText(entry.title ?? entry.content?.slice(0, 60) ?? '---')}
        </td>
      {/snippet}
    </DataTable>
  {/if}
</div>

<style>
  .activity-card {
    overflow: hidden;
    display: flex;
    flex-direction: column;
  }

  .activity-time {
    color: var(--fg-dim);
    font-size: var(--text-xs);
  }

  .activity-agent {
    color: var(--fg-secondary);
    font-size: var(--text-xs);
    max-width: 80px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .activity-title {
    color: var(--fg-primary);
  }

  .count-badge {
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    font-weight: 600;
    background: var(--bg-tertiary);
    color: var(--fg-secondary);
    padding: 2px 8px;
    border-radius: var(--radius-full);
    border: 1px solid var(--border-subtle);
  }
</style>
