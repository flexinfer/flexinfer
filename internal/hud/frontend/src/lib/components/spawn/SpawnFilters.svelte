<script lang="ts">
  /**
   * SpawnFilters — status chips + search input. Reads/writes spawnStore
   * filter state directly per the panel decomp contract.
   */
  import { spawnStore } from '../../stores/spawn.svelte';

  let spawns = $derived(spawnStore.spawns);
  let activeCount = $derived(spawnStore.activeSpawns.length);
  let completedCount = $derived(spawnStore.completedSpawns.length);
  let failedCount = $derived(spawnStore.spawns.filter((spawn) => spawn.status === 'failed').length);
  let statusFilter = $derived(spawnStore.statusFilter);
  let searchQuery = $derived(spawnStore.searchQuery);
</script>

<div class="filter-bar">
  <div class="filter-chips">
    <button type="button" class="filter-chip" class:active={statusFilter === 'all'} onclick={() => spawnStore.setStatusFilter('all')}>All ({spawns.length})</button>
    <button type="button" class="filter-chip" class:active={statusFilter === 'active'} onclick={() => spawnStore.setStatusFilter('active')}>Active ({activeCount})</button>
    <button type="button" class="filter-chip" class:active={statusFilter === 'completed'} onclick={() => spawnStore.setStatusFilter('completed')}>Completed ({completedCount})</button>
    <button type="button" class="filter-chip" class:active={statusFilter === 'failed'} onclick={() => spawnStore.setStatusFilter('failed')}>Failed ({failedCount})</button>
  </div>
  <input
    type="text"
    class="filter-search"
    placeholder="Search project, task, agent..."
    value={searchQuery}
    oninput={(e) => spawnStore.setSearchQuery(e.currentTarget.value)}
  />
</div>

<style>
  .filter-bar {
    display: flex;
    align-items: center;
    gap: var(--space-3);
    flex-wrap: wrap;
  }

  .filter-chips {
    display: flex;
    gap: var(--space-1);
  }

  .filter-chip {
    padding: 5px 10px;
    border-radius: var(--radius-full);
    border: 1px solid var(--border);
    background: transparent;
    color: var(--fg-secondary);
    font-size: var(--text-xs);
    font-family: var(--font-mono);
    cursor: pointer;
    transition: border-color var(--transition-fast), color var(--transition-fast), background var(--transition-fast);
  }

  .filter-chip:hover {
    border-color: var(--border-active);
    color: var(--fg-primary);
  }

  .filter-chip.active {
    border-color: var(--accent);
    color: var(--accent);
    background: rgba(255, 107, 53, 0.08);
  }

  .filter-search {
    flex: 1;
    min-width: 180px;
    padding: 6px 12px;
    border-radius: var(--radius-md);
    border: 1px solid var(--border);
    background: var(--bg-primary);
    color: var(--fg-primary);
    font-size: var(--text-sm);
    font-family: var(--font-mono);
    transition: border-color var(--transition-fast);
  }

  .filter-search:focus {
    outline: none;
    border-color: var(--border-focus);
  }

  .filter-search::placeholder {
    color: var(--fg-dim);
  }
</style>
