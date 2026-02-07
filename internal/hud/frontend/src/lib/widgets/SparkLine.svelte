<script>
  /** @type {{ data: number[], width?: number, height?: number, color?: string }} */
  let { data = [], width = 120, height = 24, color = 'var(--info)' } = $props();

  let points = $derived.by(() => {
    if (!data || data.length < 2) return '';
    const min = Math.min(...data);
    const max = Math.max(...data);
    const range = max - min || 1;
    const stepX = width / (data.length - 1);
    const padding = 2;
    const usableHeight = height - padding * 2;

    return data
      .map((val, i) => {
        const x = i * stepX;
        const y = padding + usableHeight - ((val - min) / range) * usableHeight;
        return `${x.toFixed(1)},${y.toFixed(1)}`;
      })
      .join(' ');
  });

  let lastValue = $derived(data.length > 0 ? data[data.length - 1] : null);
  let lastPoint = $derived.by(() => {
    if (!data || data.length < 2) return null;
    const min = Math.min(...data);
    const max = Math.max(...data);
    const range = max - min || 1;
    const stepX = width / (data.length - 1);
    const padding = 2;
    const usableHeight = height - padding * 2;
    const x = (data.length - 1) * stepX;
    const y = padding + usableHeight - ((data[data.length - 1] - min) / range) * usableHeight;
    return { x, y };
  });
</script>

<svg
  {width}
  {height}
  viewBox="0 0 {width} {height}"
  class="sparkline"
  role="img"
  aria-label="Sparkline chart"
>
  {#if points}
    <polyline
      {points}
      fill="none"
      stroke={color}
      stroke-width="1.5"
      stroke-linecap="round"
      stroke-linejoin="round"
    />
    {#if lastPoint}
      <circle
        cx={lastPoint.x}
        cy={lastPoint.y}
        r="2"
        fill={color}
      />
    {/if}
  {:else}
    <line
      x1="0"
      y1={height / 2}
      x2={width}
      y2={height / 2}
      stroke="var(--fg-muted)"
      stroke-width="1"
      stroke-dasharray="2,3"
    />
  {/if}
</svg>

<style>
  .sparkline {
    display: block;
    flex-shrink: 0;
  }
</style>
