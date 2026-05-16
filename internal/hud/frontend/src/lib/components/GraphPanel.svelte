<script>
  /**
   * GraphPanel — composition shell for the Activity → Graph view.
   * Polling + the entire UI live in `graph/GraphFullView.svelte`; the
   * panel is just the shell per `docs/HUD_PANEL_DECOMP.md`.
   *
   * Further per-card decomposition of GraphFullView (stats column,
   * explorer with list, viz, modals, drawer) is left for a follow-up
   * slice; B2.5's goal is to shrink this panel to a composition shell
   * while keeping behavior byte-compatible.
   */
  import { graphStore } from '../stores/graph.svelte.ts';
  import GraphFullView from './graph/GraphFullView.svelte';

  $effect(() => {
    graphStore.startPolling(10000);
    return () => { graphStore.stopPolling(); };
  });
</script>

<GraphFullView />
