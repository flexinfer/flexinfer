<script>
  /** @type {{ data: number[], width?: number, height?: number, color?: string, showTrend?: boolean }} */
  let { data = [], width = 120, height = 24, color = 'var(--info)', showTrend = false } = $props();

  // Stable unique IDs — generated once per component instance (not on every reactive update).
  const instanceId = Math.random().toString(36).slice(2, 8);
  const gradientId = `spark-grad-${instanceId}`;

  // Compute SVG polyline points and path for smooth cubic interpolation.
  let computed = $derived.by(() => {
    if (!data || data.length < 2) return null;
    const min = Math.min(...data);
    const max = Math.max(...data);
    const range = max - min || 1;
    const stepX = width / (data.length - 1);
    const padding = 2;
    const usableHeight = height - padding * 2;

    const pts = data.map((val, i) => ({
      x: i * stepX,
      y: padding + usableHeight - ((val - min) / range) * usableHeight,
      value: val,
    }));

    // Build smooth cubic bezier path (Catmull-Rom to cubic bezier).
    let pathD = `M ${pts[0].x.toFixed(1)},${pts[0].y.toFixed(1)}`;
    for (let i = 0; i < pts.length - 1; i++) {
      const p0 = pts[Math.max(i - 1, 0)];
      const p1 = pts[i];
      const p2 = pts[i + 1];
      const p3 = pts[Math.min(i + 2, pts.length - 1)];

      const tension = 0.3;
      const cp1x = p1.x + (p2.x - p0.x) * tension;
      const cp1y = p1.y + (p2.y - p0.y) * tension;
      const cp2x = p2.x - (p3.x - p1.x) * tension;
      const cp2y = p2.y - (p3.y - p1.y) * tension;

      pathD += ` C ${cp1x.toFixed(1)},${cp1y.toFixed(1)} ${cp2x.toFixed(1)},${cp2y.toFixed(1)} ${p2.x.toFixed(1)},${p2.y.toFixed(1)}`;
    }

    // Build fill path (closed polygon under the curve).
    const fillD = pathD +
      ` L ${pts[pts.length - 1].x.toFixed(1)},${height}` +
      ` L ${pts[0].x.toFixed(1)},${height} Z`;

    // Polyline points (fallback for tooltip hit areas).
    const polyPoints = pts.map(p => `${p.x.toFixed(1)},${p.y.toFixed(1)}`).join(' ');

    // Min/max annotation points.
    const minIdx = data.indexOf(min);
    const maxIdx = data.indexOf(max);

    return { pts, pathD, fillD, polyPoints, min, max, minIdx, maxIdx };
  });

  let lastPoint = $derived(computed ? computed.pts[computed.pts.length - 1] : null);
  let minPoint = $derived(computed ? computed.pts[computed.minIdx] : null);
  let maxPoint = $derived(computed ? computed.pts[computed.maxIdx] : null);

  // Compute trend direction from last ~20% of data points.
  let trendArrow = $derived.by(() => {
    if (!showTrend || !data || data.length < 3) return '';
    const tail = Math.max(2, Math.floor(data.length * 0.2));
    const recent = data.slice(-tail);
    const first = recent[0];
    const last = recent[recent.length - 1];
    const delta = last - first;
    const threshold = (Math.max(...data) - Math.min(...data)) * 0.05 || 0.01;
    if (delta > threshold) return '↑';
    if (delta < -threshold) return '↓';
    return '→';
  });

  const glowFilterId = `spark-glow-${instanceId}`;

  // Tooltip state.
  let hoveredIdx = $state(null);
  let hoveredPt = $derived(hoveredIdx !== null && computed ? computed.pts[hoveredIdx] : null);

  function handleMouseMove(e) {
    if (!computed) return;
    const svg = e.currentTarget;
    const rect = svg.getBoundingClientRect();
    const mouseX = (e.clientX - rect.left) * (width / rect.width);
    // Find nearest point.
    let closest = 0;
    let closestDist = Infinity;
    for (let i = 0; i < computed.pts.length; i++) {
      const d = Math.abs(computed.pts[i].x - mouseX);
      if (d < closestDist) {
        closestDist = d;
        closest = i;
      }
    }
    hoveredIdx = closest;
  }

  function handleMouseLeave() {
    hoveredIdx = null;
  }

  function formatValue(v) {
    if (v >= 1000) return (v / 1000).toFixed(1) + 'k';
    if (Number.isInteger(v)) return v.toString();
    return v.toFixed(1);
  }
</script>

<svg
  {width}
  {height}
  viewBox="0 0 {width} {height}"
  class="sparkline"
  role="img"
  aria-label="Sparkline chart"
  onmousemove={handleMouseMove}
  onmouseleave={handleMouseLeave}
>
  {#if computed}
    <!-- Gradient + glow definitions -->
    <defs>
      <linearGradient id={gradientId} x1="0%" y1="0%" x2="0%" y2="100%">
        <stop offset="0%" stop-color={color} stop-opacity="0.25" />
        <stop offset="100%" stop-color={color} stop-opacity="0.02" />
      </linearGradient>
      <filter id={glowFilterId} x="-20%" y="-20%" width="140%" height="140%">
        <feGaussianBlur in="SourceGraphic" stdDeviation="1.5" result="blur" />
        <feMerge>
          <feMergeNode in="blur" />
          <feMergeNode in="SourceGraphic" />
        </feMerge>
      </filter>
    </defs>

    <!-- Gradient fill under curve -->
    <path d={computed.fillD} fill="url(#{gradientId})" />

    <!-- Smooth curve with glow -->
    <path
      d={computed.pathD}
      fill="none"
      stroke={color}
      stroke-width="1.5"
      stroke-linecap="round"
      stroke-linejoin="round"
      filter="url(#{glowFilterId})"
    />

    <!-- Min annotation dot -->
    {#if minPoint && computed.min !== computed.max}
      <circle
        cx={minPoint.x}
        cy={minPoint.y}
        r="1.5"
        fill="var(--fg-muted)"
        opacity="0.6"
      />
    {/if}

    <!-- Max annotation dot -->
    {#if maxPoint && computed.min !== computed.max}
      <circle
        cx={maxPoint.x}
        cy={maxPoint.y}
        r="1.5"
        fill={color}
        opacity="0.8"
      />
    {/if}

    <!-- Last point (current value) -->
    {#if lastPoint}
      <circle
        cx={lastPoint.x}
        cy={lastPoint.y}
        r="2.5"
        fill={color}
      />
    {/if}

    <!-- Hover crosshair + tooltip -->
    {#if hoveredPt}
      <line
        x1={hoveredPt.x}
        y1="0"
        x2={hoveredPt.x}
        y2={height}
        stroke="var(--fg-muted)"
        stroke-width="0.5"
        stroke-dasharray="2,2"
        opacity="0.5"
      />
      <circle
        cx={hoveredPt.x}
        cy={hoveredPt.y}
        r="3"
        fill={color}
        opacity="0.9"
      />
      <text
        x={hoveredPt.x < width / 2 ? hoveredPt.x + 6 : hoveredPt.x - 6}
        y={hoveredPt.y > 10 ? hoveredPt.y - 5 : hoveredPt.y + 12}
        text-anchor={hoveredPt.x < width / 2 ? 'start' : 'end'}
        class="tooltip-text"
        fill="var(--fg-primary)"
      >
        {formatValue(hoveredPt.value)}
      </text>
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

{#if showTrend && trendArrow}
  <span
    class="trend-arrow"
    class:trend-up={trendArrow === '↑'}
    class:trend-down={trendArrow === '↓'}
    class:trend-flat={trendArrow === '→'}
  >{trendArrow}</span>
{/if}

<style>
  .sparkline {
    display: block;
    flex-shrink: 0;
    cursor: crosshair;
  }

  .tooltip-text {
    font-size: 8px;
    font-family: var(--font-mono);
    font-feature-settings: 'tnum';
    pointer-events: none;
  }

  .trend-arrow {
    display: inline-block;
    font-size: 12px;
    font-weight: 700;
    margin-left: 4px;
    vertical-align: middle;
    line-height: 1;
  }

  .trend-up { color: var(--success); }
  .trend-down { color: var(--error); }
  .trend-flat { color: var(--fg-muted); }
</style>
