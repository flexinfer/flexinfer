<script>
  /**
   * DataTable — sortable, selectable table with sticky header,
   * skeleton loading, expandable rows, and optional row pagination.
   *
   * @type {{
   *   columns: Array<{ key: string, label: string, sortable?: boolean, width?: string, align?: 'left'|'center'|'right' }>,
   *   rows: any[],
   *   sortKey?: string,
   *   sortDir?: 'asc' | 'desc',
   *   loading?: boolean,
   *   skeletonRows?: number,
   *   selectable?: boolean,
   *   selectedIds?: Set<string>,
   *   expandedIds?: Set<string>,
   *   idKey?: string,
   *   maxRows?: number,
   *   rowLabel?: string,
   *   stableLayout?: boolean,
   *   onSort?: (key: string, dir: 'asc' | 'desc') => void,
   *   onSelect?: (ids: Set<string>) => void,
   *   onRowClick?: (row: any) => void,
   *   onToggleExpand?: (row: any) => void,
   *   row: import('svelte').Snippet<[{ row: any, index: number, expanded: boolean }]>,
   *   expandedRow?: import('svelte').Snippet<[{ row: any, index: number }]>,
   * }}
   */
  let {
    columns = [],
    rows = [],
    sortKey = '',
    sortDir = 'asc',
    loading = false,
    skeletonRows = 5,
    selectable = false,
    selectedIds = new Set(),
    expandedIds = new Set(),
    idKey = 'id',
    maxRows = undefined,
    rowLabel = 'row',
    stableLayout = false,
    onSort,
    onSelect,
    onRowClick,
    onToggleExpand,
    row: rowSnippet,
    expandedRow: expandedRowSnippet,
  } = $props();

  let colSpan = $derived((selectable ? 1 : 0) + columns.length);

  // Restore persisted sort state on mount (keyed by first column label).
  $effect(() => {
    if (!onSort || !columns.length) return;
    const storageKey = `dt-sort-${columns[0]?.label ?? 'default'}`;
    try {
      const saved = sessionStorage.getItem(storageKey);
      if (saved) {
        const { key, dir } = JSON.parse(saved);
        if (key && dir && key !== sortKey) onSort(key, dir);
      }
    } catch { /* ignore */ }
  });

  // Row pagination: show maxRows initially, expand on demand
  let displayCount = $state(Infinity);

  // Reset display count when rows change
  $effect(() => {
    rows;
    displayCount = maxRows ?? Infinity;
  });

  let displayRows = $derived(maxRows ? rows.slice(0, displayCount) : rows);
  let hasMore = $derived(maxRows ? rows.length > displayCount : false);
  let remainingCount = $derived(rows.length - displayCount);
  let showFooter = $derived(!loading && rows.length > 0 && maxRows !== undefined);
  let summaryText = $derived.by(() => {
    const visible = displayRows.length;
    const total = rows.length;
    const label = total === 1 ? rowLabel : `${rowLabel}s`;
    if (visible === total) {
      return `Showing ${visible} ${label}`;
    }
    return `Showing ${visible} of ${total} ${label}`;
  });

  function showMore() {
    displayCount = Math.min(displayCount + (maxRows ?? 50), rows.length);
  }

  function handleHeaderClick(col) {
    if (!col.sortable || !onSort) return;
    const newDir = sortKey === col.key && sortDir === 'asc' ? 'desc' : 'asc';
    // Persist sort preference.
    try {
      const storageKey = `dt-sort-${columns[0]?.label ?? 'default'}`;
      sessionStorage.setItem(storageKey, JSON.stringify({ key: col.key, dir: newDir }));
    } catch { /* ignore */ }
    onSort(col.key, newDir);
  }

  function handleSelectAll() {
    if (!onSelect) return;
    if (selectedIds.size === rows.length) {
      onSelect(new Set());
    } else {
      onSelect(new Set(rows.map(r => r[idKey])));
    }
  }

  function handleSelectRow(row) {
    if (!onSelect) return;
    const next = new Set(selectedIds);
    const id = row[idKey];
    if (next.has(id)) {
      next.delete(id);
    } else {
      next.add(id);
    }
    onSelect(next);
  }

  let allSelected = $derived(rows.length > 0 && selectedIds.size === rows.length);
  let someSelected = $derived(selectedIds.size > 0 && selectedIds.size < rows.length);
</script>

<div class="data-table-wrap">
  <table class="data-table" class:stable-layout={stableLayout} role="grid">
    <thead>
      <tr>
        {#if selectable}
          <th class="data-table-check" scope="col">
            <input
              type="checkbox"
              checked={allSelected}
              indeterminate={someSelected}
              onchange={handleSelectAll}
              aria-label="Select all rows"
            />
          </th>
        {/if}
        {#each columns as col}
          <th
            scope="col"
            class:sortable={col.sortable}
            class:sorted={sortKey === col.key}
            style:width={col.width || 'auto'}
            style:text-align={col.align || 'left'}
            aria-sort={sortKey === col.key ? (sortDir === 'asc' ? 'ascending' : 'descending') : undefined}
            onclick={() => handleHeaderClick(col)}
            onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); handleHeaderClick(col); } }}
            tabindex={col.sortable ? 0 : undefined}
            role={col.sortable ? 'button' : undefined}
          >
            <span class="data-table-header-label">{col.label}</span>
            {#if col.sortable && sortKey === col.key}
              <span class="data-table-sort-icon">{sortDir === 'asc' ? '\u25B2' : '\u25BC'}</span>
            {/if}
          </th>
        {/each}
      </tr>
    </thead>
    <tbody>
      {#if loading}
        {#each Array(skeletonRows) as _, i}
          <tr class="data-table-skeleton-row">
            {#if selectable}
              <td><div class="skeleton skeleton-text" style="width: 16px;"></div></td>
            {/if}
            {#each columns as col}
              <td><div class="skeleton skeleton-text" style="width: {60 + (i * 7) % 40}%;"></div></td>
            {/each}
          </tr>
        {/each}
      {:else}
        {#each displayRows as rowData, index (rowData[idKey] ?? index)}
          {@const isExpanded = expandedIds.has(rowData[idKey])}
          <tr
            class:selected={selectable && selectedIds.has(rowData[idKey])}
            class:clickable={!!onRowClick || !!onToggleExpand}
            class:expanded-row={isExpanded}
            onclick={() => { if (onRowClick) onRowClick(rowData); else if (onToggleExpand) onToggleExpand(rowData); }}
            onkeydown={(e) => { if ((onRowClick || onToggleExpand) && (e.key === 'Enter' || e.key === ' ')) { e.preventDefault(); if (onRowClick) onRowClick(rowData); else if (onToggleExpand) onToggleExpand(rowData); } }}
            tabindex={(onRowClick || onToggleExpand) ? 0 : undefined}
            role={(onRowClick || onToggleExpand) ? 'row' : undefined}
          >
            {#if selectable}
              <td class="data-table-check">
                <input
                  type="checkbox"
                  checked={selectedIds.has(rowData[idKey])}
                  onchange={() => handleSelectRow(rowData)}
                  onclick={(e) => e.stopPropagation()}
                  aria-label="Select row"
                />
              </td>
            {/if}
            {@render rowSnippet({ row: rowData, index, expanded: isExpanded })}
          </tr>
          {#if isExpanded && expandedRowSnippet}
            <tr class="data-table-expand-row">
              <td colspan={colSpan}>
                {@render expandedRowSnippet({ row: rowData, index })}
              </td>
            </tr>
          {/if}
        {/each}
      {/if}
    </tbody>
  </table>
  {#if showFooter}
    <div class="data-table-footer">
      <span class="data-table-summary">{summaryText}</span>
      {#if hasMore}
        <button class="btn btn-ghost btn-sm" onclick={showMore}>
          Show {Math.min(maxRows ?? 50, remainingCount)} more
        </button>
      {/if}
    </div>
  {/if}
</div>

<style>
  .data-table-wrap {
    display: flex;
    flex-direction: column;
    overflow-x: auto;
    flex: 1;
    min-height: 0;
  }

  .data-table {
    width: 100%;
    border-collapse: collapse;
    font-size: var(--text-sm);
  }

  .data-table.stable-layout {
    table-layout: fixed;
  }

  .data-table thead th {
    text-align: left;
    font-size: var(--text-xs);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-muted);
    padding: var(--space-1) var(--space-2);
    border-bottom: 1px solid var(--border);
    white-space: nowrap;
    position: sticky;
    top: 0;
    background: var(--bg-secondary);
    z-index: 1;
    user-select: none;
  }

  .data-table thead th.sortable {
    cursor: pointer;
  }

  .data-table thead th.sortable:hover {
    color: var(--fg-primary);
  }

  .data-table thead th.sorted {
    color: var(--fg-primary);
  }

  .data-table-header-label {
    display: inline;
  }

  .data-table-sort-icon {
    font-size: 8px;
    margin-left: var(--space-1);
    opacity: 0.7;
  }

  .data-table tbody td {
    padding: var(--space-1) var(--space-2);
    border-bottom: 1px solid var(--border);
    color: var(--fg-secondary);
    vertical-align: middle;
  }

  .data-table.stable-layout thead th,
  .data-table.stable-layout tbody td {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .data-table tbody tr:hover td {
    background: var(--bg-tertiary);
    color: var(--fg-primary);
  }

  .data-table tbody tr:last-child td {
    border-bottom: none;
  }

  .data-table tbody tr.selected td {
    background: rgba(1, 135, 153, 0.08);
  }

  .data-table tbody tr.clickable {
    cursor: pointer;
  }

  .data-table tbody tr.clickable:focus-visible {
    outline: 2px solid var(--border-focus);
    outline-offset: -2px;
  }

  .data-table-check {
    width: 32px;
    text-align: center;
  }

  .data-table-check input[type="checkbox"] {
    accent-color: var(--info);
    cursor: pointer;
  }

  .data-table-skeleton-row td {
    padding: var(--space-2) var(--space-2);
  }

  /* Expandable row support */
  .data-table tbody tr.expanded-row td {
    border-bottom: none;
  }

  .data-table tbody tr.data-table-expand-row td {
    padding: 0 var(--space-2) var(--space-2);
    border-bottom: 1px solid var(--border);
  }

  .data-table tbody tr.data-table-expand-row:hover td {
    background: transparent;
    color: inherit;
  }

  .data-table-footer {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-2);
    padding: var(--space-2) 0 0;
    border-top: 1px solid color-mix(in srgb, var(--border) 70%, transparent);
    margin-top: var(--space-2);
  }

  .data-table-summary {
    font-size: var(--text-xs);
    color: var(--fg-muted);
    font-family: var(--font-mono);
  }

  @media (max-width: 640px) {
    .data-table-footer {
      flex-direction: column;
      align-items: stretch;
    }
  }
</style>
