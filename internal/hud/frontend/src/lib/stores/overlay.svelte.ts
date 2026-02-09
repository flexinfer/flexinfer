// Overlay store - manages overlay mode state and accordion sections.
// Activated when the frontend is loaded with ?overlay=1 query parameter.

class OverlayStore {
  enabled = $state(false);
  expandedSection = $state<string | null>(null);

  // Sub-group collapse state (stores collapsed exceptions; default = expanded)
  collapsedNamespaces = $state<Set<string>>(new Set());
  collapsedSessions = $state<Set<string>>(new Set());

  init() {
    this.enabled = new URLSearchParams(location.search).get('overlay') === '1';
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
