<script>
  /**
   * GraphFullView — entire Graph panel UI (stats column + explorer with list + viz)
   * plus the 4 modals (Add Entity / Add Relation / Find Path / Delete Confirm) and the
   * detail drawer. Extracted as a single component during the Slice B2.5 panel decomp
   * so SandboxPanel.svelte shrinks to a composition shell. Further per-card extraction
   * (GraphStats / GraphExplorer / GraphActions) is a follow-up slice.
   */
  import { graphStore } from '../../stores/graph.svelte.ts';
  import { toastStore } from '../../stores/toasts.svelte.ts';
  import Badge from '../../widgets/Badge.svelte';
  import EntityGraph from '../../widgets/EntityGraph.svelte';
  import Modal from '../../widgets/Modal.svelte';
  import ConfirmDialog from '../../widgets/ConfirmDialog.svelte';
  import EmptyState from '../shared/EmptyState.svelte';
  import FilterBar from '../shared/FilterBar.svelte';
  import DetailDrawer from '../shared/DetailDrawer.svelte';
  import { typeVariant, typeBarColor } from '../../utils/graphHelpers';

  let stats = $derived(graphStore.stats ?? {});
  let entities = $derived(graphStore.entities ?? []);

  let searchQuery = $state('');
  let typeFilter = $state('all');
  let searchResults = $state([]);
  let selectedEntity = $state(null);
  let expandedEntities = $state(new Set());

  // Entity detail (fetched on expand)
  let entityDetails = $state({});

  // Add entity modal
  let showAddEntityModal = $state(false);
  let newEntityName = $state('');
  let newEntityType = $state('service');
  let newEntityNamespace = $state('');
  let newEntityProps = $state('');
  let addingEntity = $state(false);

  // Add relation modal
  let showAddRelationModal = $state(false);
  let relSourceId = $state('');
  let relTargetId = $state('');
  let relType = $state('');
  let addingRelation = $state(false);

  // Delete confirm
  let showDeleteConfirm = $state(false);
  let deleteTarget = $state(null);
  let deleteType = $state('entity'); // 'entity' | 'relation'

  // Path finder
  let showPathFinder = $state(false);
  let pathFromId = $state('');
  let pathToId = $state('');
  let pathResult = $state(null);
  let findingPath = $state(false);

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

  function clearFilters() {
    searchQuery = '';
    typeFilter = 'all';
    searchResults = [];
  }

  function handleSearchKey(e) {
    if (e.key === 'Enter') doSearch();
  }

  let displayEntities = $derived.by(() => {
    if (searchResults.length > 0) return searchResults;
    return entities;
  });

  async function toggleExpand(entityId) {
    const next = new Set(expandedEntities);
    if (next.has(entityId)) {
      next.delete(entityId);
    } else {
      next.add(entityId);
      // Fetch detail if not cached
      if (!entityDetails[entityId]) {
        const detail = await graphStore.getEntityDetail(entityId);
        if (detail) {
          entityDetails = { ...entityDetails, [entityId]: detail };
        }
      }
    }
    expandedEntities = next;
  }

  function selectEntity(entity) {
    selectedEntity = selectedEntity?.id === entity.id ? null : entity;
  }

  // --- CRUD ---
  function openAddEntityModal() {
    newEntityName = '';
    newEntityType = 'service';
    newEntityNamespace = '';
    newEntityProps = '';
    addingEntity = false;
    showAddEntityModal = true;
  }

  async function submitAddEntity() {
    if (!newEntityName.trim()) return;
    addingEntity = true;
    let props = undefined;
    if (newEntityProps.trim()) {
      try {
        props = JSON.parse(newEntityProps);
      } catch {
        toastStore.error('Invalid JSON for properties');
        addingEntity = false;
        return;
      }
    }
    const ok = await graphStore.addEntity(
      newEntityName.trim(),
      newEntityType.trim(),
      newEntityNamespace.trim(),
      props,
    );
    if (ok) {
      toastStore.success('Entity created');
      showAddEntityModal = false;
    } else {
      toastStore.error(graphStore.error ?? 'Failed to create entity');
    }
    addingEntity = false;
  }

  function openAddRelationModal() {
    relSourceId = '';
    relTargetId = '';
    relType = '';
    addingRelation = false;
    showAddRelationModal = true;
  }

  async function submitAddRelation() {
    if (!relSourceId.trim() || !relTargetId.trim() || !relType.trim()) return;
    addingRelation = true;
    const ok = await graphStore.addRelation(relSourceId.trim(), relTargetId.trim(), relType.trim());
    if (ok) {
      toastStore.success('Relation created');
      showAddRelationModal = false;
    } else {
      toastStore.error(graphStore.error ?? 'Failed to create relation');
    }
    addingRelation = false;
  }

  function confirmDeleteEntity(entity) {
    deleteTarget = entity;
    deleteType = 'entity';
    showDeleteConfirm = true;
  }

  function confirmDeleteRelation(rel) {
    deleteTarget = rel;
    deleteType = 'relation';
    showDeleteConfirm = true;
  }

  async function executeDelete() {
    if (!deleteTarget) return;
    let ok;
    if (deleteType === 'entity') {
      ok = await graphStore.deleteEntity(deleteTarget.id);
    } else {
      ok = await graphStore.deleteRelation(deleteTarget.id ?? deleteTarget.relation_id);
    }
    if (ok) {
      toastStore.success(`${deleteType === 'entity' ? 'Entity' : 'Relation'} deleted`);
    } else {
      toastStore.error(graphStore.error ?? 'Failed to delete');
    }
    showDeleteConfirm = false;
    deleteTarget = null;
  }

  // Path finder
  function openPathFinder() {
    pathFromId = '';
    pathToId = '';
    pathResult = null;
    findingPath = false;
    showPathFinder = true;
  }

  async function submitFindPath() {
    if (!pathFromId.trim() || !pathToId.trim()) return;
    findingPath = true;
    pathResult = await graphStore.findPath(pathFromId.trim(), pathToId.trim());
    findingPath = false;
    if (pathResult === null) {
      toastStore.error(graphStore.error ?? 'Path search failed');
    }
  }

  function getDetail(entity) {
    return entityDetails[entity.id] ?? entity;
  }

  function inboundRelations(entity) {
    const d = getDetail(entity);
    return d.inbound_relations ?? d.relations?.filter(r => r.target === entity.id) ?? [];
  }

  function outboundRelations(entity) {
    const d = getDetail(entity);
    return d.outbound_relations ?? d.relations?.filter(r => r.source === entity.id) ?? [];
  }

  // DetailDrawer state
  let drawerEntity = $state(null);
  let drawerEntityDetail = $state(null);
  let loadingDrawerDetail = $state(false);

  async function openEntityDrawer(entity) {
    drawerEntity = entity;
    loadingDrawerDetail = true;
    const detail = await graphStore.getEntityDetail(entity.id);
    drawerEntityDetail = detail;
    loadingDrawerDetail = false;
  }

  function closeEntityDrawer() {
    drawerEntity = null;
    drawerEntityDetail = null;
  }
</script>

<div class="panel graph-panel">
  <!-- Left column: Statistics -->
  <div class="stats-column">
    <!-- Action buttons -->
    <div class="graph-actions">
      <button class="btn btn-success" onclick={openAddEntityModal}>+ Entity</button>
      <button class="btn btn-primary" onclick={openAddRelationModal}>+ Relation</button>
      <button class="btn btn-ghost" onclick={openPathFinder}>Find Path</button>
    </div>

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
          <EmptyState icon={'\u25C8'} heading="No entity types" compact />
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
          <EmptyState icon={'\u25C8'} heading="No relations" compact />
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
          <EmptyState icon={'\u25C8'} heading="No namespaces" compact />
        {/each}
      </div>
    </div>
  </div>

  <!-- Right column: Explorer -->
  <div class="explorer-column">
    <FilterBar
      search={searchQuery}
      placeholder="Search entities..."
      filters={[{
        key: 'type',
        label: 'All Types',
        options: uniqueTypes.filter(t => t !== 'all').map(t => ({ value: t, label: t })),
        value: typeFilter === 'all' ? '' : typeFilter,
      }]}
      resultCount={displayEntities.length}
      onSearch={(val) => { searchQuery = val; }}
      onFilter={(key, val) => { typeFilter = val || 'all'; }}
      onClear={clearFilters}
    >
      {#snippet actions()}
        <button class="btn btn-primary" onclick={doSearch}>Search</button>
      {/snippet}
    </FilterBar>

    <!-- Results list -->
    <div class="explorer-results">
      {#each displayEntities as entity (entity.id)}
        <div class="entity-card" class:entity-selected={selectedEntity?.id === entity.id}>
          <button class="entity-header" onclick={() => { toggleExpand(entity.id); selectEntity(entity); }}>
            <Badge text={entity.type ?? 'entity'} variant={typeVariant(entity.type)} />
            <span class="entity-name entity-name-link" onclick={(e) => { e.stopPropagation(); openEntityDrawer(entity); }} onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); e.stopPropagation(); openEntityDrawer(entity); } }} role="button" tabindex="0">{entity.name ?? entity.id}</span>
            <span class="entity-chevron">
              {expandedEntities.has(entity.id) ? '\u25BC' : '\u25B6'}
            </span>
          </button>

          {#if expandedEntities.has(entity.id)}
            <div class="entity-detail">
              <!-- Properties -->
              {#if getDetail(entity).properties && Object.keys(getDetail(entity).properties).length > 0}
                <div class="detail-group">
                  <div class="detail-group-title">Properties</div>
                  <div class="props-table">
                    {#each Object.entries(getDetail(entity).properties) as [key, value] (key)}
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
                      <Badge text={rel.type ?? rel.relation_type ?? 'related'} variant="info" />
                      <span class="rel-arrow">&#8594;</span>
                      <span class="text-mono">{entity.name ?? entity.id}</span>
                      <button class="action-btn delete-btn-sm" onclick={() => confirmDeleteRelation(rel)} title="Delete relation">&#10005;</button>
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
                      <Badge text={rel.type ?? rel.relation_type ?? 'related'} variant="accent" />
                      <span class="rel-arrow">&#8594;</span>
                      <span class="text-mono text-muted">{rel.target_name ?? rel.target ?? '?'}</span>
                      <button class="action-btn delete-btn-sm" onclick={() => confirmDeleteRelation(rel)} title="Delete relation">&#10005;</button>
                    </div>
                  {/each}
                </div>
              {/if}

              <!-- Entity actions -->
              <div class="entity-actions">
                <button class="btn btn-danger" onclick={() => confirmDeleteEntity(entity)}>Delete Entity</button>
              </div>
            </div>
          {/if}
        </div>
      {:else}
        <EmptyState icon={'\u25C8'} heading="No entities" description="Search for entities or browse the graph" compact />
      {/each}
    </div>

    <!-- Bottom: EntityGraph mini-visualization -->
    <div class="graph-viz">
      <EntityGraph entities={displayEntities} relations={stats.all_relations ?? []} />
    </div>
  </div>
</div>

<!-- Add Entity Modal -->
<Modal open={showAddEntityModal} title="Seed Entity" onClose={() => showAddEntityModal = false}>
  <form class="modal-form" onsubmit={(e) => { e.preventDefault(); submitAddEntity(); }}>
    <div class="form-field">
      <label class="form-label" for="ent-name">Name</label>
      <input id="ent-name" type="text" bind:value={newEntityName} placeholder="e.g. HUD Server" required />
    </div>
    <div class="form-row">
      <div class="form-field">
        <label class="form-label" for="ent-type">Type</label>
        <input id="ent-type" type="text" bind:value={newEntityType} placeholder="service, file, function..." />
      </div>
      <div class="form-field">
        <label class="form-label" for="ent-ns">Namespace</label>
        <input id="ent-ns" type="text" bind:value={newEntityNamespace} placeholder="project/module" />
      </div>
    </div>
    <div class="form-field">
      <label class="form-label" for="ent-props">Properties (JSON, optional)</label>
      <textarea id="ent-props" bind:value={newEntityProps} placeholder={'{"language": "go", "version": "1.22"}'} rows="3"></textarea>
    </div>
    <div class="form-actions">
      <button type="button" class="btn btn-ghost" onclick={() => showAddEntityModal = false}>Cancel</button>
      <button type="submit" class="btn btn-success" disabled={addingEntity || !newEntityName.trim()}>
        {addingEntity ? 'Creating...' : 'Create Entity'}
      </button>
    </div>
  </form>
</Modal>

<!-- Add Relation Modal -->
<Modal open={showAddRelationModal} title="Add Relation" onClose={() => showAddRelationModal = false}>
  <form class="modal-form" onsubmit={(e) => { e.preventDefault(); submitAddRelation(); }}>
    <div class="form-field">
      <label class="form-label" for="rel-source">Source Entity ID</label>
      <input id="rel-source" type="text" bind:value={relSourceId} placeholder="Entity ID or name..." required />
    </div>
    <div class="form-field">
      <label class="form-label" for="rel-target">Target Entity ID</label>
      <input id="rel-target" type="text" bind:value={relTargetId} placeholder="Entity ID or name..." required />
    </div>
    <div class="form-field">
      <label class="form-label" for="rel-type">Relation Type</label>
      <input id="rel-type" type="text" bind:value={relType} placeholder="depends_on, contains, calls..." required />
    </div>
    <div class="form-actions">
      <button type="button" class="btn btn-ghost" onclick={() => showAddRelationModal = false}>Cancel</button>
      <button type="submit" class="btn btn-primary" disabled={addingRelation || !relSourceId.trim() || !relTargetId.trim() || !relType.trim()}>
        {addingRelation ? 'Creating...' : 'Create Relation'}
      </button>
    </div>
  </form>
</Modal>

<!-- Path Finder Modal -->
<Modal open={showPathFinder} title="Find Path" onClose={() => showPathFinder = false}>
  <form class="modal-form" onsubmit={(e) => { e.preventDefault(); submitFindPath(); }}>
    <div class="form-field">
      <label class="form-label" for="path-from">From Entity ID</label>
      <input id="path-from" type="text" bind:value={pathFromId} placeholder="Start entity..." required />
    </div>
    <div class="form-field">
      <label class="form-label" for="path-to">To Entity ID</label>
      <input id="path-to" type="text" bind:value={pathToId} placeholder="End entity..." required />
    </div>
    <div class="form-actions">
      <button type="button" class="btn btn-ghost" onclick={() => showPathFinder = false}>Close</button>
      <button type="submit" class="btn btn-primary" disabled={findingPath || !pathFromId.trim() || !pathToId.trim()}>
        {findingPath ? 'Searching...' : 'Find Path'}
      </button>
    </div>
  </form>

  {#if pathResult !== null}
    <div class="path-result">
      <div class="detail-group-title" style="margin-top: 12px">Path ({pathResult.length} hops)</div>
      {#if pathResult.length === 0}
        <div class="text-muted text-sm" style="padding: 8px 0">No path found</div>
      {:else}
        <div class="path-steps">
          {#each pathResult as node, i}
            <div class="path-step">
              <Badge text={node.type ?? node.entity_type ?? 'entity'} variant={typeVariant(node.type ?? node.entity_type)} />
              <span class="text-mono">{node.name ?? node.id}</span>
            </div>
            {#if i < pathResult.length - 1}
              <span class="path-arrow">&#8594;</span>
            {/if}
          {/each}
        </div>
      {/if}
    </div>
  {/if}
</Modal>

<!-- Delete Confirm -->
<ConfirmDialog
  open={showDeleteConfirm}
  title="Delete {deleteType === 'entity' ? 'Entity' : 'Relation'}"
  message={deleteTarget ? `Delete ${deleteType} "${deleteTarget.name ?? deleteTarget.id}"? This cannot be undone.` : ''}
  confirmLabel="Delete"
  destructive={true}
  onConfirm={executeDelete}
  onCancel={() => { showDeleteConfirm = false; deleteTarget = null; }}
/>

<DetailDrawer
  open={!!drawerEntity}
  title={drawerEntity?.name ?? '---'}
  subtitle={drawerEntity?.type ?? drawerEntity?.entity_type ?? ''}
  onClose={closeEntityDrawer}
>
  {#snippet header()}
    {#if drawerEntity}
      <div class="detail-stats">
        <div class="stat-chip">
          <Badge text={drawerEntity.type ?? drawerEntity.entity_type ?? 'unknown'} variant={typeVariant(drawerEntity.type ?? drawerEntity.entity_type)} />
        </div>
      </div>
    {/if}
  {/snippet}

  {#if loadingDrawerDetail}
    <div class="loading-bar"><div class="loading-bar-inner"></div></div>
  {:else if drawerEntityDetail}
    {#if Object.keys(drawerEntityDetail.properties ?? {}).length > 0}
      <div class="section">
        <div class="section-title text-xs uppercase text-muted">Properties</div>
        <div class="drawer-props-table">
          {#each Object.entries(drawerEntityDetail.properties) as [key, value]}
            <div class="drawer-prop-row">
              <span class="drawer-prop-key text-mono text-xs">{key}</span>
              <span class="drawer-prop-value text-mono text-xs text-secondary">{typeof value === 'object' ? JSON.stringify(value) : String(value)}</span>
            </div>
          {/each}
        </div>
      </div>
    {/if}
    {#if drawerEntityDetail.inbound_relations?.length}
      <div class="section">
        <div class="section-title text-xs uppercase text-muted">Inbound Relations ({drawerEntityDetail.inbound_relations.length})</div>
        {#each drawerEntityDetail.inbound_relations as rel}
          <div class="drawer-relation-row">
            <span class="text-mono text-sm">{rel.source_name ?? rel.source ?? '?'}</span>
            <Badge text={rel.type ?? rel.relation_type ?? 'related'} variant="accent" />
          </div>
        {/each}
      </div>
    {/if}
    {#if drawerEntityDetail.outbound_relations?.length}
      <div class="section">
        <div class="section-title text-xs uppercase text-muted">Outbound Relations ({drawerEntityDetail.outbound_relations.length})</div>
        {#each drawerEntityDetail.outbound_relations as rel}
          <div class="drawer-relation-row">
            <Badge text={rel.type ?? rel.relation_type ?? 'related'} variant="accent" />
            <span class="text-mono text-sm">{rel.target_name ?? rel.target ?? '?'}</span>
          </div>
        {/each}
      </div>
    {/if}
  {:else}
    <EmptyState icon={'\u25C8'} heading="No detail available" compact />
  {/if}

  {#snippet footer()}
    {#if drawerEntity}
      <button class="btn btn-danger btn-sm" onclick={() => { closeEntityDrawer(); confirmDeleteEntity(drawerEntity); }}>Delete Entity</button>
    {/if}
  {/snippet}
</DetailDrawer>

<style>
  .graph-panel {
    display: flex;
    overflow: hidden;
    gap: 0;
  }

  /* Action buttons */
  .graph-actions {
    display: flex;
    gap: var(--space-2);
    padding: var(--space-2) var(--space-3);
    border-bottom: 1px solid var(--border);
    position: relative;
  }

  .graph-actions::after {
    content: '';
    position: absolute;
    bottom: 0;
    left: 10%;
    right: 10%;
    height: 1px;
    background: linear-gradient(90deg, transparent, rgba(0, 200, 255, 0.06) 50%, transparent);
    pointer-events: none;
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
    padding: var(--space-3) var(--space-3);
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
    gap: var(--space-2);
    font-size: var(--text-xs);
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
    border-radius: var(--radius-sm);
    overflow: hidden;
  }

  .hist-bar {
    height: 100%;
    border-radius: var(--radius-sm);
    transition: width var(--transition-normal);
    min-width: 2px;
  }

  .hist-count {
    width: 36px;
    text-align: right;
    color: var(--fg-dim);
    font-size: var(--text-xs);
    font-family: var(--font-mono);
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
    font-size: var(--text-xs);
    border-bottom: 1px solid var(--border-subtle);
    transition: background var(--transition-fast);
  }

  .rel-row:hover, .ns-row:hover {
    background: var(--bg-elevated);
  }

  .rel-row:last-child, .ns-row:last-child {
    border-bottom: none;
  }

  .rel-name, .ns-name {
    color: var(--fg-secondary);
  }

  .rel-count, .ns-count {
    font-size: var(--text-xs);
    font-family: var(--font-mono);
    color: var(--fg-dim);
  }

  /* Explorer column */
  .explorer-column {
    flex: 1;
    display: flex;
    flex-direction: column;
    overflow: hidden;
    min-width: 0;
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
    background: var(--info-dim);
  }

  .entity-header {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    width: 100%;
    padding: var(--space-2) var(--space-3);
    text-align: left;
    cursor: pointer;
    font-size: var(--text-sm);
    transition: background var(--transition-fast);
  }

  .entity-header:hover {
    background: var(--bg-elevated);
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
    font-size: var(--text-xs);
    color: var(--fg-muted);
    flex-shrink: 0;
    transition: transform var(--transition-fast);
  }

  /* Entity detail */
  .entity-detail {
    padding: 0 var(--space-3) var(--space-2) var(--space-3);
    background: var(--bg-secondary);
    position: relative;
  }

  .entity-detail::before {
    content: '';
    position: absolute;
    inset: 0;
    background: var(--surface-highlight);
    pointer-events: none;
  }

  .detail-group {
    margin-top: var(--space-2);
  }

  .detail-group-title {
    font-size: var(--text-xs);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-dim);
    margin-bottom: 4px;
  }

  .props-table {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .prop-row {
    display: flex;
    gap: var(--space-3);
    font-size: var(--text-xs);
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
    gap: var(--space-2);
    font-size: var(--text-xs);
    padding: 3px 0;
  }

  .rel-arrow {
    color: var(--fg-dim);
    font-size: var(--text-xs);
  }

  .entity-actions {
    margin-top: var(--space-2);
    padding-top: var(--space-2);
    border-top: 1px solid var(--border-subtle);
    display: flex;
    justify-content: flex-end;
  }

  /* Delete button in relation rows */
  .delete-btn-sm {
    width: 18px;
    height: 18px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border-radius: var(--radius-sm);
    font-size: var(--text-xs);
    color: var(--error);
    opacity: 0.5;
    transition: opacity var(--transition-fast), background var(--transition-fast), box-shadow var(--transition-fast);
    margin-left: auto;
    flex-shrink: 0;
  }

  .delete-btn-sm:hover {
    opacity: 1;
    background: var(--error-dim);
    box-shadow: 0 0 6px var(--glow-error);
  }

  /* Graph viz */
  .graph-viz {
    height: 300px;
    min-height: 200px;
    border-top: 1px solid var(--border);
    background: var(--bg-secondary);
    overflow: hidden;
  }

  /* Modal form */
  .modal-form {
    display: flex;
    flex-direction: column;
  }

  .modal-form textarea {
    font-family: var(--font-mono);
    font-size: var(--text-sm);
    background: var(--bg-primary);
    color: var(--fg-primary);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    padding: var(--space-2);
    resize: vertical;
    outline: none;
    transition: border-color var(--transition-fast), box-shadow var(--transition-fast);
  }

  .modal-form textarea:focus {
    border-color: var(--info);
    box-shadow: 0 0 4px var(--glow-info);
  }

  .form-row {
    display: flex;
    gap: var(--space-3);
  }

  .form-row .form-field {
    flex: 1;
  }

  /* Path result */
  .path-result {
    border-top: 1px solid var(--border);
    padding-top: 8px;
  }

  .path-steps {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    flex-wrap: wrap;
    padding: var(--space-2) 0;
  }

  .path-step {
    display: flex;
    align-items: center;
    gap: 4px;
    background: var(--bg-tertiary);
    padding: 4px var(--space-2);
    border-radius: var(--radius-sm);
    border: 1px solid var(--border-subtle);
    font-size: var(--text-xs);
    font-family: var(--font-mono);
    transition: border-color var(--transition-fast);
  }

  .path-step:hover {
    border-color: color-mix(in srgb, var(--info) 30%, var(--border));
  }

  .path-arrow {
    color: var(--fg-dim);
    font-size: var(--text-sm);
  }

  /* Entity name link (opens drawer) */
  .entity-name-link {
    cursor: pointer;
  }

  .entity-name-link:hover {
    text-decoration: underline;
    color: var(--accent);
    text-shadow: 0 0 6px var(--glow-accent);
  }

  /* DetailDrawer shared classes in theme.css; stat-chip override for 13px */
  .stat-chip { font-size: var(--text-sm, 13px); }

  /* Drawer properties table */
  .drawer-props-table {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .drawer-prop-row {
    display: flex;
    gap: var(--space-2, 8px);
    padding: 2px 0;
    border-bottom: 1px solid var(--border);
  }

  .drawer-prop-key {
    color: var(--fg-muted);
    min-width: 100px;
  }

  .drawer-prop-value {
    flex: 1;
    word-break: break-word;
  }

  /* Drawer relation rows */
  .drawer-relation-row {
    display: flex;
    align-items: center;
    gap: var(--space-2, 8px);
    padding: 4px 0;
    font-size: var(--text-sm, 13px);
  }

  /* Section shared in theme.css */
</style>
