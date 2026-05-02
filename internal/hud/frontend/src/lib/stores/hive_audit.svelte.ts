// Hive Audit store — adversarial-audit findings from the
// loom-hive-operator, proxied through /api/hive/audit/* by the HUD's
// domain/hive package. Polls the list endpoint at 15s by default so the
// cadence agrees with the rest of the Hive panels.
//
// Empty/disabled state: 503 from the proxy means the HUD has no operator
// URL set; surface that as a calm empty state rather than a fetch error.

// AuditFinding mirrors the Go shape `pkg/hive/store.AuditFinding`. Field
// names match the JSON the operator returns; the store keeps that on
// purpose so a panel can render directly without an extra mapping
// layer. Findings + AuditorPool are bag-of-fields maps because the
// canonical store persists them as JSON.
export interface AuditFinding {
  ID: number;
  SubjectKind: 'council_artifact' | 'pipeline_merge';
  SubjectID: string;
  Severity: 'info' | 'warn' | 'critical';
  RubricID: string;
  SurvivalScore: number;
  Findings: AuditFindingItem[];
  AuditorPool: AuditPoolEntry[];
  CostUSD: number;
  CreatedAt: string;
}

export interface AuditFindingItem {
  id?: string;
  title?: string;
  severity?: string;
  detail?: string;
}

export interface AuditPoolEntry {
  backend?: string;
  model?: string;
  role?: string;
}

export interface SeverityCounts {
  info: number;
  warn: number;
  critical: number;
}

class HiveAuditStore {
  state = $state<AuditFinding[]>([]);
  details = $state<Record<number, AuditFinding>>({});

  loading = $state(false);
  error = $state<string | null>(null);
  disabled = $state(false);
  lastUpdated = $state<Date | null>(null);

  private pollTimer: ReturnType<typeof setInterval> | null = null;

  async refresh(): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const list = await this.getJSON<AuditFinding[]>('/api/hive/audit/findings?limit=200');
      this.state = list ?? [];
      this.lastUpdated = new Date();
      this.disabled = false;
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      if (msg.includes('503') || msg.toLowerCase().includes('not configured')) {
        this.disabled = true;
        this.error = null;
        this.state = [];
      } else {
        this.disabled = false;
        this.error = msg;
      }
    } finally {
      this.loading = false;
    }
  }

  async fetchDetail(id: number): Promise<AuditFinding | null> {
    if (!Number.isFinite(id) || id <= 0) return null;
    try {
      const detail = await this.getJSON<AuditFinding>(
        `/api/hive/audit/findings/${id}`,
      );
      if (detail) {
        this.details = { ...this.details, [id]: detail };
      }
      return detail;
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      if (!msg.includes('404')) {
        this.error = msg;
      }
      return null;
    }
  }

  startPolling(intervalMs = 15000): void {
    this.stopPolling();
    void this.refresh();
    this.pollTimer = setInterval(() => void this.refresh(), intervalMs);
  }

  stopPolling(): void {
    if (this.pollTimer) {
      clearInterval(this.pollTimer);
      this.pollTimer = null;
    }
  }

  // severityCounts returns the active-window severity histogram. Used
  // by the panel header to show a quick glance at how the audit pool
  // is scoring current activity.
  severityCounts = $derived.by<SeverityCounts>(() => {
    const counts: SeverityCounts = { info: 0, warn: 0, critical: 0 };
    for (const f of this.state) {
      if (f.Severity === 'info' || f.Severity === 'warn' || f.Severity === 'critical') {
        counts[f.Severity]++;
      }
    }
    return counts;
  });

  // averageSurvival returns the mean survival score across the active
  // window. The panel renders this as a single headline number.
  averageSurvival = $derived.by<number | null>(() => {
    if (this.state.length === 0) return null;
    let sum = 0;
    for (const f of this.state) sum += f.SurvivalScore;
    return sum / this.state.length;
  });

  // topCritical returns the lowest-survival findings (regardless of
  // declared severity) — these are the rows triage should look at first.
  topCritical = $derived.by<AuditFinding[]>(() => {
    const sorted = [...this.state].sort((a, b) => a.SurvivalScore - b.SurvivalScore);
    return sorted.slice(0, 5);
  });

  private async getJSON<T>(path: string): Promise<T | null> {
    const res = await globalThis.fetch(path);
    if (res.status === 503) {
      throw new Error(`hive proxy: 503 (operator not configured)`);
    }
    if (res.status === 404) {
      return null;
    }
    if (!res.ok) {
      throw new Error(`${path}: ${res.status}`);
    }
    const text = await res.text();
    if (!text) return null;
    return JSON.parse(text) as T;
  }
}

export const hiveAuditStore = new HiveAuditStore();
