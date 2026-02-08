// Overlay store - manages overlay mode state and accordion sections.
// Activated when the frontend is loaded with ?overlay=1 query parameter.

class OverlayStore {
  enabled = $state(false);
  expandedSection = $state<string | null>(null);

  init() {
    this.enabled = new URLSearchParams(location.search).get('overlay') === '1';
  }

  toggleSection(id: string) {
    this.expandedSection = this.expandedSection === id ? null : id;
  }
}

export const overlayStore = new OverlayStore();
