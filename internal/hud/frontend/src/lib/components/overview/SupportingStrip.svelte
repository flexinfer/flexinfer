<script lang="ts">
  // SupportingStrip - the collapsed "Supporting" row at the bottom of Overview.
  //
  // Carries Memory / Stream / Sandbox / Graph chips. Collapses to an inline pill
  // strip; expands to a grid card layout. Pure presentation - parent passes
  // the derived surface array.

  interface Surface {
    route: string;
    label: string;
    value: string;
    detail: string;
  }

  interface Props {
    surfaces: Surface[];
    onSelect: (route: string) => void;
  }
  let { surfaces, onSelect }: Props = $props();

  let expanded = $state(false);
</script>

<section class="support-section">
  <button class="support-toggle" onclick={() => { expanded = !expanded; }}>
    <span class="support-toggle-label">Supporting</span>
    {#if !expanded}
      <span class="support-inline">
        {#each surfaces as s (s.route)}
          <span
            class="support-chip-inline"
            role="button"
            tabindex="0"
            onclick={(e) => { e.stopPropagation(); onSelect(s.route); }}
            onkeydown={(e) => { if (e.key === 'Enter') { e.stopPropagation(); onSelect(s.route); } }}
          >
            <span class="sci-label">{s.label}</span>
            <strong>{s.value}</strong>
          </span>
        {/each}
      </span>
    {/if}
    <span class="support-chevron" class:chevron-open={expanded}>
      <svg width="10" height="10" viewBox="0 0 10 10"><path d="M3 2l4 3-4 3" fill="currentColor"/></svg>
    </span>
  </button>

  {#if expanded}
    <div class="support-grid">
      {#each surfaces as surface (surface.route)}
        <button class="support-chip" onclick={() => onSelect(surface.route)}>
          <span class="chip-label">{surface.label}</span>
          <span class="chip-value">{surface.value}</span>
          <span class="chip-detail">{surface.detail}</span>
        </button>
      {/each}
    </div>
  {/if}
</section>

<style>
  .support-section {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .support-toggle {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    cursor: pointer;
    background: none;
    border: none;
    padding: 0;
    width: 100%;
    text-align: left;
    color: var(--fg-secondary);
  }

  .support-toggle:hover .support-toggle-label {
    color: var(--fg-primary);
  }

  .support-toggle-label {
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-muted);
    flex-shrink: 0;
    transition: color var(--transition-fast);
  }

  .support-inline {
    display: flex;
    gap: var(--space-2);
    flex: 1;
    overflow: hidden;
    margin-left: var(--space-2);
  }

  .support-chip-inline {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 2px 8px;
    border-radius: var(--radius-full);
    border: 1px solid var(--border);
    font-size: 10px;
    font-family: var(--font-mono);
    color: var(--fg-muted);
    white-space: nowrap;
    cursor: pointer;
    transition: border-color var(--transition-fast), color var(--transition-fast);
  }

  .support-chip-inline:hover {
    border-color: var(--border-focus);
    color: var(--fg-secondary);
  }

  .sci-label { color: var(--fg-muted); }
  .support-chip-inline strong { color: var(--fg-secondary); font-weight: 600; }

  .support-chevron {
    color: var(--fg-muted);
    transition: transform var(--transition-fast);
    flex-shrink: 0;
    display: flex;
    align-items: center;
    margin-left: auto;
  }

  .chevron-open { transform: rotate(90deg); }

  .support-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 160px), 1fr));
    gap: var(--space-2);
  }

  .support-chip {
    display: flex;
    flex-direction: column;
    gap: 3px;
    text-align: left;
    padding: var(--space-3);
    border-radius: var(--radius-sm);
    border: 1px solid var(--border);
    background: var(--bg-secondary);
    cursor: pointer;
    transition: border-color var(--transition-normal), transform var(--transition-fast), box-shadow var(--transition-normal);
  }

  .support-chip:hover {
    border-color: rgba(0, 200, 255, 0.2);
    transform: translateY(-1px);
    box-shadow: 0 0 12px var(--glow-info), var(--shadow-xs);
  }

  .chip-label {
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-muted);
  }

  .chip-value {
    font-size: 16px;
    font-weight: 700;
    font-family: var(--font-mono);
    color: var(--fg-primary);
  }

  .chip-detail {
    font-size: 10px;
    color: var(--fg-muted);
    font-family: var(--font-mono);
  }

  @media (max-width: 900px) {
    .support-grid {
      grid-template-columns: repeat(2, 1fr);
    }
  }

  @media (max-width: 480px) {
    .support-grid {
      grid-template-columns: 1fr 1fr;
    }

    .support-inline {
      display: none;
    }
  }
</style>
