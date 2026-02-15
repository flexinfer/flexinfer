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

  $effect(() => {
    memoryStore.startPolling(8000);
    return () => { memoryStore.stopPolling(); };
  });

  let stats = $derived(memoryStore.stats ?? {});
  let items = $derived(memoryStore.items ?? []);

  let activeTier = $state('working');
  let searchQuery = $state('');

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

  function switchTier(tier) {
    activeTier = tier;
    expandedItems = new Set();
    memoryStore.recall(tier, searchQuery || '', 100);
  }

  function handleSearch() {
    memoryStore.recall(activeTier, searchQuery, 100);
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
    >
      {#snippet actions()}
        <button class="btn btn-success" onclick={openAddModal}>+ Add Memory</button>
      {/snippet}
    </FilterBar>

    <div class="browser-table">
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>Title</th>
              <th>Importance</th>
              <th>Tokens</th>
              <th>Status</th>
              <th>Category</th>
              <th>Accessed</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            {#each filteredItems as item (item.id)}
              <tr class="row-enter memory-row" class:expanded-row={expandedItems.has(item.id)} style="border-left: 3px solid {importanceBorderColor(item.importance)}">
                <td class="item-title">
                  <button class="expand-btn" onclick={() => toggleExpand(item.id)} title="Expand">
                    <span class="expand-icon">{expandedItems.has(item.id) ? '\u25BC' : '\u25B6'}</span>
                    {item.title ?? '---'}
                  </button>
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
                      onclick={() => promoteItem(item.id)}
                      title="Promote"
                    >&#8593;</button>
                  {/if}
                  {#if activeTier !== 'working'}
                    <button
                      class="action-btn demote-btn"
                      onclick={() => demoteItem(item.id)}
                      title="Demote"
                    >&#8595;</button>
                  {/if}
                  <button
                    class="action-btn delete-btn"
                    onclick={() => confirmDelete(item)}
                    title="Delete"
                  >&#10005;</button>
                </td>
              </tr>
              {#if expandedItems.has(item.id)}
                <tr class="expand-content-row">
                  <td colspan="7">
                    <div class="expand-content">
                      <pre class="content-pre">{item.content ?? '(no content)'}</pre>
                    </div>
                  </td>
                </tr>
              {/if}
            {:else}
              <tr>
                <td colspan="7" style="padding: 0; border: none;">
                  <EmptyState icon={'\u25A1'} heading="No items in this tier" compact />
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </div>
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

<style>
  .memory-panel {
    display: flex;
    flex-direction: column;
    overflow-y: auto;
    gap: 16px;
  }

  /* Tier Overview */
  .tier-overview {
    display: grid;
    grid-template-columns: 1fr 1fr 1fr;
    gap: 16px;
  }

  .tier-col {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--border-radius);
    border-top: 3px solid var(--tier-color);
    padding: 14px 16px;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 8px;
  }

  .tier-name {
    font-size: 12px;
    font-weight: 600;
    color: var(--fg-primary);
    text-transform: uppercase;
    letter-spacing: 0.5px;
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
    gap: 16px;
  }

  /* Compression */
  .compression-section {
    flex: 1;
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--border-radius);
    padding: 12px 16px;
  }

  .compression-cards {
    display: grid;
    grid-template-columns: repeat(6, 1fr);
    gap: 12px;
  }

  /* Compaction */
  .compaction-section {
    flex: 0 0 auto;
    min-width: 260px;
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--border-radius);
    padding: 12px 16px;
  }

  .compaction-cards {
    display: grid;
    grid-template-columns: repeat(3, 1fr);
    gap: 12px;
  }

  .comp-card {
    text-align: center;
    padding: 8px;
  }

  .comp-card .metric-value {
    font-size: 18px;
    font-weight: 700;
    font-family: var(--font-mono);
    color: var(--fg-primary);
    line-height: 1.1;
  }

  .comp-card .metric-label {
    font-size: 9px;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    color: var(--fg-muted);
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
    border-radius: var(--border-radius);
    overflow: hidden;
  }

  .browser-toolbar {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 8px 12px 0;
  }

  .tier-tabs {
    display: flex;
    gap: 2px;
    background: var(--bg-tertiary);
    border-radius: var(--border-radius);
    padding: 2px;
  }

  .tier-tab {
    padding: 4px 10px;
    font-size: 11px;
    font-weight: 500;
    border-radius: var(--radius-sm);
    color: var(--fg-secondary);
    transition: background 0.15s, color 0.15s;
  }

  .tier-tab:hover {
    color: var(--fg-primary);
  }

  .active-tab {
    background: var(--bg-secondary) !important;
    color: var(--tab-color, var(--fg-primary)) !important;
    box-shadow: 0 1px 2px rgba(0, 0, 0, 0.2);
  }

  .browser-table {
    flex: 1;
    overflow-y: auto;
  }

  /* Expandable items */
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
    max-width: 250px;
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

  .content-pre {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--fg-secondary);
    white-space: pre-wrap;
    word-break: break-word;
    line-height: 1.5;
    margin: 0;
  }

  .item-title {
    color: var(--fg-primary);
    font-weight: 500;
    max-width: 250px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .importance-text {
    font-family: var(--font-mono);
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
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
    font-size: 14px;
    font-weight: 700;
    transition: background 0.15s;
  }

  .promote-btn {
    color: var(--success);
  }

  .promote-btn:hover {
    background: rgba(34, 178, 85, 0.15);
  }

  .demote-btn {
    color: var(--warning);
  }

  .demote-btn:hover {
    background: rgba(231, 179, 18, 0.15);
  }

  .delete-btn {
    color: var(--error);
    font-size: 12px;
  }

  .delete-btn:hover {
    background: rgba(230, 30, 63, 0.15);
  }

  .memory-row {
    transition: border-left-color 0.15s;
  }

  /* Add memory form */
  .add-form {
    display: flex;
    flex-direction: column;
  }

  .add-form textarea {
    font-family: var(--font-mono);
    font-size: 12px;
    background: var(--bg-primary);
    color: var(--fg-primary);
    border: 1px solid var(--border);
    border-radius: var(--border-radius);
    padding: 8px;
    resize: vertical;
    outline: none;
  }

  .add-form textarea:focus {
    border-color: var(--border-focus);
  }

  .form-row {
    display: flex;
    gap: 12px;
  }

  .form-row .form-field {
    flex: 1;
  }
</style>
