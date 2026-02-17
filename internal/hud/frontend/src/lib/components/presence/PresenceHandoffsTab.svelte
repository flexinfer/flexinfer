<script>
  import { formatTime } from '../../utils/format.ts';
  import Badge from '../../widgets/Badge.svelte';
  import DataTable from '../shared/DataTable.svelte';
  import EmptyState from '../shared/EmptyState.svelte';

  let {
    handoffs = [],
    templates = [],
    handoffLoading = false,
    handoffError = '',
    onOpenHandoffModal = () => {},
    onAcceptHandoff = () => {},
  } = $props();

  const handoffColumns = [
    { key: 'from_agent', label: 'From', width: '100px' },
    { key: 'to_agent', label: 'To', width: '100px' },
    { key: 'summary', label: 'Summary' },
    { key: 'status', label: 'Status', width: '90px' },
    { key: 'created_at', label: 'Created', width: '90px' },
    { key: 'actions', label: 'Actions', width: '80px' },
  ];

  function handoffStatusVariant(status) {
    const map = {
      pending: 'warning',
      accepted: 'success',
      expired: 'error',
    };
    return map[status] ?? 'info';
  }
</script>

<div class="card">
  <div class="card-header">
    <span class="card-title">Agent Handoffs</span>
    <span class="count-badge">{handoffs.length}</span>
    <div class="card-actions">
      <button class="btn btn-sm" onclick={onOpenHandoffModal}>+ Handoff</button>
    </div>
  </div>

  {#if handoffLoading}
    <div class="loading-bar"><div class="loading-bar-inner"></div></div>
  {/if}

  {#if handoffError}
    <div class="text-xs text-muted" style="padding: 4px 12px;">Failed to load handoffs</div>
  {/if}

  {#if handoffs.length === 0 && !handoffLoading}
    <EmptyState icon={'\u{1F91D}'} heading="No handoffs" compact />
  {:else if handoffs.length > 0}
    <DataTable
      columns={handoffColumns}
      rows={handoffs}
    >
      {#snippet row({ row: handoff })}
        <td class="text-mono">{handoff.from_agent || '---'}</td>
        <td class="text-mono">{handoff.to_agent || 'any'}</td>
        <td class="truncate" title={handoff.summary}>{handoff.summary}</td>
        <td><Badge text={handoff.status} variant={handoffStatusVariant(handoff.status)} /></td>
        <td class="text-mono text-muted">{formatTime(handoff.created_at)}</td>
        <td>
          {#if handoff.status === 'pending'}
            <button class="btn btn-xs btn-success" onclick={() => onAcceptHandoff(handoff.id)}>
              Accept
            </button>
          {:else}
            <span class="text-muted text-xs">{handoff.accepted_at ? formatTime(handoff.accepted_at) : '---'}</span>
          {/if}
        </td>
      {/snippet}
    </DataTable>
  {/if}

  {#if templates.length > 0}
    <div class="templates-section">
      <div class="section-header">
        <span class="section-title">Session Templates</span>
      </div>
      <div class="template-list">
        {#each templates as tpl (tpl.id)}
          <div class="template-chip">
            <span class="template-name text-mono">{tpl.name}</span>
            <span class="text-muted text-xs">{tpl.description}</span>
          </div>
        {/each}
      </div>
    </div>
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

  .card-actions {
    margin-left: auto;
  }

  .btn-xs {
    padding: 2px 8px;
    font-size: 11px;
  }

  .btn-success {
    background: rgba(34, 178, 85, 0.15);
    color: var(--success);
    border: 1px solid rgba(34, 178, 85, 0.3);
  }

  .btn-success:hover {
    background: rgba(34, 178, 85, 0.25);
  }

  .templates-section {
    border-top: 1px solid var(--border);
    padding: 12px 0 0;
    margin-top: 12px;
  }

  .section-header {
    margin-bottom: 8px;
  }

  .section-title {
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    color: var(--fg-muted);
  }

  .template-list {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
  }

  .template-chip {
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: 6px 10px;
    background: var(--bg-tertiary);
    border: 1px solid var(--border);
    border-radius: var(--border-radius);
  }

  .template-name {
    font-size: 12px;
    font-weight: 500;
    color: var(--fg-primary);
  }

  .loading-bar {
    height: 2px;
    background: var(--bg-tertiary);
    border-radius: 1px;
    overflow: hidden;
    margin-bottom: 4px;
  }

  .loading-bar-inner {
    width: 40%;
    height: 100%;
    background: var(--accent);
    border-radius: 1px;
    animation: loadingSlide 1s ease-in-out infinite;
  }

  @keyframes loadingSlide {
    0% { transform: translateX(-100%); }
    100% { transform: translateX(300%); }
  }
</style>
