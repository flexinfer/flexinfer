<script>
  /**
   * Compact SVG donut chart with optional center label.
   * @type {{ segments: Array<{label: string, value: number, color: string}>, size?: number, thickness?: number, centerLabel?: string, centerValue?: string }}
   */
  let { segments = [], size = 64, thickness = 8, centerLabel = '', centerValue = '' } = $props();

  const instanceId = Math.random().toString(36).slice(2, 8);

  let total = $derived(segments.reduce((sum, s) => sum + s.value, 0));

  let arcs = $derived.by(() => {
    if (total <= 0) return [];
    const cx = size / 2;
    const cy = size / 2;
    const r = (size - thickness) / 2;
    const result = [];
    let angle = -90; // start at 12 o'clock

    for (const seg of segments) {
      if (seg.value <= 0) continue;
      const sweep = (seg.value / total) * 360;
      const startRad = (angle * Math.PI) / 180;
      const endRad = ((angle + sweep) * Math.PI) / 180;

      const x1 = cx + r * Math.cos(startRad);
      const y1 = cy + r * Math.sin(startRad);
      const x2 = cx + r * Math.cos(endRad);
      const y2 = cy + r * Math.sin(endRad);

      const largeArc = sweep > 180 ? 1 : 0;
      const d = `M ${x1.toFixed(2)} ${y1.toFixed(2)} A ${r} ${r} 0 ${largeArc} 1 ${x2.toFixed(2)} ${y2.toFixed(2)}`;

      result.push({ d, color: seg.color, label: seg.label, value: seg.value });
      angle += sweep;
    }
    return result;
  });
</script>

<div class="donut" style:width="{size}px" style:height="{size}px">
  <svg viewBox="0 0 {size} {size}" width={size} height={size} aria-label="Donut chart">
    {#if total <= 0}
      <circle
        cx={size / 2}
        cy={size / 2}
        r={(size - thickness) / 2}
        fill="none"
        stroke="var(--border)"
        stroke-width={thickness}
      />
    {:else}
      {#each arcs as arc}
        <path
          d={arc.d}
          fill="none"
          stroke={arc.color}
          stroke-width={thickness}
          stroke-linecap="round"
        >
          <title>{arc.label}: {arc.value}</title>
        </path>
      {/each}
    {/if}
  </svg>
  {#if centerValue || centerLabel}
    <div class="donut-center">
      {#if centerValue}<span class="donut-value">{centerValue}</span>{/if}
      {#if centerLabel}<span class="donut-label">{centerLabel}</span>{/if}
    </div>
  {/if}
</div>

<style>
  .donut {
    position: relative;
    flex-shrink: 0;
  }

  .donut-center {
    position: absolute;
    inset: 0;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    pointer-events: none;
  }

  .donut-value {
    font-family: var(--font-mono);
    font-size: 13px;
    font-weight: 700;
    color: var(--fg-primary);
    line-height: 1;
  }

  .donut-label {
    font-size: 8px;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    color: var(--fg-muted);
    line-height: 1;
    margin-top: 1px;
  }
</style>
