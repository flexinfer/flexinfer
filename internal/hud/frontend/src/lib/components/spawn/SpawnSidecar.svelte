<script lang="ts">
  /**
   * SpawnSidecar — right-side advisory cards (Ready Check, Backend
   * Integration, Good Prompt Pattern). Reads spawnStore + labsAuthStore
   * directly; no props.
   */
  import { spawnStore } from '../../stores/spawn.svelte';
  import { labsAuthStore } from '../../stores/labsAuth.svelte.ts';

  let config = $derived(spawnStore.config);
  let hasAdminToken = $derived(labsAuthStore.hasToken);
  // formReady is duplicated from SpawnComposer's local form state — for
  // the sidecar we approximate it from the live config rather than
  // wiring the form through props (the sidecar is purely informational).
  let formReady = $derived(Boolean(config?.configured));
</script>

<aside class="spawn-sidecar">
  <div class="side-card">
    <div class="side-label">Ready Check</div>
    <div class="side-value">
      {#if !hasAdminToken}
        Token required
      {:else if config && !config.configured}
        Backend offline
      {:else if formReady}
        Launch-ready
      {:else}
        Missing context
      {/if}
    </div>
    <div class="side-copy">
      Strong spawns include a target project, an explicit outcome, and a clear timeout window.
    </div>
  </div>

  <div class="side-card">
    <div class="side-label">Backend Integration</div>
    <div class="side-copy">
      {#if config?.projects?.length}
        {config.notes?.project_count ?? config.projects.length} project target{(config.notes?.project_count ?? config.projects.length) === 1 ? '' : 's'} advertised by the backend.
      {:else if config && !config.configured}
        {config.notes?.reason || 'The HUD cannot currently reach a spawn orchestrator.'}
      {:else}
        Waiting for backend capability data.
      {/if}
    </div>
    <div class="tip-list">
      <div class="tip-item">Backend defaults hydrate the form when configuration loads.</div>
      <div class="tip-item">Live cost and token telemetry stream in over SSE instead of waiting for the next poll.</div>
      {#if config?.notes?.active_spawn_count}
        <div class="tip-item">{config.notes.active_spawn_count} active spawn{config.notes.active_spawn_count === 1 ? '' : 's'} currently tracked by the backend.</div>
      {/if}
      {#if config?.notes?.telemetry_requires_auth}
        <div class="tip-item">Protected telemetry and follow-up controls stay behind the Labs admin token.</div>
      {/if}
    </div>
  </div>

  <div class="side-card">
    <div class="side-label">Good Prompt Pattern</div>
    <div class="tip-list">
      <div class="tip-item">Name the subsystem or file area the agent should inspect first.</div>
      <div class="tip-item">State the expected result, not just the problem statement.</div>
      <div class="tip-item">Mention constraints like tests, branches, or services to avoid touching.</div>
    </div>
  </div>
</aside>

<style>
  .spawn-sidecar {
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
  }

  .side-card {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    padding: var(--space-4);
    border-radius: var(--radius-xl);
    border: 1px solid var(--border);
    background:
      linear-gradient(180deg, rgba(255, 255, 255, 0.018), transparent 42%),
      var(--bg-secondary);
    box-shadow: var(--shadow-xs);
  }

  .side-label {
    font-size: var(--text-xs);
    font-weight: 700;
    letter-spacing: var(--tracking-wide);
    text-transform: uppercase;
    color: var(--fg-muted);
  }

  .side-value {
    font-size: clamp(18px, 2vw, 24px);
    font-weight: 700;
    color: var(--fg-primary);
  }

  .side-copy {
    font-size: var(--text-sm);
    color: var(--fg-secondary);
    line-height: 1.6;
  }

  .tip-list {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .tip-item {
    padding-left: var(--space-4);
    position: relative;
    font-size: var(--text-sm);
    color: var(--fg-secondary);
    line-height: 1.6;
  }

  .tip-item::before {
    content: '';
    position: absolute;
    top: 9px;
    left: 0;
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--accent);
    box-shadow: 0 0 8px var(--glow-accent);
  }
</style>
