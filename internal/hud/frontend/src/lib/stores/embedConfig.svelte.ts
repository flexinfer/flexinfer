/**
 * Embed/subset config — read once at boot from `/api/hud/config`. The
 * backend reports which top-level views and sub-views the operator
 * subset allows; the frontend filters its nav and guards `router.navigate`
 * against the same allowlist. See Slice B5 of the HUD UX overhaul.
 *
 * Default (no subset, "full" mode) leaves every view reachable.
 */

export type EmbedSubset = 'full' | 'operator';

export interface EmbedConfig {
  subset: EmbedSubset;
  /** Top-level view ids allowed by the subset. Empty/null ⇒ no restriction. */
  allowedViews: string[] | null;
  /** Per-view sub-view allowlists. Missing keys ⇒ no sub-view restriction. */
  allowedSubViews: Record<string, string[]>;
}

class EmbedConfigStore {
  subset = $state<EmbedSubset>('full');
  allowedViews = $state<string[] | null>(null);
  allowedSubViews = $state<Record<string, string[]>>({});
  loaded = $state(false);

  /**
   * isViewAllowed returns true if the given top-level view id is reachable.
   * Returns true unconditionally before the config has loaded so the initial
   * render doesn't flash empty nav on slow networks.
   */
  isViewAllowed(viewId: string): boolean {
    if (!this.loaded) return true;
    if (!this.allowedViews) return true;
    return this.allowedViews.includes(viewId);
  }

  /**
   * isSubViewAllowed returns true if the given sub-view id is reachable
   * under the parent view. Returns true if the parent view itself isn't
   * restricted, or if the parent allows sub-views without an explicit list.
   */
  isSubViewAllowed(parentViewId: string, subViewId: string): boolean {
    if (!this.loaded) return true;
    const list = this.allowedSubViews[parentViewId];
    if (!list || list.length === 0) return true;
    return list.includes(subViewId);
  }

  async load(): Promise<void> {
    try {
      const res = await fetch('/api/hud/config', { cache: 'no-store' });
      if (!res.ok) {
        this.loaded = true;
        return;
      }
      const data = await res.json();
      const subset = (data.subset === 'operator' ? 'operator' : 'full') as EmbedSubset;
      this.subset = subset;
      this.allowedViews = Array.isArray(data.allowed_views) ? data.allowed_views : null;
      this.allowedSubViews = (data.allowed_sub_views && typeof data.allowed_sub_views === 'object')
        ? data.allowed_sub_views
        : {};
    } catch {
      // Network failure: leave the default ("full") in place. The HUD
      // stays fully navigable rather than collapsing the nav on a flaky
      // boot fetch.
    } finally {
      this.loaded = true;
    }
  }
}

export const embedConfig = new EmbedConfigStore();
