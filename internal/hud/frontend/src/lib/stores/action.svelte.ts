// Action audit store - session-local ring buffer of operator actions.
//
// Records every action initiated through useAction() (Slice A of the HUD UX
// overhaul). Backs AuditDrawer for in-session review. State is mirrored to
// sessionStorage so a navigation/reload preserves the recent history.

export type ActionStatus = 'pending' | 'success' | 'error' | 'rolled_back';

export interface ActionEntry {
  id: string;
  label: string;
  source: string; // panel or component that initiated, e.g. "OverviewPanel:inbox/approve"
  status: ActionStatus;
  startedAt: number;
  endedAt: number | null;
  error: string | null;
  retryable: boolean;
}

const RING_SIZE = 50;
const STORAGE_KEY = 'hud.action.audit.v1';

function loadInitial(): ActionEntry[] {
  if (typeof sessionStorage === 'undefined') return [];
  try {
    const raw = sessionStorage.getItem(STORAGE_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed.slice(0, RING_SIZE);
  } catch {
    return [];
  }
}

class ActionStore {
  entries = $state<ActionEntry[]>(loadInitial());
  drawerOpen = $state(false);

  private nextId = Date.now();

  /** Allocate an id + record the action start. Returns the id for follow-up. */
  start(label: string, source: string, retryable = true): string {
    const id = `${this.nextId++}`;
    const entry: ActionEntry = {
      id,
      label,
      source,
      status: 'pending',
      startedAt: Date.now(),
      endedAt: null,
      error: null,
      retryable,
    };
    this.entries = [entry, ...this.entries].slice(0, RING_SIZE);
    this.persist();
    return id;
  }

  succeed(id: string): void {
    this.update(id, { status: 'success', endedAt: Date.now(), error: null });
  }

  fail(id: string, error: string): void {
    this.update(id, { status: 'error', endedAt: Date.now(), error });
  }

  markRolledBack(id: string): void {
    this.update(id, { status: 'rolled_back', endedAt: Date.now() });
  }

  /** Remove a single entry (used by dismiss). */
  remove(id: string): void {
    this.entries = this.entries.filter((e) => e.id !== id);
    this.persist();
  }

  clear(): void {
    this.entries = [];
    this.persist();
  }

  openDrawer(): void {
    this.drawerOpen = true;
  }

  closeDrawer(): void {
    this.drawerOpen = false;
  }

  toggleDrawer(): void {
    this.drawerOpen = !this.drawerOpen;
  }

  private update(id: string, patch: Partial<ActionEntry>): void {
    let changed = false;
    this.entries = this.entries.map((e) => {
      if (e.id !== id) return e;
      changed = true;
      return { ...e, ...patch };
    });
    if (changed) this.persist();
  }

  private persist(): void {
    if (typeof sessionStorage === 'undefined') return;
    try {
      sessionStorage.setItem(STORAGE_KEY, JSON.stringify(this.entries));
    } catch {
      // sessionStorage may be full or disabled; audit drawer still works in-memory.
    }
  }
}

export const actionStore = new ActionStore();
