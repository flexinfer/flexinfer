<script>
  import { toastStore } from '../../stores/toasts.svelte.ts';
  import { presenceActionsStore } from '../../stores/presenceActions.svelte.ts';
  import { formatTime, truncatePath } from '../../utils/format.ts';
  import Badge from '../../widgets/Badge.svelte';
  import DataTable from '../shared/DataTable.svelte';
  import BulkToolbar from '../shared/BulkToolbar.svelte';
  import EmptyState from '../shared/EmptyState.svelte';

  let {
    claims = [],
    fileConflicts = [],
    onReleaseClaim = async () => {},
  } = $props();

  let selectedClaimIds = $state(new Set());

  const claimColumns = [
    { key: 'file_path', label: 'File' },
    { key: 'agent_id', label: 'Agent', width: '120px' },
    { key: 'claim_type', label: 'Type', width: '80px' },
    { key: 'reason', label: 'Reason', width: '220px' },
    { key: 'created_at', label: 'Since', width: '90px' },
    { key: 'actions', label: 'Actions', width: '80px' },
  ];

  function shortPath(path) {
    return truncatePath(path, 50);
  }

  function claimVariant(type) {
    const map = {
      edit: 'warning',
      review: 'info',
      reserve: 'accent',
    };
    return map[type] ?? 'info';
  }

  function handleClaimSelect(ids) {
    selectedClaimIds = ids;
  }

  async function bulkReleaseClaims() {
    for (const id of selectedClaimIds) {
      const claim = claims.find((c) => c.id === id);
      if (claim) {
        await onReleaseClaim(claim.agent_id, claim.file_path);
      }
    }
    toastStore.success(`${selectedClaimIds.size} claims released`);
    selectedClaimIds = new Set();
  }

  function nudgeAgent(agentId) {
    presenceActionsStore.onOpenNudge(agentId);
  }

  async function bulkReleaseConflicts() {
    let released = 0;
    for (const conflict of fileConflicts) {
      for (const agentId of conflict.agents) {
        await onReleaseClaim(agentId, conflict.path);
        released++;
      }
    }
    toastStore.success(`${released} conflicting claims released`);
  }

  let claimBulkActions = $derived([
    { label: 'Release Selected', variant: 'danger', onclick: bulkReleaseClaims },
    ...(fileConflicts.length > 0 ? [{ label: 'Release All Conflicts', variant: 'danger', onclick: bulkReleaseConflicts }] : []),
  ]);

  let typeCounts = $derived.by(() => {
    const counts = { edit: 0, review: 0, reserve: 0 };
    for (const c of claims) {
      const t = c.claim_type ?? 'edit';
      if (t in counts) counts[t]++;
      else counts[t] = (counts[t] ?? 0) + 1;
    }
    return counts;
  });
</script>

<div class="card">
  <div class="card-header">
    <span class="card-title">File Claims</span>
    <span class="count-badge">{claims.length}</span>
    {#if claims.length > 0}
      <div class="type-breakdown">
        {#if typeCounts.edit > 0}<Badge text="edit {typeCounts.edit}" variant="warning" />{/if}
        {#if typeCounts.review > 0}<Badge text="review {typeCounts.review}" variant="info" />{/if}
        {#if typeCounts.reserve > 0}<Badge text="reserve {typeCounts.reserve}" variant="accent" />{/if}
      </div>
    {/if}
  </div>

  {#if fileConflicts.length > 0}
    <div class="conflict-banner">
      <span class="conflict-icon">⚠</span>
      <span>{fileConflicts.length} file(s) claimed by multiple agents:</span>
      {#each fileConflicts as conflict}
        <div class="conflict-detail">
          <span class="text-mono text-xs">{shortPath(conflict.path)}</span>
          <span class="text-muted text-xs">→ {conflict.agents.join(', ')}</span>
          {#each conflict.agents as agentId}
            <button class="btn btn-xs btn-nudge-inline" onclick={() => nudgeAgent(agentId)} title="Nudge {agentId}">
              Nudge
            </button>
          {/each}
        </div>
      {/each}
    </div>
  {/if}

  {#if claims.length === 0}
    <EmptyState icon={'\u{1F4C1}'} heading="No active file claims" compact />
  {:else}
    <DataTable
      columns={claimColumns}
      rows={claims}
      selectable={true}
      stableLayout={true}
      selectedIds={selectedClaimIds}
      onSelect={handleClaimSelect}
    >
      {#snippet row({ row: claim })}
        <td class="text-mono" title={claim.file_path}>{shortPath(claim.file_path)}</td>
        <td class="text-mono">{claim.agent_id}</td>
        <td><Badge text={claim.claim_type} variant={claimVariant(claim.claim_type)} /></td>
        <td class="truncate text-muted" title={claim.reason}>{claim.reason || '---'}</td>
        <td class="text-mono text-muted">{formatTime(claim.created_at)}</td>
        <td>
          <button class="btn btn-xs btn-danger" onclick={() => onReleaseClaim(claim.agent_id, claim.file_path)} title="Force-release this claim">
            Release
          </button>
        </td>
      {/snippet}
    </DataTable>
    <BulkToolbar
      count={selectedClaimIds.size}
      actions={claimBulkActions}
      onClearSelection={() => { selectedClaimIds = new Set(); }}
    />
  {/if}
</div>

<style>
  .type-breakdown {
    display: flex;
    gap: 4px;
    margin-left: auto;
  }

  .count-badge {
    font-family: var(--font-mono);
    font-size: 11px;
    background: var(--bg-tertiary);
    color: var(--fg-secondary);
    padding: 1px 6px;
    border-radius: var(--radius-lg);
  }

  .conflict-banner {
    background: rgba(231, 179, 18, 0.08);
    border: 1px solid rgba(231, 179, 18, 0.2);
    border-radius: var(--border-radius);
    padding: 10px 14px;
    margin: 8px 0;
    font-size: 12px;
    color: var(--warning);
  }

  .conflict-icon {
    font-size: 14px;
    margin-right: 4px;
  }

  .conflict-detail {
    display: flex;
    gap: 8px;
    align-items: center;
    padding: 3px 0 3px 20px;
  }

  .btn-xs {
    padding: 2px 8px;
    font-size: 11px;
  }

  .btn-danger {
    background: rgba(233, 93, 116, 0.12);
    color: var(--error);
    border: 1px solid rgba(233, 93, 116, 0.3);
  }

  .btn-danger:hover {
    background: rgba(233, 93, 116, 0.22);
  }

  .btn-nudge-inline {
    padding: 1px 6px;
    font-size: 10px;
    border-radius: var(--radius-sm);
    cursor: pointer;
    border: 1px solid var(--accent);
    background: transparent;
    color: var(--accent);
    margin-left: 4px;
  }

  .btn-nudge-inline:hover {
    background: var(--accent);
    color: var(--bg-primary);
  }
</style>
