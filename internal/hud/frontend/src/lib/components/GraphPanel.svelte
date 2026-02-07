<script>
  import { graphStore } from '../stores/graph.svelte.ts';
  import Badge from '../widgets/Badge.svelte';
  import EntityGraph from '../widgets/EntityGraph.svelte';

  $effect(() => {
    graphStore.startPolling(10000);
    return () => { graphStore.stopPolling(); };
  });

  let stats = $derived(graphStore.stats ?? {});
  let entities = $derived(graphStore.entities ?? []);

  let searchQuery = $state('');
  let typeFilter = $state('all');
  let searchResults = $state([]);
  let selectedEntity = $state(null);
  let expandedEntities = $state(new Set());

  // Statistics
  let entityTypes = $derived.by(() => {
    const types = stats.entity_types ?? {};
    const entries = Object.entries(types).sort((a, b) => b[1] - a[1]);
    const max = entries.length > 0 ? entries[0][1] : 1;
    return entries.map(([type, count]) => ({ type, count, pct: (count / max) * 100 }));
  });

  let relationTypes = $derived.by(() => {
    const types = stats.relation_types ?? {};
    return Object.entries(types).sort((a, b) => b[1] - a[1]);
  });

  let namespaces = $derived.by(() => {
    const ns = stats.namespaces ?? {};
    return Object.entries(ns).sort((a, b) => b[1] - a[1]);
  });

  let uniqueTypes = $derived.by(() => {
    const types = new Set();
    entities.forEach(e => { if (e.type) types.add(e.type); });
    return ['all', ...Array.from(types).sort()];
  });

  // Search
  async function doSearch() {
    const type = typeFilter === 'all' ? '' : typeFilter;
    await graphStore.search(searchQuery, type, 50);
    searchResults = graphStore.entities ?? [];
  }

  function handleSearchKey(e) {
    if (e.key === 'Enter') doSearch();
  }

  // Display results: use searchResults if search was performed, else entities
  let displayEntities = $derived.by(() => {
    if (searchResults.length > 0) return searchResults;
    return entities;
  });

  function toggleExpand(entityId) {
    const next = new Set(expandedEntities);
    if (next.has(entityId)) {
      next.delete(entityId);
    } else {
      next.add(entityId);
    }
    expandedEntities = next;
  }

  function selectEntity(entity) {
    selectedEntity = selectedEntity?.id === entity.id ? null : entity;
  }

  function typeVariant(type) {
    const map = {
      service: 'info',
      file: 'accent',
      function: 'success',
      variable: 'warning',
      class: 'accent',
      module: 'info',
      config: 'warning',
      person: 'success',
    };
    return map[type?.toLowerCase()] ?? 'info';
  }

  function typeBarColor(type) {
    const map = {
      service: 'var(--info)',
      file: 'var(--accent)',
      function: 'var(--success)',
      variable: 'var(--warning)',
      class: 'var(--accent)',
      module: 'var(--info)',
    };
    return map[type?.toLowerCase()] ?? 'var(--fg-muted)';
  }

  // Entity inbound/outbound relations
  function inboundRelations(entity) {
    return entity.inbound_relations ?? entity.relations?.filter(r => r.target === entity.id) ?? [];
  }

  function outboundRelations(entity) {
    return entity.outbound_relations ?? entity.relations?.filter(r => r.source === entity.id) ?? [];
  }
</script>

<div class="panel graph-panel">
  <!-- Left column: Statistics -->
  <div class="stats-column">
    <!-- Entity type histogram -->
    <div class="stats-section">
      <div class="section-header">
        <span class="section-title">Entity Types</span>
        <span class="text-mono text-xs text-muted">{stats.total_entities ?? 0} total</span>
      </div>
      <div class="histogram">
        {#each entityTypes as { type, count, pct } (type)}
          <div class="histogram-row">
            <span class="hist-label truncate">{type}</span>
            <div class="hist-bar-track">
              <div
                class="hist-bar"
                style="width: {pct}%; background: {typeBarColor(type)}"
              ></div>
            </div>
            <span class="hist-count text-mono">{count}</span>
          </div>
        {:else}
          <div class="text-muted text-sm" style="padding: 12px">No entity types</div>
        {/each}
      </div>
    </div>

    <!-- Relation type list -->
    <div class="stats-section">
      <div class="section-header">
        <span class="section-title">Relation Types</span>
        <span class="text-mono text-xs text-muted">{stats.total_relations ?? 0} total</span>
      </div>
      <div class="relation-list">
        {#each relationTypes as [type, count] (type)}
          <div class="rel-row">
            <span class="rel-name text-mono">{type}</span>
            <span class="rel-count text-mono text-muted">{count}</span>
          </div>
        {:else}
          <div class="text-muted text-sm" style="padding: 12px">No relations</div>
        {/each}
      </div>
    </div>

    <!-- Namespaces -->
    <div class="stats-section">
      <div class="section-header">
        <span class="section-title">Namespaces</span>
      </div>
      <div class="namespace-list">
        {#each namespaces as [ns, count] (ns)}
          <div class="ns-row">
            <span class="ns-name text-mono truncate">{ns}</span>
            <span class="ns-count text-mono text-muted">{count}</span>
          </div>
        {:else}
          <div class="text-muted text-sm" style="padding: 12px">No namespaces</div>
        {/each}
      </div>
    </div>
  </div>

  <!-- Right column: Explorer -->
  <div class="explorer-column">
    <!-- Search bar -->
    <div class="explorer-search">
      <input
        type="text"
        placeholder="Search entities..."
        bind:value={searchQuery}
        onkeydown={handleSearchKey}
        class="search-input"
      />
      <select bind:value={typeFilter}>
        {#each uniqueTypes as t}
          <option value={t}>{t === 'all' ? 'All Types' : t}</option>
        {/each}
      </select>
      <button class="btn btn-primary" onclick={doSearch}>Search</button>
    </div>

    <!-- Results list -->
    <div class="explorer-results">
      {#each displayEntities as entity (entity.id)}
        <div class="entity-card" class:entity-selected={selectedEntity?.id === entity.id}>
          <button class="entity-header" onclick={() => { toggleExpand(entity.id); selectEntity(entity); }}>
            <Badge text={entity.type ?? 'entity'} variant={typeVariant(entity.type)} />
            <span class="entity-name">{entity.name ?? entity.id}</span>
            <span class="entity-chevron">
              {expandedEntities.has(entity.id) ? '\u25BC' : '\u25B6'}
            </span>
          </button>

          {#if expandedEntities.has(entity.id)}
            <div class="entity-detail">
              <!-- Properties -->
              {#if entity.properties && Object.keys(entity.properties).length > 0}
                <div class="detail-group">
                  <div class="detail-group-title">Properties</div>
                  <div class="props-table">
                    {#each Object.entries(entity.properties) as [key, value] (key)}
                      <div class="prop-row">
                        <span class="prop-key text-mono">{key}</span>
                        <span class="prop-value text-mono">{typeof value === 'object' ? JSON.stringify(value) : String(value)}</span>
                      </div>
                    {/each}
                  </div>
                </div>
              {/if}

              <!-- Inbound relations -->
              {#if inboundRelations(entity).length > 0}
                <div class="detail-group">
                  <div class="detail-group-title">Inbound Relations</div>
                  {#each inboundRelations(entity) as rel}
                    <div class="rel-detail-row">
                      <span class="text-mono text-muted">{rel.source_name ?? rel.source ?? '?'}</span>
                      <span class="rel-arrow">&#8594;</span>
                      <Badge text={rel.type ?? 'related'} variant="info" />
                      <span class="rel-arrow">&#8594;</span>
                      <span class="text-mono">{entity.name ?? entity.id}</span>
                    </div>
                  {/each}
                </div>
              {/if}

              <!-- Outbound relations -->
              {#if outboundRelations(entity).length > 0}
                <div class="detail-group">
                  <div class="detail-group-title">Outbound Relations</div>
                  {#each outboundRelations(entity) as rel}
                    <div class="rel-detail-row">
                      <span class="text-mono">{entity.name ?? entity.id}</span>
                      <span class="rel-arrow">&#8594;</span>
                      <Badge text={rel.type ?? 'related'} variant="accent" />
                      <span class="rel-arrow">&#8594;</span>
                      <span class="text-mono text-muted">{rel.target_name ?? rel.target ?? '?'}</span>
                    </div>
                  {/each}
                </div>
              {/if}
            </div>
          {/if}
        </div>
      {:else}
        <div class="empty-state">
          <span class="text-muted">Search for entities or browse the graph</span>
        </div>
      {/each}
    </div>

    <!-- Bottom: EntityGraph mini-visualization -->
    <div class="graph-viz">
      <EntityGraph entities={displayEntities} relations={stats.all_relations ?? []} />
    </div>
  </div>
</div>

<style>
  .graph-panel {
    display: flex;
    overflow: hidden;
    gap: 0;
  }

  /* Stats column */
  .stats-column {
    width: 40%;
    min-width: 240px;
    border-right: 1px solid var(--border);
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    gap: 0;
  }

  .stats-section {
    padding: 12px 14px;
    border-bottom: 1px solid var(--border);
  }

  .stats-section:last-child {
    border-bottom: none;
  }

  /* Histogram */
  .histogram {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .histogram-row {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 11px;
  }

  .hist-label {
    width: 80px;
    flex-shrink: 0;
    color: var(--fg-secondary);
    text-transform: capitalize;
  }

  .hist-bar-track {
    flex: 1;
    height: 14px;
    background: var(--bg-tertiary);
    border-radius: 3px;
    overflow: hidden;
  }

  .hist-bar {
    height: 100%;
    border-radius: 3px;
    transition: width 0.3s ease;
    min-width: 2px;
  }

  .hist-count {
    width: 36px;
    text-align: right;
    color: var(--fg-muted);
    font-size: 10px;
    flex-shrink: 0;
  }

  /* Relation list */
  .relation-list, .namespace-list {
    display: flex;
    flex-direction: column;
  }

  .rel-row, .ns-row {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 4px 0;
    font-size: 11px;
    border-bottom: 1px solid rgba(48, 54, 61, 0.3);
  }

  .rel-row:last-child, .ns-row:last-child {
    border-bottom: none;
  }

  .rel-name, .ns-name {
    color: var(--fg-secondary);
  }

  .rel-count, .ns-count {
    font-size: 10px;
  }

  /* Explorer column */
  .explorer-column {
    flex: 1;
    display: flex;
    flex-direction: column;
    overflow: hidden;
    min-width: 0;
  }

  .explorer-search {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 10px 14px;
    border-bottom: 1px solid var(--border);
  }

  .search-input {
    flex: 1;
    min-width: 120px;
  }

  .explorer-results {
    flex: 1;
    overflow-y: auto;
    border-bottom: 1px solid var(--border);
  }

  /* Entity card */
  .entity-card {
    border-bottom: 1px solid var(--border);
  }

  .entity-card:last-child {
    border-bottom: none;
  }

  .entity-selected {
    background: rgba(88, 166, 255, 0.05);
  }

  .entity-header {
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    padding: 8px 14px;
    text-align: left;
    cursor: pointer;
    font-size: 12px;
    transition: background 0.1s;
  }

  .entity-header:hover {
    background: var(--bg-tertiary);
  }

  .entity-name {
    flex: 1;
    color: var(--fg-primary);
    font-weight: 500;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .entity-chevron {
    font-size: 10px;
    color: var(--fg-muted);
    flex-shrink: 0;
  }

  /* Entity detail */
  .entity-detail {
    padding: 0 14px 10px 14px;
    background: rgba(13, 17, 23, 0.3);
  }

  .detail-group {
    margin-top: 8px;
  }

  .detail-group-title {
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    color: var(--fg-muted);
    margin-bottom: 4px;
  }

  .props-table {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .prop-row {
    display: flex;
    gap: 12px;
    font-size: 11px;
    padding: 2px 0;
  }

  .prop-key {
    color: var(--accent);
    min-width: 80px;
    flex-shrink: 0;
  }

  .prop-value {
    color: var(--fg-secondary);
    word-break: break-all;
  }

  .rel-detail-row {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 11px;
    padding: 3px 0;
  }

  .rel-arrow {
    color: var(--fg-muted);
    font-size: 10px;
  }

  /* Graph viz */
  .graph-viz {
    height: 300px;
    min-height: 200px;
    border-top: 1px solid var(--border);
    background: var(--bg-secondary);
    overflow: hidden;
  }
</style>
