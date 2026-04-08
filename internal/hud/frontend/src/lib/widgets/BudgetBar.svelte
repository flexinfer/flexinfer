<script lang="ts">
  /**
   * BudgetBar renders a horizontal progress bar for spawn budget caps (cost or turns).
   *
   * Color bands: green <50%, amber 50-80%, red >80%.
   *
   * When `max` is 0 or undefined the caller is signalling "unlimited" — the bar
   * is not rendered; only the label and current value are shown.
   */
  interface Props {
    label: string;
    current: number;
    max?: number;
    formatValue?: (n: number) => string;
    costEstimated?: boolean;
  }

  let {
    label,
    current,
    max,
    formatValue = (n: number) => n.toString(),
    costEstimated = false,
  }: Props = $props();

  // Unlimited when max is missing/zero/negative.
  let unlimited = $derived(!max || max <= 0);

  let pct = $derived.by(() => {
    if (unlimited) return 0;
    const raw = (current / (max as number)) * 100;
    if (raw < 0) return 0;
    if (raw > 100) return 100;
    return raw;
  });

  let fillColor = $derived.by(() => {
    if (pct > 80) return 'var(--error)';
    if (pct >= 50) return 'var(--warning)';
    return 'var(--success)';
  });

  function formatCurrent(n: number): string {
    const base = formatValue(n);
    return costEstimated ? `~${base}` : base;
  }
</script>

<div class="budget-bar" role="meter" aria-label={label} aria-valuemin={0} aria-valuemax={unlimited ? undefined : max} aria-valuenow={current}>
  <div class="budget-bar-header">
    <span class="budget-bar-label">{label}</span>
    {#if unlimited}
      <span class="budget-bar-value unlimited">{formatCurrent(current)} / unlimited</span>
    {:else}
      <span class="budget-bar-value" style:color={fillColor}>
        {formatCurrent(current)} / {formatValue(max as number)}
      </span>
    {/if}
  </div>
  {#if !unlimited}
    <div class="budget-bar-track">
      <div
        class="budget-bar-fill"
        style:width="{pct}%"
        style:background={fillColor}
        style:--budget-fill={fillColor}
      ></div>
    </div>
  {/if}
</div>

<style>
  .budget-bar {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
    width: 100%;
  }

  .budget-bar-header {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
    gap: var(--space-2);
  }

  .budget-bar-label {
    font-size: var(--text-xs);
    color: var(--fg-dim);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
  }

  .budget-bar-value {
    font-size: var(--text-xs);
    color: var(--fg-primary);
    font-family: var(--font-mono);
    font-variant-numeric: tabular-nums;
  }

  .budget-bar-value.unlimited {
    color: var(--fg-secondary);
  }

  .budget-bar-track {
    height: 6px;
    background: var(--bg-primary);
    border-radius: var(--radius-xs);
    overflow: hidden;
    border: 1px solid var(--border);
  }

  .budget-bar-fill {
    height: 100%;
    border-radius: var(--radius-xs);
    transition: width 0.3s ease, background 0.3s ease;
    box-shadow: 0 0 6px color-mix(in srgb, var(--budget-fill, var(--success)) 25%, transparent);
  }
</style>
