<script lang="ts">
  /**
   * SandboxOffline — landing card shown when the devbox backend isn't
   * connected. Reads summary from sandboxStore directly.
   */
  import { sandboxStore } from '../../stores/sandbox.svelte.ts';

  let summary = $derived(sandboxStore.summary);
  let offlineReason = $derived(summary?.reason ?? 'mcp-devbox is not running or not connected to the daemon.');
  let offlineHint = $derived(summary?.hint ?? 'Start the devbox service, then return to Labs to provision or inspect sandboxes.');
  let offlineCommand = $derived(summary?.start_command ?? 'loom start devbox');
</script>

<div class="unavailable-shell">
  <section class="unavailable-card">
    <div class="unavailable-eyebrow">Labs / Sandbox</div>
    <div class="unavailable-head">
      <div class="unavailable-icon">⬢</div>
      <div>
        <div class="unavailable-title">Devbox is offline</div>
        <div class="unavailable-hint">{offlineReason}</div>
      </div>
    </div>
    <div class="offline-command">
      <span class="offline-command-label">Start command</span>
      <code>{offlineCommand}</code>
    </div>
    <div class="unavailable-copy">{offlineHint}</div>
  </section>

  <aside class="unavailable-sidecard">
    <div class="section-title">Why it matters</div>
    <div class="offline-points">
      <div class="offline-point">Sandbox availability controls `devbox_build`, `devbox_exec`, and the HUD's project activity feed.</div>
      <div class="offline-point">When the backend reconnects, project inventory and recent build or exec events repopulate automatically.</div>
    </div>
  </aside>
</div>

<style>
  .unavailable-shell {
    display: grid;
    grid-template-columns: minmax(0, 1.4fr) minmax(260px, 0.8fr);
    gap: var(--space-4);
    align-items: start;
  }
  .unavailable-card,
  .unavailable-sidecard {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--radius-xl);
    padding: var(--space-4);
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    position: relative;
  }
  .unavailable-card::before,
  .unavailable-sidecard::before {
    content: '';
    position: absolute;
    inset: 0;
    border-radius: inherit;
    background: var(--surface-highlight);
    pointer-events: none;
  }
  .unavailable-eyebrow {
    font-size: var(--text-xs);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--accent);
    font-weight: 700;
  }
  .unavailable-head {
    display: flex;
    align-items: center;
    gap: var(--space-3);
  }
  .unavailable-icon {
    font-size: 28px;
    color: var(--fg-muted);
  }
  .unavailable-title {
    font-size: 18px;
    font-weight: 700;
    color: var(--fg-primary);
  }
  .unavailable-hint {
    font-size: var(--text-sm);
    color: var(--fg-secondary);
    line-height: 1.5;
  }
  .offline-command {
    display: flex;
    flex-direction: column;
    gap: 4px;
    padding: var(--space-3);
    background: var(--bg-primary);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-md);
  }
  .offline-command-label {
    font-size: var(--text-xs);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-muted);
  }
  .offline-command code {
    font-family: var(--font-mono);
    font-size: var(--text-sm);
    color: var(--fg-primary);
  }
  .unavailable-copy {
    font-size: var(--text-sm);
    color: var(--fg-secondary);
    line-height: 1.6;
  }
  .offline-points {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }
  .offline-point {
    font-size: var(--text-sm);
    color: var(--fg-secondary);
    line-height: 1.5;
    padding-left: var(--space-3);
    position: relative;
  }
  .offline-point::before {
    content: '';
    position: absolute;
    top: 9px;
    left: 0;
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--accent);
  }
  @media (max-width: 980px) {
    .unavailable-shell { grid-template-columns: 1fr; }
  }
</style>
