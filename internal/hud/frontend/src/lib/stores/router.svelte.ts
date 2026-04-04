// Hash-based SPA router for the HUD.
// Supports grouped views with legacy hash compatibility.
// Top-level labels are intentionally operator-oriented while IDs stay stable.

export interface RouteState {
  view: string;
  subView: string;
  detail: string | null;
}

// ---- View definitions ----

export interface ViewDef {
  id: string;
  label: string;
  icon: string;
  key: string;
  subViews: Array<{ id: string; label: string; key: string }>;
  default: string;
}

export const views: ViewDef[] = [
  {
    id: 'agents',
    label: 'Operations',
    icon: '\u25C8',
    key: '1',
    default: 'fleet',
    subViews: [
      { id: 'fleet',     label: 'Fleet',     key: 'a' },
      { id: 'dispatch',  label: 'Dispatch',  key: 'b' },
      { id: 'presence',  label: 'Presence',  key: 'c' },
      { id: 'topology',  label: 'Topology',  key: 'd' },
      { id: 'lifecycle', label: 'Lifecycle',  key: 'e' },
    ],
  },
  {
    id: 'infra',
    label: 'Infrastructure',
    icon: '\u2665',
    key: '2',
    default: 'servers',
    subViews: [
      { id: 'servers', label: 'Servers', key: 'a' },
      { id: 'catalog', label: 'Catalog', key: 'b' },
      { id: 'weaver', label: 'Weaver', key: 'c' },
    ],
  },
  {
    id: 'tasks',
    label: 'Work',
    icon: '\u2611',
    key: '3',
    default: 'tasks',
    subViews: [
      { id: 'tasks',     label: 'Tasks',     key: 'a' },
      { id: 'workflows', label: 'Workflows', key: 'b' },
    ],
  },
  {
    id: 'knowledge',
    label: 'Context',
    icon: '\u29BE',
    key: '4',
    default: 'feed',
    subViews: [
      { id: 'feed',      label: 'Feed',      key: 'a' },
      { id: 'memory',    label: 'Memory',    key: 'b' },
      { id: 'graph',     label: 'Graph',     key: 'c' },
      { id: 'reasoning', label: 'Reasoning', key: 'd' },
    ],
  },
  {
    id: 'activity',
    label: 'Activity',
    icon: '\u2261',
    key: '5',
    default: 'timeline',
    subViews: [
      { id: 'timeline', label: 'Timeline', key: 'a' },
      { id: 'stream',   label: 'Stream',   key: 'b' },
    ],
  },
  {
    id: 'sandbox',
    label: 'Labs',
    icon: '\u2B22',
    key: '6',
    default: 'sandbox',
    subViews: [
      { id: 'sandbox', label: 'Sandbox', key: 'a' },
      { id: 'spawn',   label: 'Spawn',   key: 'b' },
    ],
  },
];

// Overview is standalone (no sub-views)
export const overviewId = 'overview';

// ---- Legacy hash redirect map (old flat panel id -> new view/subView) ----

const legacyRedirects: Record<string, { view: string; subView: string }> = {};
for (const v of views) {
  for (const sv of v.subViews) {
    legacyRedirects[sv.id] = { view: v.id, subView: sv.id };
  }
}
// Additional aliases
legacyRedirects['fleet'] = { view: 'agents', subView: 'fleet' };
legacyRedirects['dispatch'] = { view: 'agents', subView: 'dispatch' };
legacyRedirects['presence'] = { view: 'agents', subView: 'presence' };
legacyRedirects['topology'] = { view: 'agents', subView: 'topology' };
legacyRedirects['lifecycle'] = { view: 'agents', subView: 'lifecycle' };
legacyRedirects['servers'] = { view: 'infra', subView: 'servers' };
legacyRedirects['catalog'] = { view: 'infra', subView: 'catalog' };
legacyRedirects['workflows'] = { view: 'tasks', subView: 'workflows' };
legacyRedirects['feed'] = { view: 'knowledge', subView: 'feed' };
legacyRedirects['memory'] = { view: 'knowledge', subView: 'memory' };
legacyRedirects['graph'] = { view: 'knowledge', subView: 'graph' };
legacyRedirects['reasoning'] = { view: 'knowledge', subView: 'reasoning' };
legacyRedirects['timeline'] = { view: 'activity', subView: 'timeline' };
legacyRedirects['stream'] = { view: 'activity', subView: 'stream' };

const DEFAULT_VIEW = 'agents';
const DEFAULT_SUB = 'fleet';

// ---- Hash parsing ----

function findViewDef(id: string): ViewDef | undefined {
  return views.find(v => v.id === id);
}

function parseHash(): RouteState {
  const raw = globalThis.location?.hash?.replace(/^#\/?/, '') ?? '';
  if (!raw || raw === overviewId) {
    return { view: raw === overviewId ? overviewId : DEFAULT_VIEW, subView: DEFAULT_SUB, detail: null };
  }

  const parts = raw.split('/');

  // Check for legacy single-segment hash (e.g., #fleet, #tasks)
  if (parts.length === 1) {
    const legacy = legacyRedirects[parts[0]];
    if (legacy) {
      return { view: legacy.view, subView: legacy.subView, detail: null };
    }
    // Could be a view id (e.g., #agents)
    const vd = findViewDef(parts[0]);
    if (vd) {
      return { view: vd.id, subView: vd.default, detail: null };
    }
    return { view: DEFAULT_VIEW, subView: DEFAULT_SUB, detail: null };
  }

  // Two or three segments: view/subView or view/subView/detail
  const viewId = parts[0];
  const subViewId = parts[1];
  const detailId = parts[2] || null;

  const vd = findViewDef(viewId);
  if (!vd) {
    // Try legacy redirect on first segment
    const legacy = legacyRedirects[viewId];
    if (legacy) {
      return { view: legacy.view, subView: legacy.subView, detail: subViewId || null };
    }
    return { view: DEFAULT_VIEW, subView: DEFAULT_SUB, detail: null };
  }

  // Validate subView belongs to this view
  const validSub = vd.subViews.some(sv => sv.id === subViewId);
  return {
    view: vd.id,
    subView: validSub ? subViewId : vd.default,
    detail: detailId,
  };
}

// ---- Router class ----

class Router {
  view = $state(DEFAULT_VIEW);
  subView = $state(DEFAULT_SUB);
  detail = $state<string | null>(null);

  // Legacy alias: panels that read router.panel still work
  get panel(): string {
    return this.subView;
  }

  private listening = false;

  /** Initialize from current URL hash and start listening. */
  init(): void {
    const state = parseHash();
    this.view = state.view;
    this.subView = state.subView;
    this.detail = state.detail;

    // Rewrite legacy hashes to new format
    this._syncHash();

    if (!this.listening && typeof globalThis.addEventListener === 'function') {
      globalThis.addEventListener('hashchange', () => {
        const s = parseHash();
        this.view = s.view;
        this.subView = s.subView;
        this.detail = s.detail;
      });
      this.listening = true;
    }
  }

  /** Navigate to a view + subView, optionally with a detail ID. */
  navigate(view: string, subView?: string, detail?: string | null): void {
    // Handle legacy single-arg calls: navigate('fleet') -> navigate('agents', 'fleet')
    const legacy = legacyRedirects[view];
    if (!findViewDef(view) && view !== overviewId && legacy) {
      this.view = legacy.view;
      this.subView = subView ?? legacy.subView;
    } else if (view === overviewId) {
      this.view = overviewId;
      this.subView = '';
    } else {
      const vd = findViewDef(view);
      this.view = view;
      this.subView = subView ?? vd?.default ?? '';
    }
    this.detail = detail ?? null;
    this._syncHash();
  }

  /** Switch sub-view within the current view. */
  navigateSub(subView: string, detail?: string | null): void {
    this.subView = subView;
    this.detail = detail ?? null;
    this._syncHash();
  }

  /** Navigate to detail within current view/subView. */
  navigateDetail(detail: string | null): void {
    this.detail = detail;
    this._syncHash();
  }

  /** Navigate back: clear detail first, then sub-view. */
  back(): void {
    if (this.detail) {
      this.detail = null;
      this._syncHash();
    }
  }

  /** Get the ViewDef for the current view. */
  get currentViewDef(): ViewDef | undefined {
    return findViewDef(this.view);
  }

  private _syncHash(): void {
    let hash: string;
    if (this.view === overviewId) {
      hash = `#${overviewId}`;
    } else if (this.detail) {
      hash = `#${this.view}/${this.subView}/${this.detail}`;
    } else {
      hash = `#${this.view}/${this.subView}`;
    }
    if (globalThis.location && globalThis.location.hash !== hash) {
      globalThis.history.replaceState(null, '', hash);
    }
  }
}

export const router = new Router();
