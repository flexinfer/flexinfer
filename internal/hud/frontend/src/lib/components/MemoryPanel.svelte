<script>
  import { memoryStore } from '../stores/memory.svelte.ts';
  import { toastStore } from '../stores/toasts.svelte.ts';
  import { formatNumber, relativeTime, statusVariant } from '../utils/format.ts';
  import Gauge from '../widgets/Gauge.svelte';
  import Badge from '../widgets/Badge.svelte';
  import Modal from '../widgets/Modal.svelte';
  import ConfirmDialog from '../widgets/ConfirmDialog.svelte';
  import FilterBar from './shared/FilterBar.svelte';
  import EmptyState from './shared/EmptyState.svelte';
  import DataTable from './shared/DataTable.svelte';
  import BulkToolbar from './shared/BulkToolbar.svelte';
  import DetailDrawer from './shared/DetailDrawer.svelte';

  $effect(() => {
    memoryStore.startPolling(8000);
    return () => { memoryStore.stopPolling(); };
  });

  let stats = $derived(memoryStore.stats ?? {});
  let items = $derived(memoryStore.items ?? []);

  let activeTier = $state('working');
  let searchQuery = $state('');
  let sortKey = $state('');
  let sortDir = $state('asc');

  const memColumns = [
    { key: 'title', label: 'Title', sortable: true },
    { key: 'importance', label: 'Importance', sortable: true, width: '100px' },
    { key: 'tokens', label: 'Tokens', sortable: true, width: '80px' },
    { key: 'status', label: 'Status', width: '100px' },
    { key: 'category', label: 'Category', sortable: true, width: '120px' },
    { key: 'last_accessed', label: 'Accessed', sortable: true, width: '120px' },
    { key: 'actions', label: '', width: '90px' },
  ];

  // Expanded items (click to see full content)
  let expandedItems = $state(new Set());

  // Add memory modal
  let showAddModal = $state(false);
  let newTitle = $state('');
  let newContent = $state('');
  let newTier = $state('working');
  let newImportance = $state('medium');
  let newCategory = $state('');
  let adding = $state(false);

  // Delete confirm dialog
  let showDeleteConfirm = $state(false);
  let deleteTarget = $state(null);

  // Compaction status
  let compaction = $state(null);

  // Tier data accessors
  let workingTier = $derived(stats.working_memory ?? {});
  let shortTier = $derived(stats.short_term_memory ?? {});
  let longTier = $derived(stats.long_term_memory ?? {});

  // Compression stats
  let compression = $derived(stats.compression ?? {});

  // Filtered items for browser
  let filteredItems = $derived.by(() => {
    let result = items.filter(item => item.tier === activeTier);

    if (searchQuery.trim()) {
      const q = searchQuery.toLowerCase().trim();
      result = result.filter(item =>
        (item.title ?? '').toLowerCase().includes(q) ||
        (item.content ?? '').toLowerCase().includes(q) ||
        (item.category ?? '').toLowerCase().includes(q)
      );
    }

    // Default sort by importance (highest first).
    result.sort((a, b) => importanceScore(b.importance) - importanceScore(a.importance));

    return result;
  });

  let sortedItems = $derived.by(() => {
    if (!sortKey) return filteredItems;
    const result = [...filteredItems];
    result.sort((a, b) => {
      let va = a[sortKey] ?? '';
      let vb = b[sortKey] ?? '';
      if (sortKey === 'tokens') { va = a.tokens ?? 0; vb = b.tokens ?? 0; }
      if (va < vb) return sortDir === 'asc' ? -1 : 1;
      if (va > vb) return sortDir === 'asc' ? 1 : -1;
      return 0;
    });
    return result;
  });

  function switchTier(tier) {
    activeTier = tier;
    expandedItems = new Set();
    memoryStore.recall(tier, searchQuery || '', 100);
  }

  function handleSearch() {
    memoryStore.recall(activeTier, searchQuery, 100);
  }

  function clearFilters() {
    searchQuery = '';
    memoryStore.recall(activeTier, '', 100);
  }

  function promoteItem(id) {
    memoryStore.promote(id);
    toastStore.info('Memory promoted');
  }

  function demoteItem(id) {
    memoryStore.demote(id);
    toastStore.info('Memory demoted');
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

  // Add memory
  function resetAddForm() {
    newTitle = '';
    newContent = '';
    newTier = activeTier;
    newImportance = 'medium';
    newCategory = '';
    adding = false;
  }

  function openAddModal() {
    resetAddForm();
    showAddModal = true;
  }

  function closeAddModal() {
    showAddModal = false;
    resetAddForm();
  }

  async function submitAddMemory() {
    if (!newTitle.trim() || !newContent.trim()) return;
    adding = true;
    const ok = await memoryStore.addItem(
      newTitle.trim(),
      newContent.trim(),
      newTier,
      newImportance,
      newCategory.trim() || undefined,
    );
    if (ok) {
      toastStore.success('Memory item added');
      closeAddModal();
    } else {
      toastStore.error(memoryStore.error ?? 'Failed to add memory');
      adding = false;
    }
  }

  // Delete memory
  function confirmDelete(item) {
    deleteTarget = item;
    showDeleteConfirm = true;
  }

  async function executeDelete() {
    if (!deleteTarget) return;
    const ok = await memoryStore.deleteItem(deleteTarget.id);
    if (ok) {
      toastStore.success('Memory item deleted');
    } else {
      toastStore.error(memoryStore.error ?? 'Failed to delete');
    }
    showDeleteConfirm = false;
    deleteTarget = null;
  }

  function cancelDelete() {
    showDeleteConfirm = false;
    deleteTarget = null;
  }

  // Fetch compaction on mount
  $effect(() => {
    memoryStore.fetchCompaction().then(data => {
      if (data) compaction = data;
    });
  });

  function importanceColor(importance) {
    const map = {
      critical: 'var(--error)',
      high: 'var(--warning)',
      medium: 'var(--fg-primary)',
      low: 'var(--fg-muted)',
    };
    return map[importance] ?? 'var(--fg-secondary)';
  }

  function importanceBorderColor(importance) {
    if (typeof importance === 'number') {
      if (importance >= 0.7) return 'var(--accent)';
      if (importance >= 0.4) return 'var(--info)';
      return 'var(--fg-muted)';
    }
    const map = {
      critical: 'var(--accent)',
      high: 'var(--accent)',
      medium: 'var(--info)',
      low: 'var(--fg-muted)',
    };
    return map[importance] ?? 'var(--fg-muted)';
  }

  function importanceScore(importance) {
    if (typeof importance === 'number') return importance;
    const map = { critical: 1.0, high: 0.8, medium: 0.5, low: 0.2 };
    return map[importance] ?? 0.3;
  }

  // Bulk selection
  let selectedMemIds = $state(new Set());

  function handleMemSelect(ids) {
    selectedMemIds = ids;
  }

  // Clear selection on tier switch
  $effect(() => {
    activeTier;
    selectedMemIds = new Set();
  });

  async function bulkPromote() {
    for (const id of selectedMemIds) {
      await memoryStore.promote(id);
    }
    toastStore.success(`${selectedMemIds.size} items promoted`);
    selectedMemIds = new Set();
  }

  async function bulkDemote() {
    for (const id of selectedMemIds) {
      await memoryStore.demote(id);
    }
    toastStore.success(`${selectedMemIds.size} items demoted`);
    selectedMemIds = new Set();
  }

  async function bulkDelete() {
    for (const id of selectedMemIds) {
      await memoryStore.deleteItem(id);
    }
    toastStore.success(`${selectedMemIds.size} items deleted`);
    selectedMemIds = new Set();
  }

  let memBulkActions = $derived(() => {
    const actions = [];
    if (activeTier !== 'long_term') {
      actions.push({ label: 'Promote', variant: 'success', onclick: bulkPromote });
    }
    if (activeTier !== 'working') {
      actions.push({ label: 'Demote', variant: 'warning', onclick: bulkDemote });
    }
    actions.push({ label: 'Delete', variant: 'danger', onclick: bulkDelete });
    return actions;
  });

  // Detail drawer
  let drawerItem = $state(null);

  function openItemDrawer(item) {
    drawerItem = item;
  }

  function closeItemDrawer() {
    drawerItem = null;
  }

</script>

<div class="panel memory-panel">
  <!-- Top section: Tier Overview -->
  <div class="tier-overview">
    <div class="tier-col" style="--tier-color: var(--tier-working)">
      <div class="tier-name">Working Memory</div>
      <Gauge
        value={workingTier.items ?? 0}
        max={workingTier.max_items ?? 100}
        label="Items"
        color="var(--tier-working)"
        showPercentage={true}
      />
      <div class="tier-tokens text-mono text-xs">
        {#key workingTier.tokens}<span class="data-updated">{formatNumber(workingTier.tokens ?? 0)} tokens</span>{/key}
      </div>
      <div class="tier-policy text-xs text-muted">
        TTL: {workingTier.ttl ?? '---'}
        {#if workingTier.compression_ratio}
          | Compress: {(workingTier.compression_ratio * 100).toFixed(0)}%
        {/if}
      </div>
    </div>

    <div class="tier-col" style="--tier-color: var(--tier-short)">
      <div class="tier-name">Short-Term Memory</div>
      <Gauge
        value={shortTier.items ?? 0}
        max={shortTier.max_items ?? 500}
        label="Items"
        color="var(--tier-short)"
        showPercentage={true}
      />
      <div class="tier-tokens text-mono text-xs">
        {#key shortTier.tokens}<span class="data-updated">{formatNumber(shortTier.tokens ?? 0)} tokens</span>{/key}
      </div>
      <div class="tier-policy text-xs text-muted">
        TTL: {shortTier.ttl ?? '---'}
        {#if shortTier.compression_ratio}
          | Compress: {(shortTier.compression_ratio * 100).toFixed(0)}%
        {/if}
      </div>
    </div>

    <div class="tier-col" style="--tier-color: var(--tier-long)">
      <div class="tier-name">Long-Term Memory</div>
      <Gauge
        value={longTier.items ?? 0}
        max={longTier.max_items ?? 2000}
        label="Items"
        color="var(--tier-long)"
        showPercentage={true}
      />
      <div class="tier-tokens text-mono text-xs">
        {#key longTier.tokens}<span class="data-updated">{formatNumber(longTier.tokens ?? 0)} tokens</span>{/key}
      </div>
      <div class="tier-policy text-xs text-muted">
        TTL: {longTier.ttl ?? '---'}
        {#if longTier.compression_ratio}
          | Compress: {(longTier.compression_ratio * 100).toFixed(0)}%
        {/if}
      </div>
    </div>
  </div>

  <!-- Middle section: Compression + Compaction Stats -->
  <div class="stats-row">
    <div class="compression-section">
      <div class="section-header">
        <span class="section-title">Compression</span>
      </div>
      <div class="compression-cards">
        <div class="comp-card">
          {#key compression.overall_ratio}<div class="metric-value data-updated">{((compression.overall_ratio ?? 0) * 100).toFixed(1)}%</div>{/key}
          <div class="metric-label">Overall Ratio</div>
        </div>
        <div class="comp-card">
          {#key compression.compressed_items}<div class="metric-value data-updated">{formatNumber(compression.compressed_items ?? 0)}</div>{/key}
          <div class="metric-label">Compressed</div>
        </div>
        <div class="comp-card">
          {#key compression.tokens_saved}<div class="metric-value data-updated">{formatNumber(compression.tokens_saved ?? 0)}</div>{/key}
          <div class="metric-label">Tokens Saved</div>
        </div>
        <div class="comp-card">
          {#key compression.added_24h}<div class="metric-value data-updated">{formatNumber(compression.added_24h ?? 0)}</div>{/key}
          <div class="metric-label">Added (24h)</div>
        </div>
        <div class="comp-card">
          {#key compression.compressed_24h}<div class="metric-value data-updated">{formatNumber(compression.compressed_24h ?? 0)}</div>{/key}
          <div class="metric-label">Compressed (24h)</div>
        </div>
        <div class="comp-card">
          {#key compression.expired_24h}<div class="metric-value data-updated">{formatNumber(compression.expired_24h ?? 0)}</div>{/key}
          <div class="metric-label">Expired (24h)</div>
        </div>
      </div>
    </div>

    {#if compaction}
      <div class="compaction-section">
        <div class="section-header">
          <span class="section-title">Compaction</span>
          <Badge text={compaction.status ?? 'idle'} variant={compaction.status === 'running' ? 'info' : 'success'} />
        </div>
        <div class="compaction-cards">
          <div class="comp-card">
            <div class="metric-value">{formatNumber(compaction.items_processed ?? 0)}</div>
            <div class="metric-label">Processed</div>
          </div>
          <div class="comp-card">
            <div class="metric-value">{formatNumber(compaction.items_compacted ?? 0)}</div>
            <div class="metric-label">Compacted</div>
          </div>
          <div class="comp-card">
            <div class="metric-value">{compaction.last_run ? relativeTime(compaction.last_run) : '---'}</div>
            <div class="metric-label">Last Run</div>
          </div>
        </div>
      </div>
    {/if}
  </div>

  <!-- Bottom section: Item Browser -->
  <div class="item-browser">
    <div class="browser-toolbar">
      <div class="tier-tabs">
        <button
          class="tier-tab"
          class:active-tab={activeTier === 'working'}
          onclick={() => switchTier('working')}
          style="--tab-color: var(--tier-working)"
        >Working</button>
        <button
          class="tier-tab"
          class:active-tab={activeTier === 'short_term'}
          onclick={() => switchTier('short_term')}
          style="--tab-color: var(--tier-short)"
        >Short-Term</button>
        <button
          class="tier-tab"
          class:active-tab={activeTier === 'long_term'}
          onclick={() => switchTier('long_term')}
          style="--tab-color: var(--tier-long)"
        >Long-Term</button>
      </div>
    </div>
    <FilterBar
      search={searchQuery}
      placeholder="Search memories..."
      resultCount={filteredItems.length}
      onSearch={(val) => { searchQuery = val; handleSearch(); }}
      onClear={clearFilters}
    >
      {#snippet actions()}
        <button class="btn btn-success" onclick={openAddModal}>+ Add Memory</button>
      {/snippet}
    </FilterBar>

    {#if sortedItems.length === 0}
      <EmptyState icon={'\u25A1'} heading="No items in this tier" compact />
    {:else}
      <DataTable
        columns={memColumns}
        rows={sortedItems}
        {sortKey}
        {sortDir}
        expandedIds={expandedItems}
        idKey="id"
        stableLayout={true}
        selectable={true}
        selectedIds={selectedMemIds}
        onSelect={handleMemSelect}
        onSort={(key, dir) => { sortKey = key; sortDir = dir; }}
        onToggleExpand={(item) => toggleExpand(item.id)}
        onRowClick={openItemDrawer}
      >
        {#snippet row({ row: item, expanded })}
          <td style="border-left: 3px solid {importanceBorderColor(item.importance)}">
            <span class="expand-icon">{expanded ? '\u25BC' : '\u25B6'}</span>
            {item.title ?? '---'}
          </td>
          <td>
            <span class="importance-text" style="color: {importanceColor(item.importance)}">
              {item.importance ?? 'medium'}
            </span>
          </td>
          <td class="text-mono">{formatNumber(item.tokens ?? 0)}</td>
          <td>
            <Badge text={item.status ?? 'active'} variant={statusVariant(item.status)} />
          </td>
          <td class="text-mono text-muted">{item.category ?? '---'}</td>
          <td class="text-mono text-muted">{relativeTime(item.last_accessed)}</td>
          <td class="actions-cell">
            {#if activeTier !== 'long_term'}
              <button
                class="action-btn promote-btn"
                onclick={(e) => { e.stopPropagation(); promoteItem(item.id); }}
                title="Promote"
              >&#8593;</button>
            {/if}
            {#if activeTier !== 'working'}
              <button
                class="action-btn demote-btn"
                onclick={(e) => { e.stopPropagation(); demoteItem(item.id); }}
                title="Demote"
              >&#8595;</button>
            {/if}
            <button
              class="action-btn delete-btn"
              onclick={(e) => { e.stopPropagation(); confirmDelete(item); }}
              title="Delete"
            >&#10005;</button>
          </td>
        {/snippet}
        {#snippet expandedRow({ row: item })}
          <div class="expand-content">
            <pre class="content-pre">{item.content ?? '(no content)'}</pre>
          </div>
        {/snippet}
      </DataTable>
      <BulkToolbar
        count={selectedMemIds.size}
        actions={memBulkActions()}
        onClearSelection={() => { selectedMemIds = new Set(); }}
      />
    {/if}
  </div>
</div>

<!-- Add Memory Modal -->
<Modal open={showAddModal} title="Add Memory" onClose={closeAddModal}>
  <form class="add-form" onsubmit={(e) => { e.preventDefault(); submitAddMemory(); }}>
    <div class="form-field">
      <label class="form-label" for="mem-title">Title</label>
      <input id="mem-title" type="text" bind:value={newTitle} placeholder="Memory title..." required />
    </div>
    <div class="form-field">
      <label class="form-label" for="mem-content">Content</label>
      <textarea id="mem-content" bind:value={newContent} placeholder="Memory content..." rows="4" required></textarea>
    </div>
    <div class="form-row">
      <div class="form-field">
        <label class="form-label" for="mem-tier">Tier</label>
        <select id="mem-tier" bind:value={newTier}>
          <option value="working">Working</option>
          <option value="short_term">Short-Term</option>
          <option value="long_term">Long-Term</option>
        </select>
      </div>
      <div class="form-field">
        <label class="form-label" for="mem-importance">Importance</label>
        <select id="mem-importance" bind:value={newImportance}>
          <option value="low">Low</option>
          <option value="medium">Medium</option>
          <option value="high">High</option>
          <option value="critical">Critical</option>
        </select>
      </div>
    </div>
    <div class="form-field">
      <label class="form-label" for="mem-category">Category (optional)</label>
      <input id="mem-category" type="text" bind:value={newCategory} placeholder="e.g. architecture, decision, note..." />
    </div>
    <div class="form-actions">
      <button type="button" class="btn btn-ghost" onclick={closeAddModal}>Cancel</button>
      <button type="submit" class="btn btn-success" disabled={adding || !newTitle.trim() || !newContent.trim()}>
        {adding ? 'Adding...' : 'Add Memory'}
      </button>
    </div>
  </form>
</Modal>

<!-- Delete Confirm Dialog -->
<ConfirmDialog
  open={showDeleteConfirm}
  title="Delete Memory"
  message={deleteTarget ? `Delete "${deleteTarget.title}"? This cannot be undone.` : ''}
  confirmLabel="Delete"
  destructive={true}
  onConfirm={executeDelete}
  onCancel={cancelDelete}
/>

<!-- Memory Detail Drawer -->
<DetailDrawer
  open={!!drawerItem}
  title={drawerItem?.title ?? '---'}
  subtitle={drawerItem?.category ?? ''}
  onClose={closeItemDrawer}
>
  {#snippet header()}
    {#if drawerItem}
      <div class="detail-stats">
        <div class="stat-chip">
          <span class="importance-text" style="color: {importanceColor(drawerItem.importance)}">
            {drawerItem.importance ?? 'medium'}
          </span>
        </div>
        <div class="stat-chip">
          <span class="stat-chip-value">{formatNumber(drawerItem.tokens ?? 0)}</span>
          <span class="stat-chip-label">tokens</span>
        </div>
        <div class="stat-chip">
          <Badge text={drawerItem.status ?? 'active'} variant={statusVariant(drawerItem.status)} />
        </div>
        {#if drawerItem.last_accessed}
          <div class="stat-chip">
            <span class="stat-chip-value">{relativeTime(drawerItem.last_accessed)}</span>
            <span class="stat-chip-label">accessed</span>
          </div>
        {/if}
      </div>
    {/if}
  {/snippet}

  {#if drawerItem}
    <div class="section">
      <div class="section-title text-xs uppercase text-muted">Content</div>
      <pre class="detail-pre">{drawerItem.content ?? '(no content)'}</pre>
    </div>
  {/if}

  {#snippet footer()}
    {#if drawerItem}
      <div class="drawer-actions">
        {#if activeTier !== 'long_term'}
          <button class="btn btn-success btn-sm" onclick={() => { promoteItem(drawerItem.id); }}>Promote</button>
        {/if}
        {#if activeTier !== 'working'}
          <button class="btn btn-ghost btn-sm" onclick={() => { demoteItem(drawerItem.id); }}>Demote</button>
        {/if}
        <button class="btn btn-danger btn-sm" onclick={() => { closeItemDrawer(); confirmDelete(drawerItem); }}>Delete</button>
      </div>
    {/if}
  {/snippet}
</DetailDrawer>

<style>
  .memory-panel {
    display: flex;
    flex-direction: column;
    overflow-y: auto;
    gap: var(--space-4);
  }

  /* Tier Overview */
  .tier-overview {
    display: grid;
    grid-template-columns: 1fr 1fr 1fr;
    gap: var(--space-4);
  }

  .tier-col {
    position: relative;
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    border-top: 3px solid var(--tier-color);
    padding: var(--space-3) var(--space-4);
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: var(--space-2);
  }

  .tier-col::before {
    content: '';
    position: absolute;
    inset: 0;
    border-radius: inherit;
    background: var(--surface-highlight);
    pointer-events: none;
  }

  .tier-name {
    font-size: var(--text-sm);
    font-weight: 600;
    color: var(--fg-primary);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
  }

  .tier-tokens {
    color: var(--fg-secondary);
  }

  .tier-policy {
    text-align: center;
    line-height: 1.3;
  }

  /* Stats row */
  .stats-row {
    display: flex;
    gap: var(--space-4);
  }

  /* Compression */
  .compression-section {
    position: relative;
    flex: 1;
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    padding: var(--space-3) var(--space-4);
  }

  .compression-section::before {
    content: '';
    position: absolute;
    inset: 0;
    border-radius: inherit;
    background: var(--surface-highlight);
    pointer-events: none;
  }

  .compression-cards {
    display: grid;
    grid-template-columns: repeat(6, 1fr);
    gap: var(--space-3);
  }

  /* Compaction */
  .compaction-section {
    position: relative;
    flex: 0 0 auto;
    min-width: 260px;
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    padding: var(--space-3) var(--space-4);
  }

  .compaction-section::before {
    content: '';
    position: absolute;
    inset: 0;
    border-radius: inherit;
    background: var(--surface-highlight);
    pointer-events: none;
  }

  .compaction-cards {
    display: grid;
    grid-template-columns: repeat(3, 1fr);
    gap: var(--space-3);
  }

  .comp-card {
    text-align: center;
    padding: var(--space-2);
  }

  .comp-card .metric-value {
    font-size: var(--text-lg);
    font-weight: 700;
    font-family: var(--font-mono);
    color: var(--fg-primary);
    line-height: var(--leading-tight);
  }

  .comp-card .metric-label {
    font-size: var(--text-2xs);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-dim);
    margin-top: 4px;
  }

  /* Item Browser */
  .item-browser {
    flex: 1;
    display: flex;
    flex-direction: column;
    min-height: 200px;
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    overflow: hidden;
  }

  .browser-toolbar {
    position: relative;
    display: flex;
    align-items: center;
    gap: var(--space-3);
    padding: var(--space-2) var(--space-3) 0;
  }

  .browser-toolbar::after {
    content: '';
    position: absolute;
    bottom: 0;
    left: 10%;
    right: 10%;
    height: 1px;
    background: linear-gradient(90deg, transparent, rgba(0, 200, 255, 0.06) 50%, transparent);
    pointer-events: none;
  }

  .tier-tabs {
    display: flex;
    gap: 2px;
    background: var(--bg-tertiary);
    border-radius: var(--radius-md);
    padding: 2px;
  }

  .tier-tab {
    padding: 4px 10px;
    font-size: var(--text-sm);
    font-weight: 500;
    border-radius: var(--radius-sm);
    color: var(--fg-secondary);
    transition: background var(--transition-fast), color var(--transition-fast);
  }

  .tier-tab:hover {
    color: var(--fg-primary);
  }

  .active-tab {
    background: var(--bg-secondary) !important;
    color: var(--tab-color, var(--fg-primary)) !important;
    box-shadow: 0 0 6px var(--glow-accent);
  }

  .expand-icon {
    font-size: var(--text-2xs);
    flex-shrink: 0;
    width: 10px;
    color: var(--fg-dim);
  }

  .expand-content {
    background: var(--bg-primary);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    padding: 10px 14px;
    margin-left: 16px;
  }

  .content-pre {
    font-family: var(--font-mono);
    font-size: var(--text-sm);
    color: var(--fg-secondary);
    white-space: pre-wrap;
    word-break: break-word;
    line-height: var(--leading-normal);
    margin: 0;
  }

  .importance-text {
    font-family: var(--font-mono);
    font-size: var(--text-sm);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
  }

  .actions-cell {
    display: flex;
    gap: 4px;
  }

  .action-btn {
    width: 24px;
    height: 24px;
    display: flex;
    align-items: center;
    justify-content: center;
    border-radius: var(--radius-sm);
    font-size: var(--text-base);
    font-weight: 700;
    transition: background var(--transition-fast);
  }

  .promote-btn {
    color: var(--success);
  }

  .promote-btn:hover {
    background: var(--success-dim);
  }

  .demote-btn {
    color: var(--warning);
  }

  .demote-btn:hover {
    background: var(--warning-dim);
  }

  .delete-btn {
    color: var(--error);
    font-size: var(--text-sm);
  }

  .delete-btn:hover {
    background: var(--error-dim);
  }

  /* Add memory form */
  .add-form {
    display: flex;
    flex-direction: column;
  }

  .add-form textarea {
    font-family: var(--font-mono);
    font-size: var(--text-sm);
    background: var(--bg-primary);
    color: var(--fg-primary);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    padding: var(--space-2);
    resize: vertical;
    outline: none;
  }

  .add-form textarea:focus {
    border-color: var(--border-focus);
  }

  .form-row {
    display: flex;
    gap: var(--space-3);
  }

  .form-row .form-field {
    flex: 1;
  }

  /* --- Detail Drawer (shared classes in theme.css) --- */

  .drawer-actions {
    display: flex;
    gap: var(--space-2, 8px);
  }
</style>
