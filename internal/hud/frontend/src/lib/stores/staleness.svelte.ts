/**
 * Staleness tracking — Slice B3 of the HUD UX overhaul.
 *
 * Pattern: each store records a `lastUpdated` timestamp every time it applies
 * a snapshot (whether from SSE push or polling fallback). The `clockStore`
 * ticks every 5s so derived `isStale` getters become reactive without each
 * store needing its own timer. The `stalenessStore` aggregator lets the
 * `ConnectionBanner` surface a single "stale" pill whenever any registered
 * store crosses its `staleAfter` threshold — catches silent SSE failures
 * (connection up, no events flowing) that the existing connection-state
 * banner cannot see.
 *
 * Stores register themselves at module-load time via `stalenessStore.register`,
 * which keeps the dependency graph one-way (stores -> staleness) and avoids
 * circular imports.
 */

class ClockStore {
  /** Current wall-clock time in ms. Ticks every 5s so `isStale` derivations re-evaluate. */
  now = $state(Date.now());

  private timer: ReturnType<typeof setInterval> | null = null;
  private static readonly TICK_MS = 5000;

  start(): void {
    if (this.timer) return;
    this.timer = setInterval(() => {
      this.now = Date.now();
    }, ClockStore.TICK_MS);
  }

  stop(): void {
    if (this.timer) {
      clearInterval(this.timer);
      this.timer = null;
    }
  }
}

export const clockStore = new ClockStore();
clockStore.start();

class StalenessStore {
  private trackers = new Map<string, () => boolean>();

  /** Register a store's `isStale` getter under a stable name. Idempotent. */
  register(name: string, isStaleFn: () => boolean): void {
    this.trackers.set(name, isStaleFn);
  }

  /** Remove a registration (e.g., during hot reload or teardown). */
  unregister(name: string): void {
    this.trackers.delete(name);
  }

  /** Names of all registered stores currently reporting stale. */
  get staleStores(): string[] {
    const stale: string[] = [];
    for (const [name, isStale] of this.trackers) {
      if (isStale()) stale.push(name);
    }
    return stale;
  }

  /** True when at least one registered store is stale. */
  get anyStale(): boolean {
    for (const [, isStale] of this.trackers) {
      if (isStale()) return true;
    }
    return false;
  }
}

export const stalenessStore = new StalenessStore();

/**
 * Helper for stores that maintain `lastUpdated: Date | null` and a
 * `staleAfter` ms threshold. Pure function so each store can call it
 * from a getter without coupling the store class to this module.
 */
export function isStaleFromTimestamp(lastUpdated: Date | null, staleAfterMs: number): boolean {
  if (!lastUpdated) return false;
  return clockStore.now - lastUpdated.getTime() > staleAfterMs;
}
