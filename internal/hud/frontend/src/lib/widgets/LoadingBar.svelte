<script>
  let { visible = true } = $props();
</script>

{#if visible}
  <div class="loading-bar">
    <div class="loading-bar-inner"></div>
  </div>
{/if}

<style>
  .loading-bar {
    height: 2px;
    background: var(--bg-tertiary);
    overflow: hidden;
    border-radius: 1px;
    flex-shrink: 0;
  }
  .loading-bar-inner {
    height: 100%;
    width: 40%;
    background: var(--info);
    animation: loadingSlide 1.2s ease-in-out infinite;
  }
  @keyframes loadingSlide {
    0%   { transform: translateX(-100%); }
    100% { transform: translateX(350%); }
  }
</style>
