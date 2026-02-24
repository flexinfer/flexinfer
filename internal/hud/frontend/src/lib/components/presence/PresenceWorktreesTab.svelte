<script>
  import { formatTime } from '../../utils/format.ts';
  import Badge from '../../widgets/Badge.svelte';
  import DataTable from '../shared/DataTable.svelte';
  import EmptyState from '../shared/EmptyState.svelte';

  let { worktrees = [] } = $props();

  const worktreeColumns = [
    { key: 'branch', label: 'Branch' },
    { key: 'agent_id', label: 'Agent', width: '100px' },
    { key: 'status', label: 'Status', width: '90px' },
    { key: 'git_status', label: 'Git', width: '100px' },
    { key: 'purpose', label: 'Purpose' },
    { key: 'created_at', label: 'Created', width: '90px' },
  ];

  function worktreeVariant(status) {
    const map = {
      active: 'success',
      released: 'info',
      orphaned: 'error',
    };
    return map[status] ?? 'info';
  }
</script>

<div class="card">
  <div class="card-header">
    <span class="card-title">Git Worktrees</span>
    <span class="count-badge">{worktrees.length}</span>
  </div>
  {#if worktrees.length === 0}
    <EmptyState icon={'\u{1F333}'} heading="No active worktrees" compact />
  {:else}
    <DataTable
      columns={worktreeColumns}
      rows={worktrees}
      idKey="assignment_id"
    >
      {#snippet row({ row: wt })}
        <td class="text-mono">{wt.branch}</td>
        <td class="text-mono">{wt.agent_id}</td>
        <td><Badge text={wt.status} variant={worktreeVariant(wt.status)} /></td>
        <td class="text-mono text-muted text-xs" title={wt.git_status}>{wt.git_status || 'clean'}</td>
        <td class="truncate text-muted" title={wt.purpose}>{wt.purpose || '---'}</td>
        <td class="text-mono text-muted">{formatTime(wt.created_at)}</td>
      {/snippet}
    </DataTable>
  {/if}
</div>

<style>
  .count-badge {
    font-family: var(--font-mono);
    font-size: 11px;
    background: var(--bg-tertiary);
    color: var(--fg-secondary);
    padding: 1px 6px;
    border-radius: var(--radius-lg);
  }
</style>
