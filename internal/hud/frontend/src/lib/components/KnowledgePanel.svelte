<script>
  import { knowledgeStore } from '../stores/knowledge.svelte.ts';
  import { formatNumber, relativeTime } from '../utils/format.ts';
  import Badge from '../widgets/Badge.svelte';

  $effect(() => {
    knowledgeStore.startPolling(30000);
    return () => { knowledgeStore.stopPolling(); };
  });

  let entries = $derived(knowledgeStore.filteredEntries);
  let categories = $derived(knowledgeStore.categories);
  let agents = $derived(knowledgeStore.agents);

  let searchInput = $state('');
  let expandedItems = $state(new Set());

  function handleSearch() {
    knowledgeStore.search(searchInput);
  }

  function toggleExpand(id) {
    const next = new Set(expandedItems);
    if (next.has(id)) {
      next.delete(id);
    } else {
      next.add(id);
    }
    expandedItems = next;
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
  <div class="toolbar">
    <input
      type="text"
      placeholder="Semantic search across all agents..."
      bind:value={searchInput}
      onkeydown={(e) => e.key === 'Enter' && handleSearch()}
      class="search-input"
    />
    <button class="btn btn-accent" onclick={handleSearch} disabled={knowledgeStore.loading}>
      {knowledgeStore.loading ? 'Searching...' : 'Search'}
    </button>

    <select bind:value={knowledgeStore.filterCategory} onchange={() => knowledgeStore.fetch()}>
      <option value="all">All types</option>
      {#each categories as cat}
        <option value={cat}>{cat}</option>
      {/each}
    </select>

    <select bind:value={knowledgeStore.filterAgent} onchange={() => knowledgeStore.fetch()}>
      <option value="all">All agents</option>
      {#each agents as agent}
        <option value={agent}>{agent}</option>
      {/each}
    </select>

    <span class="text-muted text-xs text-mono">{entries.length} results</span>
  </div>

  <!-- Entry list -->
  <div class="entry-list">
    <div class="table-wrap">
      <table>
        <thead>
          <tr>
            <th>Type</th>
            <th>Title</th>
            <th>Agent</th>
            <th>File</th>
            <th>Tokens</th>
            <th>Time</th>
          </tr>
        </thead>
        <tbody>
          {#each entries as entry (entry.id)}
            <tr
              class="row-enter entry-row"
              class:expanded-row={expandedItems.has(entry.id)}
              style="border-left: 3px solid {entryTypeColor(entry.entry_type)}"
            >
              <td>
                <Badge text={entry.entry_type} variant={entryTypeVariant(entry.entry_type)} />
              </td>
              <td class="entry-title">
                <button class="expand-btn" onclick={() => toggleExpand(entry.id)} title="Expand">
                  <span class="expand-icon">{expandedItems.has(entry.id) ? '\u25BC' : '\u25B6'}</span>
                  {entry.title ?? '---'}
                </button>
              </td>
              <td class="text-mono text-xs">{entry.agent_id || '---'}</td>
              <td class="text-mono text-xs text-muted" title={entry.file_path}>
                {#if entry.file_path}
                  {entry.file_path.split('/').pop()}
                {:else}
                  ---
                {/if}
              </td>
              <td class="text-mono text-xs">{formatNumber(entry.token_count ?? 0)}</td>
              <td class="text-mono text-xs text-muted">{relativeTime(entry.timestamp)}</td>
            </tr>
            {#if expandedItems.has(entry.id)}
              <tr class="expand-content-row">
                <td colspan="6">
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
                </td>
              </tr>
            {/if}
          {:else}
            <tr>
              <td colspan="6" class="empty-cell">
                {#if knowledgeStore.loading}
                  Loading cross-agent knowledge...
                {:else if knowledgeStore.error}
                  Error: {knowledgeStore.error}
                {:else}
                  No knowledge entries found. Agents need active sessions with context.
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
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
    grid-template-columns: repeat(4, 1fr);
    gap: 12px;
  }

  .stat-card {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--border-radius);
    padding: 12px 16px;
    text-align: center;
  }

  .stat-value {
    font-size: 20px;
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

  .toolbar {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 12px;
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--border-radius);
  }

  .search-input {
    flex: 1;
    min-width: 200px;
  }

  .toolbar select {
    max-width: 140px;
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

  .table-wrap {
    flex: 1;
    overflow-y: auto;
  }

  .entry-row {
    transition: border-left-color 0.15s;
  }

  .entry-title {
    max-width: 300px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .expand-btn {
    background: none;
    border: none;
    padding: 0;
    cursor: pointer;
    display: flex;
    align-items: center;
    gap: 6px;
    color: var(--fg-primary);
    font-weight: 500;
    font-size: 12px;
    max-width: 300px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .expand-btn:hover {
    color: var(--info);
  }

  .expand-icon {
    font-size: 8px;
    flex-shrink: 0;
    width: 10px;
    color: var(--fg-muted);
  }

  .expanded-row td {
    border-bottom: none !important;
  }

  .expand-content-row td {
    padding: 0 10px 10px !important;
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

  .meta-item strong {
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

  .empty-cell {
    text-align: center;
    color: var(--fg-muted);
    padding: 24px 10px !important;
  }
</style>
