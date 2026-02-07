<script>
  /**
   * VirtualList — renders only visible items + buffer for large lists.
   *
   * Props:
   *  - items: T[]           — full data array
   *  - itemHeight: number   — fixed height per row (px)
   *  - buffer: number       — extra rows to render above/below viewport
   *
   * Slots:
   *  - default: receives { item, index } for each visible item
   *
   * @type {{
   *   items: any[],
   *   itemHeight?: number,
   *   buffer?: number,
   *   children: import('svelte').Snippet<[{ item: any, index: number }]>
   * }}
   */
  let { items = [], itemHeight = 32, buffer = 10, children } = $props();

  let containerEl = $state(null);
  let scrollTop = $state(0);
  let containerHeight = $state(400);

  function handleScroll() {
    if (containerEl) {
      scrollTop = containerEl.scrollTop;
    }
  }

  let totalHeight = $derived(items.length * itemHeight);

  let startIdx = $derived(Math.max(0, Math.floor(scrollTop / itemHeight) - buffer));
  let endIdx = $derived(Math.min(items.length, Math.ceil((scrollTop + containerHeight) / itemHeight) + buffer));

  let visibleItems = $derived(
    items.slice(startIdx, endIdx).map((item, i) => ({
      item,
      index: startIdx + i,
      offsetY: (startIdx + i) * itemHeight,
    }))
  );

  $effect(() => {
    if (containerEl) {
      containerHeight = containerEl.clientHeight;
      const ro = new ResizeObserver((entries) => {
        containerHeight = entries[0].contentRect.height;
      });
      ro.observe(containerEl);
      return () => ro.disconnect();
    }
  });
</script>

<div
  class="virtual-list"
  bind:this={containerEl}
  onscroll={handleScroll}
>
  <div class="virtual-list-spacer" style:height="{totalHeight}px">
    {#each visibleItems as { item, index, offsetY } (index)}
      <div class="virtual-list-item" style:transform="translateY({offsetY}px)" style:height="{itemHeight}px">
        {@render children({ item, index })}
      </div>
    {/each}
  </div>
</div>

<style>
  .virtual-list {
    overflow-y: auto;
    height: 100%;
    position: relative;
  }

  .virtual-list-spacer {
    position: relative;
  }

  .virtual-list-item {
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
  }
</style>
