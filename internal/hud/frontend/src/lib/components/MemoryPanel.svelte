<script>
  import { memoryStore } from '../stores/memory.svelte.ts';
  import Gauge from '../widgets/Gauge.svelte';
  import Badge from '../widgets/Badge.svelte';

  $effect(() => {
    memoryStore.startPolling(8000);
    return () => { memoryStore.stopPolling(); };
  });

  let stats = $derived(memoryStore.stats ?? {});
  let items = $derived(memoryStore.items ?? []);

  let activeTier = $state('working');
  let searchQuery = $state('');

  // Tier data accessors
  let workingTier = $derived(stats.tiers?.working ?? {});
  let shortTier = $derived(stats.tiers?.short_term ?? {});
  let longTier = $derived(stats.tiers?.long_term ?? {});

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

    return result;
  });

  function switchTier(tier) {
    activeTier = tier;
    memoryStore.recall(tier, searchQuery || '', 100);
  }

  function handleSearch() {
    memoryStore.recall(activeTier, searchQuery, 100);
  }

  function promoteItem(id) {
    memoryStore.promote(id);
  }

  function demoteItem(id) {
    memoryStore.demote(id);
  }

  function importanceColor(importance) {
    const map = {
      critical: 'var(--error)',
      high: 'var(--warning)',
      medium: 'var(--fg-primary)',
      low: 'var(--fg-muted)',
    };
    return map[importance] ?? 'var(--fg-secondary)';
  }

  function statusVariant(status) {
    const map = {
      active: 'success',
      compressed: 'accent',
      expired: 'error',
      pending: 'warning',
    };
    return map[status] ?? 'info';
  }

  function formatNumber(n) {
    if (n == null) return '0';
    if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M';
    if (n >= 1_000) return (n / 1_000).toFixed(1) + 'K';
    return String(n);
  }

  function relativeTime(ts) {
    if (!ts) return '---';
    const now = Date.now();
    const then = new Date(ts).getTime();
    const diff = now - then;
    const secs = Math.floor(diff / 1000);
    if (secs < 60) return secs + 's ago';
    const mins = Math.floor(secs / 60);
    if (mins < 60) return mins + 'm ago';
    const hours = Math.floor(mins / 60);
    if (hours < 24) return hours + 'h ago';
    const days = Math.floor(hours / 24);
    return days + 'd ago';
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
        {formatNumber(workingTier.tokens ?? 0)} tokens
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
        {formatNumber(shortTier.tokens ?? 0)} tokens
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
        {formatNumber(longTier.tokens ?? 0)} tokens
      </div>
      <div class="tier-policy text-xs text-muted">
        TTL: {longTier.ttl ?? '---'}
        {#if longTier.compression_ratio}
          | Compress: {(longTier.compression_ratio * 100).toFixed(0)}%
        {/if}
      </div>
    </div>
  </div>

  <!-- Middle section: Compression Stats -->
  <div class="compression-section">
    <div class="section-header">
      <span class="section-title">Compression</span>
    </div>
    <div class="compression-cards">
      <div class="comp-card">
        <div class="metric-value">{((compression.overall_ratio ?? 0) * 100).toFixed(1)}%</div>
        <div class="metric-label">Overall Ratio</div>
      </div>
      <div class="comp-card">
        <div class="metric-value">{formatNumber(compression.compressed_items ?? 0)}</div>
        <div class="metric-label">Compressed</div>
      </div>
      <div class="comp-card">
        <div class="metric-value">{formatNumber(compression.tokens_saved ?? 0)}</div>
        <div class="metric-label">Tokens Saved</div>
      </div>
      <div class="comp-card">
        <div class="metric-value">{formatNumber(compression.added_24h ?? 0)}</div>
        <div class="metric-label">Added (24h)</div>
      </div>
      <div class="comp-card">
        <div class="metric-value">{formatNumber(compression.compressed_24h ?? 0)}</div>
        <div class="metric-label">Compressed (24h)</div>
      </div>
      <div class="comp-card">
        <div class="metric-value">{formatNumber(compression.expired_24h ?? 0)}</div>
        <div class="metric-label">Expired (24h)</div>
      </div>
    </div>
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
      <input
        type="text"
        placeholder="Search memories..."
        bind:value={searchQuery}
        onkeydown={(e) => e.key === 'Enter' && handleSearch()}
        class="search-input"
      />
      <span class="text-muted text-xs text-mono">{filteredItems.length} items</span>
    </div>

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
              <tr>
                <td class="item-title">{item.title ?? '---'}</td>
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
                </td>
              </tr>
            {:else}
              <tr>
                <td colspan="7" class="empty-cell">No items in this tier</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </div>
  </div>
</div>

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

  /* Compression */
  .compression-section {
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
    padding: 8px 12px;
    border-bottom: 1px solid var(--border);
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
    border-radius: 4px;
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

  .search-input {
    flex: 1;
    min-width: 150px;
  }

  .browser-table {
    flex: 1;
    overflow-y: auto;
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
    border-radius: 4px;
    font-size: 14px;
    font-weight: 700;
    transition: background 0.15s;
  }

  .promote-btn {
    color: var(--success);
  }

  .promote-btn:hover {
    background: rgba(63, 185, 80, 0.15);
  }

  .demote-btn {
    color: var(--warning);
  }

  .demote-btn:hover {
    background: rgba(210, 153, 34, 0.15);
  }

  .empty-cell {
    text-align: center;
    color: var(--fg-muted);
    padding: 24px 10px !important;
  }
</style>
