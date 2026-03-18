<script>
  import { knowledgeStore } from '../stores/knowledge.svelte.ts';
  import { formatNumber, relativeTime } from '../utils/format.ts';
  import Badge from '../widgets/Badge.svelte';
  import DataTable from './shared/DataTable.svelte';
  import FilterBar from './shared/FilterBar.svelte';
  import EmptyState from './shared/EmptyState.svelte';

  $effect(() => {
    knowledgeStore.startPolling(30000);
    return () => { knowledgeStore.stopPolling(); };
  });

  let entries = $derived(knowledgeStore.filteredEntries);
  let categories = $derived(knowledgeStore.categories);
  let agents = $derived(knowledgeStore.agents);

  let searchInput = $state('');
  let expandedIds = $state(new Set());

  let topCategories = $derived.by(() =>
    categories
      .map((category) => ({
        name: category,
        count: entries.filter((entry) => entry.entry_type === category).length,
      }))
      .sort((a, b) => b.count - a.count || a.name.localeCompare(b.name))
      .slice(0, 6)
  );

  let topAgents = $derived.by(() => {
    const counts = new Map();
    for (const entry of entries) {
      if (!entry.agent_id) continue;
      counts.set(entry.agent_id, (counts.get(entry.agent_id) ?? 0) + 1);
    }
    return Array.from(counts.entries())
      .map(([name, count]) => ({ name, count }))
      .sort((a, b) => b.count - a.count || a.name.localeCompare(b.name))
      .slice(0, 6);
  });

  let topNamespaces = $derived.by(() => {
    const counts = new Map();
    for (const entry of entries) {
      if (!entry.namespace) continue;
      counts.set(entry.namespace, (counts.get(entry.namespace) ?? 0) + 1);
    }
    return Array.from(counts.entries())
      .map(([name, count]) => ({ name, count }))
      .sort((a, b) => b.count - a.count || a.name.localeCompare(b.name))
      .slice(0, 5);
  });

  let averageTokens = $derived.by(() =>
    entries.length > 0 ? Math.round(knowledgeStore.totalTokens / entries.length) : 0
  );

  // Sort state
  let sortKey = $state('timestamp');
  let sortDir = $state('desc');

  let sortedEntries = $derived.by(() => {
    const sorted = [...entries];
    sorted.sort((a, b) => {
      let va = a[sortKey] ?? '';
      let vb = b[sortKey] ?? '';
      if (sortKey === 'token_count') {
        va = a.token_count ?? 0;
        vb = b.token_count ?? 0;
      }
      if (va < vb) return sortDir === 'asc' ? -1 : 1;
      if (va > vb) return sortDir === 'asc' ? 1 : -1;
      return 0;
    });
    return sorted;
  });

  const columns = [
    { key: 'entry_type', label: 'Type', sortable: true, width: '90px' },
    { key: 'title', label: 'Title', sortable: true },
    { key: 'agent_id', label: 'Agent', sortable: true, width: '110px' },
    { key: 'file_path', label: 'File', sortable: true, width: '120px' },
    { key: 'token_count', label: 'Tokens', sortable: true, width: '70px', align: 'right' },
    { key: 'timestamp', label: 'Time', sortable: true, width: '80px' },
  ];

  let filterDefs = $derived([
    {
      key: 'category',
      label: 'All types',
      options: categories.map(c => ({ value: c, label: c })),
      value: knowledgeStore.filterCategory === 'all' ? '' : knowledgeStore.filterCategory,
    },
    {
      key: 'agent',
      label: 'All agents',
      options: agents.map(a => ({ value: a, label: a })),
      value: knowledgeStore.filterAgent === 'all' ? '' : knowledgeStore.filterAgent,
    },
  ]);

  function handleSearch(val) {
    searchInput = val;
    knowledgeStore.search(val);
  }

  function handleFilter(key, value) {
    if (key === 'category') {
      knowledgeStore.filterCategory = value || 'all';
      knowledgeStore.fetch();
    } else if (key === 'agent') {
      knowledgeStore.filterAgent = value || 'all';
      knowledgeStore.fetch();
    }
  }

  function clearFilters() {
    searchInput = '';
    knowledgeStore.filterCategory = 'all';
    knowledgeStore.filterAgent = 'all';
    knowledgeStore.search('');
  }

  function handleSort(key, dir) {
    sortKey = key;
    sortDir = dir;
  }

  function toggleExpand(row) {
    const next = new Set(expandedIds);
    if (next.has(row.id)) {
      next.delete(row.id);
    } else {
      next.add(row.id);
    }
    expandedIds = next;
  }

  function entryTypeColor(type) {
    const map = {
      decision: 'var(--accent)',
      finding: 'var(--info)',
      error: 'var(--error)',
      question: 'var(--warning)',
      task: 'var(--success)',
      summary: 'var(--fg-muted)',
      file_read: 'var(--fg-secondary)',
      note: 'var(--fg-secondary)',
      annotation: 'var(--info)',
    };
    return map[type] ?? 'var(--fg-muted)';
  }

  function entryTypeVariant(type) {
    const map = {
      decision: 'accent',
      finding: 'info',
      error: 'error',
      question: 'warning',
      task: 'success',
      summary: 'neutral',
    };
    return map[type] ?? 'neutral';
  }
</script>

<div class="panel knowledge-panel">
  <!-- Stats row -->
  <div class="stats-strip">
    <div class="stat-card">
      <div class="stat-value text-mono">{formatNumber(knowledgeStore.entries.length)}</div>
      <div class="stat-label">Entries</div>
    </div>
    <div class="stat-card">
      <div class="stat-value text-mono">{agents.length}</div>
      <div class="stat-label">Agents</div>
    </div>
    <div class="stat-card">
      <div class="stat-value text-mono">{categories.length}</div>
      <div class="stat-label">Categories</div>
    </div>
    <div class="stat-card">
      <div class="stat-value text-mono">{formatNumber(knowledgeStore.totalTokens)}</div>
      <div class="stat-label">Tokens</div>
    </div>
  </div>

  <!-- Search + filters -->
  <FilterBar
    search={searchInput}
    placeholder="Semantic search across all agents..."
    filters={filterDefs}
    resultCount={entries.length}
    onSearch={handleSearch}
    onFilter={handleFilter}
    onClear={clearFilters}
  >
    {#snippet actions()}
      <button class="btn btn-accent" onclick={() => handleSearch(searchInput)} disabled={knowledgeStore.loading}>
        {knowledgeStore.loading ? 'Searching...' : 'Search'}
      </button>
    {/snippet}
  </FilterBar>

  <div class="knowledge-layout">
    <!-- Entry table -->
    <div class="entry-list">
      {#if sortedEntries.length === 0 && !knowledgeStore.loading}
        <EmptyState
          icon={knowledgeStore.error ? '\u26A0' : '\u{1F4D6}'}
          heading={knowledgeStore.error ? `Error: ${knowledgeStore.error}` : 'No knowledge entries found'}
          description={knowledgeStore.error ? '' : 'Agents need active sessions with context.'}
        />
      {:else}
        <DataTable
          {columns}
          rows={sortedEntries}
          {sortKey}
          {sortDir}
          {expandedIds}
          idKey="id"
          stableLayout={true}
          loading={knowledgeStore.loading}
          onSort={handleSort}
          onToggleExpand={toggleExpand}
        >
          {#snippet row({ row: entry, expanded })}
            <td style="border-left: 3px solid {entryTypeColor(entry.entry_type)}">
              <Badge text={entry.entry_type} variant={entryTypeVariant(entry.entry_type)} />
            </td>
            <td class="entry-title">
              <span class="expand-icon">{expanded ? '\u25BC' : '\u25B6'}</span>
              {entry.title ?? '---'}
            </td>
            <td class="text-mono text-xs">{entry.agent_id || '---'}</td>
            <td class="text-mono text-xs text-muted" title={entry.file_path}>
              {#if entry.file_path}
                {entry.file_path.split('/').pop()}
              {:else}
                ---
              {/if}
            </td>
            <td class="text-mono text-xs" style="text-align: right">{formatNumber(entry.token_count ?? 0)}</td>
            <td class="text-mono text-xs text-muted">{relativeTime(entry.timestamp)}</td>
          {/snippet}
          {#snippet expandedRow({ row: entry })}
            <div class="expand-content">
              <div class="expand-meta">
                <span class="meta-item">Agent: <strong>{entry.agent_id}</strong></span>
                <span class="meta-item">Session: <strong>{entry.session_id?.slice(0, 8) ?? '---'}</strong></span>
                {#if entry.namespace}
                  <span class="meta-item">Namespace: <strong>{entry.namespace}</strong></span>
                {/if}
                {#if entry.file_path}
                  <span class="meta-item">File: <strong>{entry.file_path}</strong></span>
                {/if}
                {#if entry.tags?.length}
                  <span class="meta-item">Tags: {entry.tags.join(', ')}</span>
                {/if}
              </div>
              <pre class="content-pre">{entry.content ?? '(no content)'}</pre>
            </div>
          {/snippet}
        </DataTable>
      {/if}
    </div>

    <aside class="knowledge-rail">
      <section class="rail-card">
        <div class="rail-card-header">
          <span class="rail-card-title">Entry Mix</span>
          <span class="rail-card-meta text-mono">{averageTokens} avg tok</span>
        </div>
        {#if topCategories.length > 0}
          <div class="rail-list">
            {#each topCategories as category}
              <div class="rail-list-row">
                <Badge text={category.name} variant={entryTypeVariant(category.name)} />
                <span class="rail-count text-mono">{formatNumber(category.count)}</span>
              </div>
            {/each}
          </div>
        {:else}
          <div class="rail-empty">No categorized entries yet</div>
        {/if}
      </section>

      <section class="rail-card">
        <div class="rail-card-header">
          <span class="rail-card-title">Top Agents</span>
          <span class="rail-card-meta text-mono">{agents.length} active</span>
        </div>
        {#if topAgents.length > 0}
          <div class="rail-list">
            {#each topAgents as agent}
              <div class="rail-list-row">
                <span class="rail-label text-mono" title={agent.name}>{agent.name}</span>
                <span class="rail-count text-mono">{agent.count}</span>
              </div>
            {/each}
          </div>
        {:else}
          <div class="rail-empty">No agents in current result set</div>
        {/if}
      </section>

      <section class="rail-card">
        <div class="rail-card-header">
          <span class="rail-card-title">Busy Namespaces</span>
          <span class="rail-card-meta text-mono">{knowledgeStore.tokenBudget} budget</span>
        </div>
        {#if topNamespaces.length > 0}
          <div class="rail-stack">
            {#each topNamespaces as namespace}
              <div class="namespace-row">
                <span class="namespace-name text-mono" title={namespace.name}>{namespace.name}</span>
                <span class="namespace-count text-mono">{namespace.count}</span>
              </div>
            {/each}
          </div>
        {:else}
          <div class="rail-empty">Namespaces appear once agents write scoped context</div>
        {/if}
      </section>
    </aside>
  </div>
</div>

<style>
  .knowledge-panel {
    display: flex;
    flex-direction: column;
    overflow-y: auto;
    gap: 12px;
  }

  .stats-strip {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
    gap: 12px;
  }

  .stat-card {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--border-radius);
    padding: 10px 14px;
    text-align: left;
  }

  .stat-value {
    font-size: 18px;
    font-weight: 700;
    color: var(--fg-primary);
    line-height: 1.1;
  }

  .stat-label {
    font-size: 9px;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    color: var(--fg-muted);
    margin-top: 4px;
  }

  .knowledge-layout {
    flex: 1;
    min-height: 0;
    display: grid;
    grid-template-columns: minmax(0, 1fr) 280px;
    gap: 12px;
  }

  .entry-list {
    flex: 1;
    display: flex;
    flex-direction: column;
    min-height: 200px;
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--border-radius);
    overflow: hidden;
  }

  .knowledge-rail {
    display: flex;
    flex-direction: column;
    gap: 12px;
    min-height: 0;
  }

  .rail-card {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--border-radius);
    padding: 12px;
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .rail-card-header {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 8px;
  }

  .rail-card-title {
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--fg-muted);
  }

  .rail-card-meta {
    font-size: 10px;
    color: var(--fg-secondary);
  }

  .rail-list,
  .rail-stack {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .rail-list-row,
  .namespace-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
  }

  .rail-label,
  .namespace-name {
    color: var(--fg-primary);
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .rail-count,
  .namespace-count {
    color: var(--fg-secondary);
    flex-shrink: 0;
  }

  .rail-empty {
    font-size: 11px;
    color: var(--fg-muted);
    line-height: 1.5;
  }

  .entry-title {
    max-width: 300px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .expand-icon {
    font-size: 8px;
    color: var(--fg-muted);
    margin-right: 6px;
  }

  .expand-content {
    background: var(--bg-primary);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    padding: 10px 14px;
    margin-left: 16px;
  }

  .expand-meta {
    display: flex;
    flex-wrap: wrap;
    gap: 12px;
    margin-bottom: 8px;
    font-size: 11px;
    color: var(--fg-secondary);
  }

  .expand-meta .meta-item strong {
    color: var(--fg-primary);
  }

  .content-pre {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--fg-secondary);
    white-space: pre-wrap;
    word-break: break-word;
    line-height: 1.5;
    margin: 0;
  }

  @media (max-width: 1100px) {
    .knowledge-layout {
      grid-template-columns: 1fr;
    }
  }
</style>
