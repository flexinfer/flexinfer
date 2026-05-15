<script lang="ts">
  // HeroSummary - extracted from OverviewPanel:233-289.
  //
  // Renders the "command" headline that summarizes the top pressure (file
  // conflicts > server health > blocked work > approvals > nominal). The
  // attention lanes list (right column) is composed beside the headline so
  // the slim composed Overview keeps the same visual layout.

  interface HeroSpec {
    eyebrow: string;
    headline: string;
    detail: string;
    tone: 'alert' | 'calm';
    action: { label: string; route: string } | null;
  }

  interface Lane {
    route: string;
    label: string;
    action: string;
    value: string;
    detail: string;
    severity: 'error' | 'warning' | 'info' | 'success';
    kind?: string;
    agent?: unknown;
  }

  interface Props {
    hero: HeroSpec;
    lanes: Lane[];
    onAction: (route: string) => void;
    onLaneClick: (lane: Lane) => void;
  }
  let { hero, lanes, onAction, onLaneClick }: Props = $props();
</script>

<section class="command-section" class:command-alert={hero.tone === 'alert'}>
  <div class="command-main">
    <div class="command-eyebrow">{hero.eyebrow}</div>
    <h1 class="command-title">{hero.headline}</h1>
    <p class="command-detail">{hero.detail}</p>
    {#if hero.action}
      <button class="command-action" onclick={() => onAction(hero.action!.route)}>
        {hero.action.label}
        <svg width="14" height="14" viewBox="0 0 14 14" fill="none">
          <path d="M5 3l4 4-4 4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
        </svg>
      </button>
    {/if}
  </div>

  {#if lanes.length > 0}
    <div class="lane-list">
      <div class="lane-list-label">Attention lanes</div>
      {#each lanes as lane (lane.route + lane.label)}
        <button class="lane-item lane-{lane.severity}" onclick={() => onLaneClick(lane)}>
          <div class="lane-head">
            <span class="lane-dot" style="background: var(--{lane.severity});"></span>
            <span class="lane-name">{lane.label}</span>
            <span class="lane-badge">{lane.action}</span>
          </div>
          <div class="lane-value">{lane.value}</div>
          <div class="lane-detail">{lane.detail}</div>
        </button>
      {/each}
    </div>
  {/if}
</section>

<style>
  .command-section {
    display: grid;
    grid-template-columns: 1fr minmax(260px, 0.5fr);
    width: 100%;
    max-width: 100%;
    min-width: 0;
    gap: var(--space-5);
    padding: var(--space-5);
    border-radius: var(--radius-lg);
    border: 1px solid var(--border);
    background:
      radial-gradient(ellipse at 15% 20%, rgba(0, 200, 255, 0.04), transparent 50%),
      var(--bg-secondary);
    position: relative;
  }

  .command-section::after {
    content: '';
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    height: 1px;
    background: linear-gradient(
      90deg,
      transparent 10%,
      rgba(0, 200, 255, 0.12) 40%,
      rgba(0, 200, 255, 0.08) 60%,
      transparent 90%
    );
  }

  .command-alert {
    border-color: rgba(255, 107, 53, 0.2);
    background:
      radial-gradient(ellipse at 15% 20%, rgba(255, 107, 53, 0.05), transparent 50%),
      var(--bg-secondary);
  }

  .command-alert::after {
    background: linear-gradient(
      90deg,
      transparent 10%,
      rgba(255, 107, 53, 0.15) 40%,
      rgba(255, 107, 53, 0.1) 60%,
      transparent 90%
    );
  }

  .command-main {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .command-eyebrow {
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-muted);
  }

  .command-title {
    margin: 0;
    font-size: clamp(22px, 2.4vw, 30px);
    font-weight: 700;
    line-height: var(--leading-tight);
    color: var(--fg-primary);
    letter-spacing: var(--tracking-tight);
  }

  .command-detail {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--fg-secondary);
    line-height: 1.5;
    max-width: 52ch;
    font-family: var(--font-mono);
  }

  .command-action {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 8px 16px;
    margin-top: var(--space-2);
    border-radius: var(--radius-sm);
    border: 1px solid rgba(255, 107, 53, 0.35);
    background: var(--accent-dim);
    color: var(--accent);
    font-size: 12px;
    font-weight: 600;
    font-family: var(--font-mono);
    letter-spacing: 0.02em;
    cursor: pointer;
    transition: background var(--transition-fast), border-color var(--transition-fast), box-shadow var(--transition-fast);
    width: fit-content;
  }

  .command-action:hover {
    background: rgba(255, 107, 53, 0.18);
    border-color: rgba(255, 107, 53, 0.5);
    box-shadow: 0 0 16px rgba(255, 107, 53, 0.12);
  }

  .lane-list {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .lane-list-label {
    font-size: 9px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wider);
    color: var(--fg-muted);
  }

  .lane-item {
    display: flex;
    flex-direction: column;
    gap: 3px;
    text-align: left;
    padding: var(--space-2) var(--space-3);
    border-radius: var(--radius-sm);
    border: 1px solid var(--border);
    background: var(--bg-surface);
    cursor: pointer;
    transition: border-color var(--transition-normal), transform var(--transition-fast), box-shadow var(--transition-normal);
  }

  .lane-item:hover {
    transform: translateY(-1px);
    border-color: var(--border-focus);
    box-shadow: var(--shadow-sm);
  }

  .lane-head {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }

  .lane-dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    flex-shrink: 0;
    box-shadow: 0 0 4px currentColor;
  }

  .lane-name {
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-secondary);
  }

  .lane-badge {
    margin-left: auto;
    font-size: 9px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--accent);
    padding: 1px 6px;
    border-radius: var(--radius-full);
    border: 1px solid rgba(255, 107, 53, 0.25);
    background: var(--accent-dim);
  }

  .lane-value {
    font-size: 13px;
    font-weight: 600;
    font-family: var(--font-mono);
    color: var(--fg-primary);
  }

  .lane-detail {
    font-size: 10px;
    color: var(--fg-muted);
    font-family: var(--font-mono);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  @media (max-width: 900px) {
    .command-section {
      grid-template-columns: 1fr;
    }
  }

  @media (max-width: 480px) {
    .command-title {
      font-size: clamp(18px, 5vw, 22px);
    }

    .command-action {
      width: 100%;
      justify-content: center;
    }
  }
</style>
