<script>
  import { timelineStore } from '../stores/timeline.svelte.ts';
  import { formatTime, agentColor, eventIcon, statusVariant } from '../utils/format.ts';
  import Badge from '../widgets/Badge.svelte';
  import EmptyState from './shared/EmptyState.svelte';

  $effect(() => {
    timelineStore.startPolling(30000);
    return () => {
      timelineStore.stopPolling();
    };
  });

  let entries = $derived(timelineStore.entries ?? []);
  let filter = $state('');

  let filtered = $derived.by(() => {
    if (!filter) return entries;
    const q = filter.toLowerCase();
    return entries.filter(e =>
      e.event_type.toLowerCase().includes(q) ||
      (e.agent_id ?? '').toLowerCase().includes(q) ||
      (e.agent_type ?? '').toLowerCase().includes(q)
    );
  });

  function eventVariant(type) {
    if (type.includes('session.start')) return 'success';
    if (type.includes('session.end') || type.includes('reaped')) return 'error';
    if (type.includes('heartbeat')) return 'info';
    if (type.includes('task')) return 'accent';
    if (type.includes('conflict')) return 'warning';
    if (type.includes('approval')) return 'warning';
    if (type.includes('dispatch')) return 'accent';
    return 'info';
  }

  function shortEventType(type) {
    // Remove common prefixes for compact display.
    return type.replace('agent.', '').replace('hud.', '');
  }
</script>

<div class="panel timeline-panel">
  <div class="panel-header">
    <span class="panel-title">Activity Timeline</span>
    <span class="count-badge">{filtered.length}</span>
    <div class="header-spacer"></div>
    <input
      type="text"
      class="panel-search-input"
      placeholder="Filter events..."
      bind:value={filter}
    />
  </div>

  <div class="timeline-list">
    {#each filtered as entry, i (entry.timestamp + '-' + i)}
      <div class="timeline-entry">
        <div class="timeline-time">{formatTime(entry.timestamp)}</div>
        <div class="timeline-icon" style:color={agentColor(entry.agent_type)}>
          {eventIcon(entry.event_type)}
        </div>
        <div class="timeline-body">
          <div class="timeline-header-row">
            <Badge text={shortEventType(entry.event_type)} variant={eventVariant(entry.event_type)} />
            {#if entry.agent_id}
              <span class="agent-badge" style:color={agentColor(entry.agent_type)}>
                {entry.agent_id}
              </span>
            {/if}
          </div>
          {#if entry.data}
            <div class="timeline-detail">
              {#if entry.data.namespace}
                <span class="detail-chip">{entry.data.namespace}</span>
              {/if}
              {#if entry.data.session_id}
                <span class="detail-chip text-muted">{String(entry.data.session_id).slice(0, 12)}...</span>
              {/if}
              {#if entry.data.title}
                <span class="detail-chip">{entry.data.title}</span>
              {/if}
              {#if entry.data.status}
                <span class="detail-chip">{entry.data.status}</span>
              {/if}
              {#if entry.data.reason}
                <span class="detail-chip text-muted">{entry.data.reason}</span>
              {/if}
              {#if entry.data.branch}
                <span class="detail-chip text-mono">{entry.data.branch}</span>
              {/if}
            </div>
          {/if}
        </div>
      </div>
    {:else}
      <EmptyState icon={'\u23F0'} heading="No timeline events" compact />
    {/each}
  </div>
</div>

<style>
  .timeline-panel {
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  .panel-header {
    display: flex;
    align-items: center;
    gap: 8px;
    padding-bottom: 8px;
    border-bottom: 1px solid var(--border);
    margin-bottom: 8px;
  }

  .panel-title {
    font-size: 12px;
    font-weight: 600;
    color: var(--fg-primary);
    text-transform: uppercase;
    letter-spacing: 0.06em;
  }

  .count-badge {
    font-family: var(--font-mono);
    font-size: 11px;
    background: var(--bg-tertiary);
    color: var(--fg-secondary);
    padding: 1px 6px;
    border-radius: var(--radius-lg);
  }

  .header-spacer { flex: 1; }

  .panel-search-input {
    width: 200px;
    font-size: 12px;
  }

  .timeline-list {
    flex: 1;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .timeline-entry {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    padding: 6px 8px;
    border-radius: var(--radius-sm);
    transition: background 0.1s;
  }

  .timeline-entry:hover {
    background: var(--bg-secondary);
  }

  .timeline-time {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--fg-muted);
    white-space: nowrap;
    min-width: 64px;
    padding-top: 1px;
  }

  .timeline-icon {
    font-size: 12px;
    min-width: 16px;
    text-align: center;
    padding-top: 2px;
  }

  .timeline-body {
    flex: 1;
    min-width: 0;
  }

  .timeline-header-row {
    display: flex;
    align-items: center;
    gap: 6px;
  }

  .agent-badge {
    font-family: var(--font-mono);
    font-size: 11px;
    font-weight: 500;
  }

  .timeline-detail {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    margin-top: 2px;
  }

  .detail-chip {
    font-size: 10px;
    padding: 1px 5px;
    background: var(--bg-tertiary);
    border-radius: var(--radius-sm);
    color: var(--fg-secondary);
  }

</style>
