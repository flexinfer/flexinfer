// Hash-based SPA router for the HUD.
// Supports two-level routing: panel + optional detail ID.
// Example hashes: #fleet, #tasks, #sessions/abc123, #graph/entity-42

export interface RouteState {
  panel: string;
  detail: string | null;
}

const DEFAULT_PANEL = 'fleet';

function parseHash(): RouteState {
  const raw = globalThis.location?.hash?.replace(/^#\/?/, '') ?? '';
  if (!raw) return { panel: DEFAULT_PANEL, detail: null };

  const parts = raw.split('/');
  return {
    panel: parts[0] || DEFAULT_PANEL,
    detail: parts[1] || null,
  };
}

class Router {
  panel = $state(DEFAULT_PANEL);
  detail = $state<string | null>(null);

  private listening = false;

  /** Initialize from current URL hash and start listening. */
  init(): void {
    const state = parseHash();
    this.panel = state.panel;
    this.detail = state.detail;

    if (!this.listening && typeof globalThis.addEventListener === 'function') {
      globalThis.addEventListener('hashchange', () => {
        const s = parseHash();
        this.panel = s.panel;
        this.detail = s.detail;
      });
      this.listening = true;
    }
  }

  /** Navigate to a panel, optionally with a detail ID. Updates the URL hash. */
  navigate(panel: string, detail?: string | null): void {
    this.panel = panel;
    this.detail = detail ?? null;

    const hash = detail ? `#${panel}/${detail}` : `#${panel}`;
    if (globalThis.location && globalThis.location.hash !== hash) {
      globalThis.history.replaceState(null, '', hash);
    }
  }

  /** Navigate back to the panel overview (clears detail). */
  back(): void {
    this.navigate(this.panel);
  }
}

export const router = new Router();
