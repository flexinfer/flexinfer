<script lang="ts">
  /**
   * SandboxPanel — composition shell for the Sandbox (Labs) view.
   * Picks between SandboxOffline (devbox backend disconnected) and
   * SandboxLive (full controls + projects + activity + rail) per the
   * panel decomposition pattern (`docs/HUD_PANEL_DECOMP.md`).
   *
   * Further decomposition of SandboxLive into per-card sub-components
   * (header / toolbars / projects / activity / rail) is left for a
   * follow-up slice; for B2.4 the goal is to shrink this panel to a
   * composition shell while keeping behavior byte-compatible.
   */
  import { sandboxStore } from '../stores/sandbox.svelte.ts';
  import LabsAccessBar from './shared/LabsAccessBar.svelte';
  import SandboxOffline from './sandbox/SandboxOffline.svelte';
  import SandboxLive from './sandbox/SandboxLive.svelte';

  $effect(() => {
    sandboxStore.startPolling(15000);
    return () => { sandboxStore.stopPolling(); };
  });

  let available = $derived(sandboxStore.available);
</script>

<div class="panel sandbox-panel">
  <LabsAccessBar />
  {#if !available}
    <SandboxOffline />
  {:else}
    <SandboxLive />
  {/if}
</div>

<style>
  .sandbox-panel {
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }
</style>
