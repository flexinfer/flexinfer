<script>
  import { streamStore } from '../stores/stream.svelte.ts';
  import { formatTime, entryVariant } from '../utils/format.ts';
  import Badge from '../widgets/Badge.svelte';
  import SparkLine from '../widgets/SparkLine.svelte';
  import EmptyState from './shared/EmptyState.svelte';

  $effect(() => {
    streamStore.startPolling(2000);
    return () => { streamStore.stopPolling(); };
  });

  let entries = $derived(streamStore.entries ?? []);
  let paused = $derived(streamStore.paused ?? false);

  let typeFilter = $state('all');
  let agentFilter = $state('all');
  let streamEl = $state(null);

  const entryTypes = ['all', 'decision', 'finding', 'error', 'task', 'file_read', 'note'];

  let agents = $derived.by(() => {
    const set = new Set();
    entries.forEach(e => { if (e.agent) set.add(e.agent); });
    return ['all', ...Array.from(set).sort()];
  });

  let filtered = $derived.by(() => {
    let result = entries;

    if (typeFilter !== 'all') {
      result = result.filter(e => e.entry_type === typeFilter);
    }

    if (agentFilter !== 'all') {
      result = result.filter(e => e.agent === agentFilter);
    }

    return result;
  });

  // Auto-scroll when not paused and new entries arrive
  $effect(() => {
    const count = filtered.length;
    if (!paused && streamEl) {
      streamEl.scrollTop = 0;
    }
  });

  function setTypeFilter(type) {
    typeFilter = type;
    streamStore.filterType = type;
  }

  function setAgentFilter(agent) {
    agentFilter = agent;
    streamStore.filterAgent = agent;
  }

  // Event density: bucket entries into 12 time slices for sparkline
  let densityData = $derived.by(() => {
    if (entries.length < 2) return [];
    const times = entries.map(e => new Date(e.timestamp).getTime()).filter(t => !isNaN(t));
    if (times.length < 2) return [];
    const min = Math.min(...times);
    const max = Math.max(...times);
    const span = max - min || 1;
    const buckets = new Array(12).fill(0);
    for (const t of times) {
      const idx = Math.min(11, Math.floor(((t - min) / span) * 12));
      buckets[idx]++;
    }
    return buckets;
  });

  // Entry type distribution counts
  let typeCounts = $derived.by(() => {
    const counts = {};
    for (const e of entries) {
      const t = e.entry_type ?? 'note';
      counts[t] = (counts[t] || 0) + 1;
    }
    return counts;
  });

  function typeBorderColor(type) {
    const map = {
      decision: 'var(--accent)',
      finding: 'var(--info)',
      error: 'var(--error)',
      task: 'var(--warning)',
      file_read: 'var(--info)',
      note: 'var(--success)',
    };
    return map[type] ?? 'var(--border)';
  }
</script>

<div class="panel stream-panel">
  <!-- Header: Filters -->
  <div class="stream-header">
    <div class="filter-pills">
      {#each entryTypes as type}
        <button
          class="filter-chip"
          class:active={typeFilter === type}
          onclick={() => setTypeFilter(type)}
        >
          {type === 'all' ? 'All' : type.replace('_', ' ')}
        </button>
      {/each}
    </div>
    <div class="header-controls">
      <select
        class="agent-select"
        value={agentFilter}
        onchange={(e) => setAgentFilter(e.target.value)}
      >
        {#each agents as agent}
          <option value={agent}>{agent === 'all' ? 'All Agents' : agent}</option>
        {/each}
      </select>
      <button
        class="btn pause-btn"
        class:paused-btn={paused}
        onclick={() => streamStore.togglePause()}
      >
        {#if paused}
          <span class="pause-icon">&#9654;</span> Resume
        {:else}
          <span class="pause-icon">&#9208;</span> Pause
        {/if}
      </button>
    </div>
  </div>

  <!-- Density strip -->
  {#if densityData.length > 0 || Object.keys(typeCounts).length > 0}
    <div class="density-strip">
      {#if densityData.length > 0}
        <SparkLine data={densityData} width={140} height={20} color="var(--accent)" />
      {/if}
      {#if Object.keys(typeCounts).length > 0}
        <div class="type-dist">
          {#each Object.entries(typeCounts).sort((a, b) => b[1] - a[1]) as [type, count]}
            <span class="type-dist-item" style:border-color={typeBorderColor(type)}>
              {type.replace('_', ' ')}: {count}
            </span>
          {/each}
        </div>
      {/if}
    </div>
  {/if}

  <!-- Stream area -->
  <div class="stream-container" bind:this={streamEl}>
    {#if paused}
      <div class="paused-overlay">
        <span class="paused-text">PAUSED</span>
      </div>
    {/if}

    {#if filtered.length === 0}
      <EmptyState icon={'\u25C9'} heading="No activity yet" description="Context entries will appear here in real-time" />
    {:else}
      {#each filtered as entry, i (entry.id ?? `${entry.timestamp}-${i}`)}
        <div
          class="stream-row"
          class:alt-row={i % 2 === 1}
          style="border-left: 3px solid {typeBorderColor(entry.entry_type)}"
        >
          <span class="stream-time text-mono">{formatTime(entry.timestamp)}</span>
          <Badge text={entry.entry_type ?? 'note'} variant={entryVariant(entry.entry_type)} />
          <span class="stream-agent">
            <Badge text={entry.agent ?? '---'} variant="info" />
          </span>
          <span class="stream-ns text-mono text-muted">{entry.namespace ?? ''}</span>
          <span class="stream-title truncate">{entry.title ?? entry.content?.slice(0, 80) ?? '---'}</span>
        </div>
      {/each}
    {/if}
  </div>
</div>

<style>
  .stream-panel {
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  .stream-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: var(--space-2) 0;
    border-bottom: 1px solid var(--border);
    gap: var(--space-3);
    flex-wrap: wrap;
  }

  .density-strip {
    display: flex;
    align-items: center;
    gap: var(--space-3);
    padding: var(--space-1) 0;
    border-bottom: 1px solid var(--border-subtle);
    flex-wrap: wrap;
  }

  .type-dist {
    display: flex;
    gap: var(--space-2);
    flex-wrap: wrap;
  }

  .type-dist-item {
    font-size: var(--text-xs);
    font-family: var(--font-mono);
    color: var(--fg-muted);
    border-left: 2px solid;
    padding-left: 6px;
    letter-spacing: var(--tracking-normal);
  }

  .filter-pills {
    display: flex;
    gap: 4px;
    flex-wrap: wrap;
  }

  .header-controls {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }

  .agent-select {
    min-width: 120px;
  }

  .pause-btn {
    padding: 4px var(--space-3);
    background: var(--bg-tertiary);
    border-radius: var(--radius-sm);
    font-size: var(--text-xs);
    font-weight: 500;
    display: flex;
    align-items: center;
    gap: 4px;
    transition: background var(--transition-fast), color var(--transition-fast), box-shadow var(--transition-fast);
    letter-spacing: var(--tracking-normal);
  }

  .pause-btn:hover {
    background: var(--bg-elevated);
    color: var(--fg-primary);
  }

  .paused-btn {
    background: var(--warning-dim);
    color: var(--warning);
    border: 1px solid rgba(255, 184, 48, 0.3);
    box-shadow: 0 0 8px var(--glow-warning);
  }

  .pause-icon {
    font-size: 10px;
  }

  /* Stream container */
  .stream-container {
    flex: 1;
    overflow-y: auto;
    position: relative;
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    margin-top: var(--space-2);
  }

  .paused-overlay {
    position: sticky;
    top: 0;
    z-index: 10;
    display: flex;
    justify-content: center;
    padding: 4px;
    background: var(--warning-dim);
    border-bottom: 1px solid rgba(255, 184, 48, 0.3);
    backdrop-filter: blur(4px);
  }

  .paused-text {
    font-family: var(--font-mono);
    font-size: 10px;
    font-weight: 700;
    letter-spacing: 3px;
    color: var(--warning);
    animation: glowPulse 2s ease-in-out infinite;
  }

  .stream-row {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    padding: 6px var(--space-3);
    border-bottom: 1px solid var(--border-subtle);
    font-size: var(--text-sm);
    transition: background var(--transition-fast);
    position: relative;
  }

  .stream-row:hover {
    background: var(--bg-tertiary);
  }

  .stream-row:last-child {
    border-bottom: none;
  }

  .alt-row {
    background: rgba(6, 12, 16, 0.4);
  }

  .stream-time {
    color: var(--fg-dim);
    font-size: var(--text-xs);
    flex-shrink: 0;
    width: 65px;
    letter-spacing: var(--tracking-normal);
  }

  .stream-agent {
    flex-shrink: 0;
  }

  .stream-ns {
    font-size: var(--text-xs);
    flex-shrink: 0;
    max-width: 120px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .stream-title {
    color: var(--fg-primary);
    flex: 1;
    min-width: 0;
  }

  @media (max-width: 768px) {
    .stream-header {
      flex-direction: column;
      align-items: flex-start;
    }
    .stream-ns {
      display: none;
    }
    .stream-row {
      min-height: 44px;
    }
  }
</style>
