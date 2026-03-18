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
   *   onClear?: () => void,
   *   showShortcutHint?: boolean,
   *   shortcutKey?: string,
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
    onClear,
    showShortcutHint = true,
    shortcutKey = '/',
    actions,
  } = $props();

  let debounceTimer;
  let activeFilterCount = $derived.by(() => {
    let count = search.trim() ? 1 : 0;
    for (const filter of filters) {
      const value = filter.value;
      if (typeof value === 'string' ? value.trim() !== '' : Boolean(value)) {
        count += 1;
      }
    }
    return count;
  });
  let canClear = $derived(!!onClear && activeFilterCount > 0);

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
    <div class="filter-bar-search-field">
      <input
        class="filter-bar-input panel-search-input"
        type="text"
        value={search}
        {placeholder}
        oninput={handleSearchInput}
        aria-label={placeholder}
        autocomplete="off"
        spellcheck="false"
        enterkeyhint="search"
        data-panel-search="primary"
      />
      {#if showShortcutHint}
        <kbd class="filter-bar-shortcut">{shortcutKey}</kbd>
      {/if}
    </div>
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
    {#if activeFilterCount > 0}
      <span class="filter-bar-active">{activeFilterCount} active</span>
    {/if}
    {#if resultCount != null}
      <span class="filter-bar-count">{resultCount} result{resultCount !== 1 ? 's' : ''}</span>
    {/if}

    {#if canClear}
      <button class="btn btn-ghost btn-sm filter-bar-clear" onclick={onClear}>
        Clear filters
      </button>
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

  .filter-bar-search-field {
    position: relative;
    flex: 1;
    min-width: 0;
  }

  .filter-bar-search-icon {
    font-size: var(--text-sm);
    opacity: 0.5;
    flex-shrink: 0;
  }

  .filter-bar-input {
    width: 100%;
    font-size: var(--text-sm);
    padding: var(--space-1) calc(var(--space-2) + 2.5rem) var(--space-1) var(--space-2);
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

  .filter-bar-shortcut {
    position: absolute;
    right: var(--space-2);
    top: 50%;
    transform: translateY(-50%);
    font-size: 10px;
    line-height: 1;
    color: var(--fg-muted);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    padding: 3px 5px;
    background: color-mix(in srgb, var(--bg-secondary) 85%, transparent);
    pointer-events: none;
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

  .filter-bar-active {
    font-size: var(--text-xs);
    color: var(--fg-secondary);
    font-family: var(--font-mono);
    padding: 3px 8px;
    border-radius: 999px;
    border: 1px solid color-mix(in srgb, var(--border-focus) 45%, var(--border));
    background: color-mix(in srgb, var(--accent) 10%, transparent);
    white-space: nowrap;
  }

  .filter-bar-actions {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }

  .filter-bar-clear {
    white-space: nowrap;
  }

  @media (max-width: 900px) {
    .filter-bar-meta {
      width: 100%;
      margin-left: 0;
      justify-content: space-between;
    }
  }

  @media (max-width: 640px) {
    .filter-bar-search {
      flex-basis: 100%;
    }

    .filter-bar-select {
      flex: 1 1 160px;
    }

    .filter-bar-shortcut {
      display: none;
    }
  }
</style>
