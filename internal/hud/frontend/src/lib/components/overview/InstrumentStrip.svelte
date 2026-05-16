<script lang="ts">
  // InstrumentStrip - extracted from OverviewPanel:177-210 + matching template.
  //
  // Four signal-ring gauges + today summary chip. Pure presentation; the
  // parent passes the derived instrument array.

  export interface Instrument {
    label: string;
    value: number;
    max: number;
    suffix?: string;
    color: string;
    route: string;
  }

  interface Props {
    instruments: Instrument[];
    todayLabel: string;
    refreshedLabel: string | null;
    onSelect: (route: string) => void;
  }
  let { instruments, todayLabel, refreshedLabel, onSelect }: Props = $props();

  function ringDashOffset(pct: number): number {
    const r = 18;
    const circumference = 2 * Math.PI * r;
    return circumference - (pct / 100) * circumference;
  }
</script>

<div class="signal-strip" role="group" aria-label="Signal strip">
  {#each instruments as inst, i (inst.label)}
    <button
      class="instrument"
      onclick={() => onSelect(inst.route)}
      style="animation-delay: {i * 60}ms;"
      aria-label="{inst.label} {inst.value}{inst.suffix ?? ''}"
    >
      <div class="inst-ring">
        <svg viewBox="0 0 44 44" class="ring-svg">
          <circle cx="22" cy="22" r="18" class="ring-track" />
          <circle
            cx="22" cy="22" r="18"
            class="ring-fill"
            style="
              stroke: {inst.color};
              stroke-dasharray: {2 * Math.PI * 18};
              stroke-dashoffset: {ringDashOffset(inst.max > 0 ? (inst.value / inst.max) * 100 : 0)};
              filter: drop-shadow(0 0 4px {inst.color});
            "
          />
        </svg>
        <span class="inst-ring-value">{inst.value}{inst.suffix ?? ''}</span>
      </div>
      <div class="inst-data">
        <span class="inst-label">{inst.label}</span>
      </div>
    </button>
  {/each}

  <div class="strip-divider"></div>

  <div class="strip-summary">
    <span class="strip-summary-label">Today</span>
    <span class="strip-summary-value">{todayLabel}</span>
    {#if refreshedLabel}
      <span class="strip-refreshed">Last refreshed: {refreshedLabel}</span>
    {/if}
  </div>
</div>

<style>
  .signal-strip {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    width: 100%;
    max-width: 100%;
    min-width: 0;
    gap: var(--space-3);
    padding: var(--space-3) var(--space-4);
    border-radius: var(--radius-lg);
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    position: relative;
    overflow: visible;
  }

  .signal-strip::before {
    content: '';
    position: absolute;
    inset: 0;
    opacity: 0.03;
    background-image:
      linear-gradient(var(--fg-muted) 1px, transparent 1px),
      linear-gradient(90deg, var(--fg-muted) 1px, transparent 1px);
    background-size: 24px 24px;
    pointer-events: none;
  }

  .signal-strip::after {
    content: '';
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    height: 1px;
    background: linear-gradient(
      90deg,
      transparent 5%,
      rgba(0, 200, 255, 0.15) 30%,
      rgba(0, 200, 255, 0.25) 50%,
      rgba(0, 200, 255, 0.15) 70%,
      transparent 95%
    );
    pointer-events: none;
  }

  .instrument {
    display: flex;
    align-items: center;
    gap: var(--space-3);
    padding: var(--space-2) var(--space-3);
    border-radius: var(--radius-md);
    cursor: pointer;
    transition: background var(--transition-fast), transform var(--transition-fast);
    position: relative;
    z-index: 1;
    animation: fadeIn 0.25s ease-out both;
    background: none;
    border: none;
  }

  .instrument:hover {
    background: rgba(0, 200, 255, 0.05);
    transform: translateY(-1px);
  }

  .inst-ring {
    position: relative;
    width: 44px;
    height: 44px;
    flex-shrink: 0;
  }

  .ring-svg {
    width: 100%;
    height: 100%;
    transform: rotate(-90deg);
    overflow: visible;
  }

  .ring-track {
    fill: none;
    stroke: var(--border);
    stroke-width: 2.5;
  }

  .ring-fill {
    fill: none;
    stroke-width: 2.5;
    stroke-linecap: round;
    transition: stroke-dashoffset 0.6s cubic-bezier(0.4, 0, 0.2, 1);
  }

  .inst-ring-value {
    position: absolute;
    inset: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    font-family: var(--font-mono);
    font-size: 11px;
    font-weight: 700;
    color: var(--fg-primary);
    font-feature-settings: 'tnum';
  }

  .inst-data {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .inst-label {
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-secondary);
    white-space: nowrap;
  }

  .strip-divider {
    width: 1px;
    height: 32px;
    background: var(--border);
    flex-shrink: 0;
    margin: 0 var(--space-1);
  }

  .strip-summary {
    display: flex;
    flex-direction: column;
    gap: 2px;
    margin-left: auto;
    text-align: right;
    flex-shrink: 0;
    z-index: 1;
  }

  .strip-summary-label {
    font-size: 9px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wider);
    color: var(--fg-muted);
  }

  .strip-summary-value {
    font-size: 11px;
    font-family: var(--font-mono);
    color: var(--fg-secondary);
    white-space: nowrap;
  }

  .strip-refreshed {
    font-size: var(--text-2xs, 10px);
    font-family: var(--font-mono);
    color: var(--fg-muted);
    opacity: 0.7;
  }

  @media (max-width: 768px) {
    .signal-strip {
      overflow-x: auto;
      scrollbar-width: none;
      -webkit-overflow-scrolling: touch;
      gap: var(--space-2);
      padding: var(--space-2) var(--space-3);
    }

    .signal-strip::-webkit-scrollbar {
      display: none;
    }

    .instrument {
      flex-shrink: 0;
    }

    .strip-summary,
    .strip-divider {
      display: none;
    }
  }
</style>
