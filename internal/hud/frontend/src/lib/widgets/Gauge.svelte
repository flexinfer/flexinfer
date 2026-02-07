<script>
  /** @type {{ value: number, max: number, label?: string, color?: string, showPercentage?: boolean }} */
  let { value = 0, max = 100, label = '', color = 'var(--info)', showPercentage = true } = $props();

  let pct = $derived(max > 0 ? Math.min((value / max) * 100, 100) : 0);

  let resolvedColor = $derived.by(() => {
    if (pct >= 80) return 'var(--error)';
    if (pct >= 60) return 'var(--warning)';
    return color;
  });
</script>

<div class="gauge" role="meter" aria-valuenow={value} aria-valuemin={0} aria-valuemax={max} aria-label={label}>
  {#if label}
    <div class="gauge-header">
      <span class="gauge-label">{label}</span>
      {#if showPercentage}
        <span class="gauge-pct" style:color={resolvedColor}>{pct.toFixed(0)}%</span>
      {/if}
    </div>
  {/if}
  <div class="gauge-track">
    <div
      class="gauge-fill"
      style:width="{pct}%"
      style:background={resolvedColor}
    ></div>
  </div>
</div>

<style>
  .gauge {
    width: 100%;
  }

  .gauge-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 3px;
  }

  .gauge-label {
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    color: var(--fg-muted);
  }

  .gauge-pct {
    font-size: 11px;
    font-family: var(--font-mono);
    font-weight: 600;
  }

  .gauge-track {
    height: 6px;
    background: var(--bg-primary);
    border-radius: 3px;
    overflow: hidden;
    border: 1px solid var(--border);
  }

  .gauge-fill {
    height: 100%;
    border-radius: 3px;
    transition: width 0.3s ease, background 0.3s ease;
    box-shadow: 0 0 6px rgba(88, 166, 255, 0.15);
  }
</style>
