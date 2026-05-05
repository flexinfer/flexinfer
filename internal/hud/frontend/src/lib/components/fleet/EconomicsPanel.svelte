<script>
  // F8 (D2): Token-economics dashboard. Fetches /api/fleet/economics?window=7d
  // on mount and renders six ratio cards plus a stacked bar of frontier vs
  // local tokens. Null ratios render as "—".
  import { labsAuthStore } from '../../stores/labsAuth.svelte.ts';

  let snapshot = $state(/** @type {any} */ (null));
  let loading = $state(true);
  let error = $state(/** @type {string|null} */ (null));
  const WINDOW = '7d';

  async function load() {
    loading = true;
    error = null;
    try {
      // Endpoint is admin-gated — forward the token from LabsAccessBar.
      /** @type {Record<string, string>} */
      const headers = {};
      const token = labsAuthStore.adminToken.trim();
      if (token) headers['X-Admin-Token'] = token;
      const res = await fetch(`/api/fleet/economics?window=${WINDOW}`, { credentials: 'same-origin', headers });
      if (!res.ok) {
        // Surface the daemon's error message (e.g. admin-token not configured)
        // instead of a bare "HTTP 403". Falls back to status code if the body
        // isn't JSON or has no message.
        let detail = `HTTP ${res.status}`;
        try {
          const body = await res.json();
          if (body && typeof body.error === 'string' && body.error.length > 0) {
            detail = body.error;
          }
        } catch {
          // body wasn't JSON — keep the status-code fallback
        }
        throw new Error(detail);
      }
      snapshot = await res.json();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  $effect(() => {
    void load();
    const timer = setInterval(load, 60_000);
    return () => clearInterval(timer);
  });

  function formatRatio(r) {
    if (!r || r.value == null) return '—';
    if (Math.abs(r.value) >= 100) return r.value.toFixed(0);
    if (Math.abs(r.value) >= 10) return r.value.toFixed(1);
    return r.value.toFixed(2);
  }

  function statusLabel(r) {
    if (!r || r.status === 'ok') return '';
    return r.status.replaceAll('_', ' ');
  }

  const CARDS = [
    { key: 'token_savings', label: 'Token savings', hint: '1 − comp/raw' },
    { key: 'tool_call_reduced', label: 'Tool calls reduced', hint: 'before/after' },
    { key: 'cost_ratio', label: 'Cost ratio', hint: 'frontier/total' },
    { key: 'context_waste', label: 'Context waste', hint: 'tool resp/input' },
    { key: 'compression', label: 'Compression', hint: 'raw/compressed' },
    { key: 'local_utilization', label: 'Local util.', hint: 'weaver/total' },
  ];

  let ratios = $derived(snapshot?.ratios ?? {});
  let tokens = $derived(snapshot?.tokens ?? { frontier_tokens: 0, local_tokens: 0 });
  let total = $derived(Math.max(1, tokens.frontier_tokens + tokens.local_tokens));
  let frontierPct = $derived((tokens.frontier_tokens / total) * 100);
  let localPct = $derived((tokens.local_tokens / total) * 100);
</script>

<div class="economics-card">
  <div class="econ-header">
    <span class="econ-title">Token economics · {WINDOW}</span>
    {#if loading && !snapshot}
      <span class="econ-sub">loading…</span>
    {:else if error}
      <span class="econ-sub econ-error">error: {error}</span>
    {:else if snapshot?.generated_at}
      <span class="econ-sub">updated {new Date(snapshot.generated_at).toLocaleTimeString()}</span>
    {/if}
  </div>

  <div class="ratio-grid">
    {#each CARDS as card (card.key)}
      {@const r = ratios[card.key]}
      <div class="ratio-card" class:ratio-stub={r?.status && r.status !== 'ok'}>
        <div class="ratio-value">{formatRatio(r)}</div>
        <div class="ratio-label">{card.label}</div>
        <div class="ratio-hint">{card.hint}</div>
        {#if statusLabel(r)}
          <div class="ratio-status">{statusLabel(r)}</div>
        {/if}
      </div>
    {/each}
  </div>

  <div class="bar-wrapper">
    <div class="bar-legend">
      <span class="legend-item"><span class="swatch swatch-frontier"></span>Frontier {tokens.frontier_tokens.toLocaleString()}</span>
      <span class="legend-item"><span class="swatch swatch-local"></span>Local {tokens.local_tokens.toLocaleString()}</span>
    </div>
    <svg class="bar-svg" viewBox="0 0 100 8" preserveAspectRatio="none" aria-label="Frontier vs local token split">
      <rect x="0" y="0" width={frontierPct} height="8" class="bar-frontier" />
      <rect x={frontierPct} y="0" width={localPct} height="8" class="bar-local" />
    </svg>
  </div>
</div>

<style>
  .economics-card {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    padding: var(--space-3);
    margin-bottom: var(--space-3);
  }
  .econ-header {
    display: flex; justify-content: space-between; align-items: baseline;
    margin-bottom: var(--space-2);
  }
  .econ-title {
    font-size: var(--text-sm); font-weight: 600; color: var(--fg-primary);
    text-transform: uppercase; letter-spacing: var(--tracking-wide);
  }
  .econ-sub { font-family: var(--font-mono); font-size: var(--text-xs); color: var(--fg-muted); }
  .econ-error { color: var(--error); }
  .ratio-grid {
    display: grid; grid-template-columns: repeat(6, minmax(0, 1fr));
    gap: var(--space-2); margin-bottom: var(--space-3);
  }
  .ratio-card {
    background: var(--bg-primary); border: 1px solid var(--border-subtle);
    border-radius: var(--radius-sm); padding: 8px 10px;
    display: flex; flex-direction: column; gap: 2px;
  }
  .ratio-stub { opacity: 0.6; }
  .ratio-value {
    font-family: var(--font-mono); font-size: 18px; font-weight: 700;
    color: var(--fg-primary); line-height: 1.1;
  }
  .ratio-label { font-size: var(--text-xs); color: var(--fg-secondary); }
  .ratio-hint { font-size: 10px; color: var(--fg-dim); font-family: var(--font-mono); }
  .ratio-status {
    font-size: 9px; color: var(--warning); text-transform: uppercase;
    letter-spacing: 0.06em; margin-top: 2px;
  }
  .bar-wrapper { display: flex; flex-direction: column; gap: 4px; }
  .bar-legend {
    display: flex; gap: var(--space-3); font-size: var(--text-xs);
    color: var(--fg-muted); font-family: var(--font-mono);
  }
  .legend-item { display: flex; align-items: center; gap: 6px; }
  .swatch { width: 10px; height: 10px; border-radius: 2px; display: inline-block; }
  .swatch-frontier { background: var(--accent); }
  .swatch-local { background: var(--success); }
  .bar-svg {
    width: 100%; height: 8px; border-radius: 4px;
    overflow: hidden; background: var(--bg-tertiary);
  }
  .bar-frontier { fill: var(--accent); }
  .bar-local { fill: var(--success); }
  @media (max-width: 900px) { .ratio-grid { grid-template-columns: repeat(3, minmax(0, 1fr)); } }
  @media (max-width: 600px) { .ratio-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
</style>
