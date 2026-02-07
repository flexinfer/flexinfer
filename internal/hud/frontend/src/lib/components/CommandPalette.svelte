<script>
  let { open = $bindable(false), onclose = () => {}, onselect = () => {} } = $props();

  let query = $state('');
  let selectedIndex = $state(0);
  let inputEl = $state(null);

  const panels = [
    { id: 'fleet', label: 'Fleet Overview', category: 'Panels', icon: '\u2637' },
    { id: 'servers', label: 'Server Constellation', category: 'Panels', icon: '\u26C5' },
    { id: 'tasks', label: 'Task Command Board', category: 'Panels', icon: '\u2611' },
    { id: 'stream', label: 'Context Stream', category: 'Panels', icon: '\u21C9' },
    { id: 'workflows', label: 'Workflow DAG Monitor', category: 'Panels', icon: '\u2699' },
    { id: 'memory', label: 'Memory Inspector', category: 'Panels', icon: '\u29BF' },
    { id: 'graph', label: 'Knowledge Graph', category: 'Panels', icon: '\u25C9' },
    { id: 'presence', label: 'Agent Presence', category: 'Panels', icon: '\u25C9' },
    { id: 'reasoning', label: 'Reasoning Chains', category: 'Panels', icon: '\u2726' },
  ];

  const actions = [
    { id: 'create-task', label: 'Create Task...', category: 'Actions', icon: '\u2795' },
    { id: 'seed-entity', label: 'Seed Entity...', category: 'Actions', icon: '\u2B21' },
    { id: 'create-handoff', label: 'Create Handoff...', category: 'Actions', icon: '\u21C6' },
    { id: 'approve-workflow', label: 'Approve Workflow Step...', category: 'Actions', icon: '\u2713' },
    { id: 'reject-workflow', label: 'Reject Workflow Step...', category: 'Actions', icon: '\u2717' },
    { id: 'promote-memory', label: 'Promote Memory Item...', category: 'Actions', icon: '\u2191' },
    { id: 'demote-memory', label: 'Demote Memory Item...', category: 'Actions', icon: '\u2193' },
    { id: 'add-memory', label: 'Add Memory Item...', category: 'Actions', icon: '\u29BE' },
    { id: 'pause-stream', label: 'Toggle Stream Pause', category: 'Actions', icon: '\u23F8' },
    { id: 'toggle-scanlines', label: 'Toggle CRT Scanlines', category: 'Actions', icon: '\u2588' },
    { id: 'refresh-all', label: 'Refresh All Data', category: 'Actions', icon: '\u21BB' },
  ];

  const allItems = [...panels, ...actions];

  function fuzzyMatch(str, query) {
    const lower = str.toLowerCase();
    const q = query.toLowerCase();
    let qi = 0;
    for (let i = 0; i < lower.length && qi < q.length; i++) {
      if (lower[i] === q[qi]) qi++;
    }
    return qi === q.length;
  }

  function fuzzyScore(str, query) {
    const lower = str.toLowerCase();
    const q = query.toLowerCase();
    let score = 0;
    let qi = 0;
    let lastMatch = -1;
    for (let i = 0; i < lower.length && qi < q.length; i++) {
      if (lower[i] === q[qi]) {
        score += 10;
        // Bonus for consecutive matches
        if (lastMatch === i - 1) score += 5;
        // Bonus for matching at word boundary
        if (i === 0 || lower[i - 1] === ' ' || lower[i - 1] === '-') score += 3;
        lastMatch = i;
        qi++;
      }
    }
    return qi === q.length ? score : 0;
  }

  let filtered = $derived.by(() => {
    if (!query.trim()) return allItems;
    return allItems
      .map(item => ({ ...item, score: fuzzyScore(item.label, query) }))
      .filter(item => item.score > 0)
      .sort((a, b) => b.score - a.score);
  });

  let groupedResults = $derived.by(() => {
    const groups = {};
    filtered.forEach(item => {
      if (!groups[item.category]) groups[item.category] = [];
      groups[item.category].push(item);
    });
    return Object.entries(groups);
  });

  // Reset on open
  $effect(() => {
    if (open) {
      query = '';
      selectedIndex = 0;
      // Focus input after mount
      requestAnimationFrame(() => {
        inputEl?.focus();
      });
    }
  });

  // Clamp selectedIndex when results change
  $effect(() => {
    const max = filtered.length;
    if (selectedIndex >= max) {
      selectedIndex = Math.max(0, max - 1);
    }
  });

  function handleKeydown(e) {
    if (e.key === 'Escape') {
      e.preventDefault();
      close();
      return;
    }

    if (e.key === 'ArrowDown') {
      e.preventDefault();
      selectedIndex = Math.min(selectedIndex + 1, filtered.length - 1);
      return;
    }

    if (e.key === 'ArrowUp') {
      e.preventDefault();
      selectedIndex = Math.max(selectedIndex - 1, 0);
      return;
    }

    if (e.key === 'Enter') {
      e.preventDefault();
      const item = filtered[selectedIndex];
      if (item) {
        onselect(item);
        close();
      }
      return;
    }
  }

  function close() {
    open = false;
    onclose();
  }

  function handleBackdropClick(e) {
    if (e.target === e.currentTarget) {
      close();
    }
  }

  function selectItem(item) {
    onselect(item);
    close();
  }

  // Global keyboard listener
  function handleGlobalKeydown(e) {
    if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
      e.preventDefault();
      open = !open;
      if (!open) onclose();
    }
  }

  $effect(() => {
    document.addEventListener('keydown', handleGlobalKeydown);
    return () => document.removeEventListener('keydown', handleGlobalKeydown);
  });

  // Track flat index across groups
  function flatIndex(groupIdx, itemIdx) {
    let idx = 0;
    const groups = groupedResults;
    for (let g = 0; g < groupIdx; g++) {
      idx += groups[g][1].length;
    }
    return idx + itemIdx;
  }
</script>

{#if open}
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="palette-backdrop" onclick={handleBackdropClick} onkeydown={handleKeydown}>
    <div class="palette-container">
      <div class="palette-input-wrap">
        <span class="palette-icon">&#9906;</span>
        <input
          bind:this={inputEl}
          type="text"
          class="palette-input"
          placeholder="Type a command or search..."
          bind:value={query}
          onkeydown={handleKeydown}
        />
        <kbd class="palette-kbd">ESC</kbd>
      </div>

      <div class="palette-results">
        {#if filtered.length === 0}
          <div class="palette-empty">
            <span class="text-muted">No results found</span>
          </div>
        {:else}
          {#each groupedResults as [category, items], gi (category)}
            <div class="palette-group">
              <div class="palette-group-label">{category}</div>
              {#each items as item, ii (item.id)}
                <button
                  class="palette-item"
                  class:palette-item-selected={selectedIndex === flatIndex(gi, ii)}
                  onclick={() => selectItem(item)}
                  onmouseenter={() => selectedIndex = flatIndex(gi, ii)}
                >
                  <span class="palette-item-icon">{item.icon}</span>
                  <span class="palette-item-label">{item.label}</span>
                  {#if item.category === 'Panels'}
                    <kbd class="palette-item-hint">{item.id}</kbd>
                  {/if}
                </button>
              {/each}
            </div>
          {/each}
        {/if}
      </div>
    </div>
  </div>
{/if}

<style>
  .palette-backdrop {
    position: fixed;
    inset: 0;
    z-index: 1000;
    display: flex;
    align-items: flex-start;
    justify-content: center;
    padding-top: 15vh;
    background: rgba(0, 0, 0, 0.6);
    backdrop-filter: blur(4px);
    -webkit-backdrop-filter: blur(4px);
  }

  .palette-container {
    width: 560px;
    max-width: 90vw;
    max-height: 60vh;
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: 12px;
    box-shadow:
      0 16px 48px rgba(0, 0, 0, 0.5),
      0 0 0 1px rgba(88, 166, 255, 0.1);
    overflow: hidden;
    display: flex;
    flex-direction: column;
    animation: paletteSlideIn 0.12s ease-out;
  }

  @keyframes paletteSlideIn {
    from {
      opacity: 0;
      transform: translateY(-8px) scale(0.98);
    }
    to {
      opacity: 1;
      transform: translateY(0) scale(1);
    }
  }

  .palette-input-wrap {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 14px 16px;
    border-bottom: 1px solid var(--border);
  }

  .palette-icon {
    font-size: 18px;
    color: var(--fg-muted);
    flex-shrink: 0;
  }

  .palette-input {
    flex: 1;
    font-family: var(--font-sans);
    font-size: 16px;
    background: transparent;
    border: none;
    color: var(--fg-primary);
    outline: none;
    padding: 0;
  }

  .palette-input::placeholder {
    color: var(--fg-muted);
  }

  .palette-kbd {
    font-family: var(--font-mono);
    font-size: 10px;
    padding: 2px 6px;
    background: var(--bg-tertiary);
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--fg-muted);
    flex-shrink: 0;
  }

  .palette-results {
    overflow-y: auto;
    max-height: calc(60vh - 60px);
    padding: 6px 0;
  }

  .palette-empty {
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 24px;
  }

  .palette-group {
    padding: 0;
  }

  .palette-group-label {
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    color: var(--fg-muted);
    padding: 8px 16px 4px;
  }

  .palette-item {
    display: flex;
    align-items: center;
    gap: 10px;
    width: 100%;
    padding: 8px 16px;
    text-align: left;
    font-size: 13px;
    color: var(--fg-primary);
    cursor: pointer;
    border: none;
    background: transparent;
    transition: background 0.08s;
  }

  .palette-item:hover {
    background: var(--bg-tertiary);
  }

  .palette-item-selected {
    background: rgba(88, 166, 255, 0.1) !important;
    color: var(--info);
  }

  .palette-item-icon {
    width: 20px;
    text-align: center;
    font-size: 14px;
    flex-shrink: 0;
    opacity: 0.7;
  }

  .palette-item-label {
    flex: 1;
  }

  .palette-item-hint {
    font-family: var(--font-mono);
    font-size: 10px;
    padding: 1px 5px;
    background: var(--bg-tertiary);
    border: 1px solid var(--border);
    border-radius: 3px;
    color: var(--fg-muted);
    flex-shrink: 0;
  }
</style>
