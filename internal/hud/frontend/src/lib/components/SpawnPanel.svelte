<script lang="ts">
  /**
   * SpawnPanel — composition shell for the Sandbox → Spawn (Labs) view.
   * The launch form, sidecar, list, and stop dialog live in
   * `lib/components/spawn/*`; pure helpers + filter logic live in
   * `lib/utils/spawnHelpers.ts`. Filter state moved into spawnStore per
   * the Slice B1 decomposition contract (`docs/HUD_PANEL_DECOMP.md`).
   */
  import { spawnStore } from '../stores/spawn.svelte';
  import SpawnComposer from './spawn/SpawnComposer.svelte';
  import SpawnSidecar from './spawn/SpawnSidecar.svelte';
  import SpawnList from './spawn/SpawnList.svelte';

  $effect(() => {
    spawnStore.startPolling(60000);
    return () => { spawnStore.stopPolling(); };
  });
</script>

<div class="panel spawn-panel">
  <section class="spawn-layout">
    <SpawnComposer />
    <SpawnSidecar />
  </section>

  <SpawnList />
</div>

<style>
  .spawn-panel {
    display: flex;
    flex-direction: column;
    gap: var(--space-5);
  }

  .spawn-layout {
    display: grid;
    grid-template-columns: minmax(0, 1.65fr) minmax(280px, 0.9fr);
    gap: var(--space-4);
    align-items: start;
  }

  @media (max-width: 980px) {
    .spawn-layout {
      grid-template-columns: 1fr;
    }
  }
</style>
