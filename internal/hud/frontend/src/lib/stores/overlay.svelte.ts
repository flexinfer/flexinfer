// Overlay store - manages overlay mode state, accordion sections, and activity tracking.
// Activated when the frontend is loaded with ?overlay=1 query parameter.

export interface ActivityEvent {
  type: string;
  agentId: string;
  timestamp: number; // Date.now()
}

const RING_BUFFER_SIZE = 50;
const ACTIVE_WINDOW_MS = 3000; // 3 seconds

class OverlayStore {
  enabled = $state(false);
  expandedSection = $state<string | null>(null);

  // Sub-group collapse state (stores collapsed exceptions; default = expanded)
  collapsedNamespaces = $state<Set<string>>(new Set());
  collapsedSessions = $state<Set<string>>(new Set());

  // Activity tracking — ring buffer of recent agent events for overlay feedback.
  recentEvents = $state<ActivityEvent[]>([]);

  init() {
    this.enabled = new URLSearchParams(location.search).get('overlay') === '1';
  }

  /** Push an activity event into the ring buffer (max 50 entries). */
  pushEvent(type: string, agentId: string) {
    const next = [...this.recentEvents, { type, agentId, timestamp: Date.now() }];
    // Trim to ring buffer size.
    this.recentEvents = next.length > RING_BUFFER_SIZE ? next.slice(-RING_BUFFER_SIZE) : next;
  }

  /** Set of agent IDs with activity in the last 3 seconds (for pulse animation). */
  get activeAgentIds(): Set<string> {
    const cutoff = Date.now() - ACTIVE_WINDOW_MS;
    const ids = new Set<string>();
    for (const e of this.recentEvents) {
      if (e.timestamp >= cutoff) ids.add(e.agentId);
    }
    return ids;
  }

  toggleSection(id: string) {
    this.expandedSection = this.expandedSection === id ? null : id;
  }

  toggleNamespace(ns: string) {
    const next = new Set(this.collapsedNamespaces);
    if (next.has(ns)) next.delete(ns); else next.add(ns);
    this.collapsedNamespaces = next;
  }

  toggleSession(sessionId: string) {
    const next = new Set(this.collapsedSessions);
    if (next.has(sessionId)) next.delete(sessionId); else next.add(sessionId);
    this.collapsedSessions = next;
  }

  isNamespaceExpanded(ns: string): boolean {
    return !this.collapsedNamespaces.has(ns);
  }

  isSessionExpanded(sessionId: string): boolean {
    return !this.collapsedSessions.has(sessionId);
  }

  /** Reset sub-group state so smart defaults re-apply on next open. */
  resetSubGroups() {
    this.collapsedNamespaces = new Set();
    this.collapsedSessions = new Set();
  }
}

export const overlayStore = new OverlayStore();
