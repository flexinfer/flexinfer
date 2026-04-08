<script lang="ts">
  import type { SpawnTelemetry } from '../../stores/spawn.svelte.ts';

  interface Props {
    telemetry: SpawnTelemetry | null;
  }

  let { telemetry }: Props = $props();

  type TokenRow = { label: string; value: number };

  let tokenRows = $derived.by<TokenRow[]>(() => {
    const u = telemetry?.token_usage;
    if (!u) return [];
    return [
      { label: 'Input', value: u.input_tokens ?? 0 },
      { label: 'Output', value: u.output_tokens ?? 0 },
      { label: 'Cache create', value: u.cache_creation_tokens ?? 0 },
      { label: 'Cache read', value: u.cache_read_tokens ?? 0 },
    ];
  });

  let tokenMax = $derived.by(() => {
    const max = tokenRows.reduce((acc, r) => (r.value > acc ? r.value : acc), 0);
    return max > 0 ? max : 1;
  });

  let modelEntries = $derived.by(() => {
    const m = telemetry?.model_usage;
    if (!m) return [];
    return Object.entries(m).sort((a, b) => (b[1].cost_usd ?? 0) - (a[1].cost_usd ?? 0));
  });

  function formatTokens(n: number): string {
    if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`;
    if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
    return String(n);
  }

  function formatCost(n: number): string {
    return `$${n.toFixed(4)}`;
  }

  function pct(n: number): number {
    return (n / tokenMax) * 100;
  }
</script>

<div class="tab-content">
  {#if !telemetry}
    <div class="tab-empty">No telemetry yet.</div>
  {:else}
    <section class="usage-section">
      <div class="section-label">Token usage</div>
      <div class="token-bars">
        {#each tokenRows as row}
          <div class="token-row">
            <div class="token-row-header">
              <span class="token-label">{row.label}</span>
              <span class="token-value">{formatTokens(row.value)}</span>
            </div>
            <div class="token-track">
              <div class="token-fill" style:width="{pct(row.value)}%"></div>
            </div>
          </div>
        {/each}
      </div>
    </section>

    {#if modelEntries.length > 0}
      <section class="usage-section">
        <div class="section-label">Model usage</div>
        <div class="model-list">
          {#each modelEntries as [model, use]}
            <div class="model-row">
              <span class="model-name">{model}</span>
              <span class="model-tokens">
                in {formatTokens(use.input_tokens ?? 0)} / out {formatTokens(use.output_tokens ?? 0)}
              </span>
              <span class="model-cost">{formatCost(use.cost_usd ?? 0)}</span>
            </div>
          {/each}
        </div>
      </section>
    {/if}
  {/if}
</div>

<style>
  .tab-content {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    padding: var(--space-3);
    background: var(--bg-secondary);
    border-radius: var(--radius-md);
    border: 1px solid var(--border);
    font-family: var(--font-mono);
  }

  .tab-empty {
    padding: var(--space-2);
    color: var(--fg-secondary);
    font-size: var(--text-sm);
  }

  .usage-section {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .section-label {
    font-size: var(--text-xs);
    color: var(--fg-dim);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
  }

  .token-bars {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .token-row {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
  }

  .token-row-header {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
  }

  .token-label {
    font-size: var(--text-xs);
    color: var(--fg-secondary);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
  }

  .token-value {
    font-size: var(--text-sm);
    color: var(--fg-primary);
    font-variant-numeric: tabular-nums;
  }

  .token-track {
    height: 6px;
    background: var(--bg-primary);
    border: 1px solid var(--border);
    border-radius: var(--radius-xs);
    overflow: hidden;
  }

  .token-fill {
    height: 100%;
    background: var(--info);
    border-radius: var(--radius-xs);
    box-shadow: 0 0 6px var(--info-glow);
    transition: width 0.3s ease;
  }

  .model-list {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
  }

  .model-row {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    font-size: var(--text-sm);
    padding: var(--space-1) 0;
    border-bottom: 1px solid var(--border-subtle);
  }

  .model-name {
    color: var(--fg-primary);
    font-weight: 500;
  }

  .model-tokens {
    color: var(--fg-dim);
    font-size: var(--text-xs);
    margin-left: var(--space-2);
  }

  .model-cost {
    margin-left: auto;
    color: var(--success);
    font-variant-numeric: tabular-nums;
  }
</style>
