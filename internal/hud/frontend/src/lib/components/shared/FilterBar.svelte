<script>
  /**
   * FilterBar — reusable search + filter dropdown bar with result count.
   *
   * @type {{
   *   search?: string,
   *   placeholder?: string,
   *   filters?: Array<{ key: string, label: string, options: Array<{ value: string, label: string }>, value?: string }>,
   *   resultCount?: number | null,
   *   onSearch?: (value: string) => void,
   *   onFilter?: (key: string, value: string) => void,
   *   actions?: import('svelte').Snippet,
   * }}
   */
  let {
    search = '',
    placeholder = 'Search\u2026',
    filters = [],
    resultCount = null,
    onSearch,
    onFilter,
    actions,
  } = $props();

  let debounceTimer;

  function handleSearchInput(e) {
    const val = e.target.value;
    clearTimeout(debounceTimer);
    debounceTimer = setTimeout(() => {
      if (onSearch) onSearch(val);
    }, 150);
  }

  function handleFilterChange(key, e) {
    if (onFilter) onFilter(key, e.target.value);
  }
</script>

<div class="filter-bar" role="search" aria-label="Filter">
  <div class="filter-bar-search">
    <span class="filter-bar-search-icon">{'\u{1F50D}'}</span>
    <input
      class="filter-bar-input panel-search-input"
      type="text"
      value={search}
      {placeholder}
      oninput={handleSearchInput}
      aria-label={placeholder}
    />
  </div>

  {#each filters as filter}
    <select
      class="filter-bar-select"
      value={filter.value || ''}
      onchange={(e) => handleFilterChange(filter.key, e)}
      aria-label={filter.label}
    >
      <option value="">{filter.label}</option>
      {#each filter.options as opt}
        <option value={opt.value}>{opt.label}</option>
      {/each}
    </select>
  {/each}

  <div class="filter-bar-spacer"></div>

  <div class="filter-bar-meta">
    {#if resultCount != null}
      <span class="filter-bar-count">{resultCount} result{resultCount !== 1 ? 's' : ''}</span>
    {/if}

    {#if actions}
      <div class="filter-bar-actions">
        {@render actions()}
      </div>
    {/if}
  </div>
</div>

<style>
  .filter-bar {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    padding: var(--space-2) 0;
    border-bottom: 1px solid var(--border);
    flex-shrink: 0;
    flex-wrap: wrap;
  }

  .filter-bar-search {
    display: flex;
    align-items: center;
    gap: var(--space-1);
    flex: 1 1 260px;
    min-width: 180px;
  }

  .filter-bar-search-icon {
    font-size: var(--text-sm);
    opacity: 0.5;
    flex-shrink: 0;
  }

  .filter-bar-input {
    flex: 1;
    font-size: var(--text-sm);
    padding: var(--space-1) var(--space-2);
    background: var(--bg-primary);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    color: var(--fg-primary);
    font-family: var(--font-mono);
  }

  .filter-bar-input:focus {
    border-color: var(--border-focus);
    outline: none;
  }

  .filter-bar-select {
    flex: 0 1 180px;
    min-width: 140px;
    font-size: var(--text-sm);
    padding: var(--space-1) var(--space-2);
    background: var(--bg-primary);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    color: var(--fg-secondary);
    font-family: var(--font-mono);
    cursor: pointer;
  }

  .filter-bar-select:focus {
    border-color: var(--border-focus);
    outline: none;
  }

  .filter-bar-spacer {
    flex: 1 1 auto;
  }

  .filter-bar-meta {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    margin-left: auto;
    flex-wrap: wrap;
    justify-content: flex-end;
  }

  .filter-bar-count {
    font-size: var(--text-xs);
    color: var(--fg-muted);
    font-family: var(--font-mono);
    white-space: nowrap;
  }

  .filter-bar-actions {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }

  @media (max-width: 900px) {
    .filter-bar-meta {
      width: 100%;
      margin-left: 0;
      justify-content: space-between;
    }
  }

  @media (max-width: 640px) {
    .filter-bar-select {
      flex: 1 1 160px;
    }
  }
</style>
