// Hive store — backlog / pipeline runs / council runs / eval scores from
// the in-cluster loom-hive-operator, proxied through /api/hive/* by the
// HUD's domain/hive package. Each panel owns a slice of this store and
// polls the corresponding read endpoint at 15s.
//
// Empty/disabled state: when the proxy returns 503 ("operator not
// configured") we surface that via the `disabled` flag so panels can
// render a clear "Hive disabled" empty-state instead of a fetch error.

export interface BacklogItem {
  ID: string;
  Title: string;
  State: string;
  Priority: string;
  Labels?: string[];
  CreatedBy?: string;
  CreatedAt?: string;
  UpdatedAt?: string;
}

export interface PipelineRun {
  ID: string;
  BacklogID: string;
  Template: string;
  State: string;
  Attempts: number;
  StartedAt?: string;
  EndedAt?: string;
  // Phase 6 (bounded recursion): top-level runs have ParentRunID == null
  // and Depth == 0. Subruns created via hive_pipeline_subrun_create
  // carry their parent's ID and depth+1.
  ParentRunID?: string | null;
  Depth?: number;
}

export interface CouncilRun {
  ID: string;
  Trigger: string;
  Outcome: string;
  StartedAt?: string;
  EndedAt?: string;
  CostUSD?: number;
}

// CouncilDebateRound mirrors the Go store.CouncilDebateRound shape.
// Used by the Council panel's "Debate Rounds" expander (Phase 5 slice
// 5.3) to render the per-round transcript persisted by slice 5.2.
export interface CouncilDebateRound {
  ID: number;
  CouncilRunID: string;
  RoundIndex: number;
  // editor_proposes | reviewer_critiques | moderator_decision | editor_revises
  Role: string;
  CostUSD: number;
  Summary?: string;
  ArtifactDeltas?: Array<{ path?: string; line_range?: string; action?: string }>;
  CreatedAt?: string;
}

// DebateLoadState tracks the lazy fetch lifecycle per council run.
// Stored in the hiveStore so the panel can render a spinner / error
// / cached transcript without re-fetching on every poll tick.
type DebateLoadState =
  | { status: 'idle' }
  | { status: 'loading' }
  | { status: 'loaded'; rounds: CouncilDebateRound[] }
  | { status: 'error'; message: string };

export interface EvalScore {
  ID: string;
  Loop: string;        // "A" | "B" | "C"
  Subject: string;     // run id, merge sha, etc.
  Score: number;       // 0..1
  Notes?: string;
  CreatedAt?: string;
}

export interface PolicyView {
  enabled: boolean;
  version: number;
  raw?: unknown;
}

// PolicyProposal mirrors the operator's proposals row. Field casing is
// PascalCase because the proposals handler relies on Go's default JSON
// marshalling (no explicit json: tags). Phase 7 slice 7.1/7.2 own the
// emission + apply/reject endpoints; this UI consumes them read-only
// plus the two POST mutations below.
export interface PolicyProposal {
  ID: number;
  ProposalDate: string; // YYYY-MM-DD
  Kind: 'relax' | 'tighten' | 'rotate_ensemble';
  Target: string;
  Diff: string;
  Rationale: string;
  State: 'pending' | 'applied_human' | 'applied_auto' | 'rejected' | 'reverted';
  AppliedAt?: string;
  RevertDeadline?: string;
  CreatedAt: string;
}

// CostEstimate mirrors the slice 7.3 /api/hive/cost-preview response.
// Field casing is snake_case because that handler sets explicit
// json:"…" tags. Confidence + sample_size let the UI render a band
// pill (low/med/high) so users can read past-data quality at a glance.
export interface CostEstimate {
  backlog_id: string;
  path_class: string;
  median_historical_usd: number;
  sidecar_slice_count: number;
  sidecar_overhead_usd: number;
  recursion_overhead_usd: number;
  estimate_usd: number;
  ensemble_cap_usd: number;
  capped_by_policy: boolean;
  confidence: 'low' | 'medium' | 'high';
  sample_size: number;
  source: string; // "estimator/v1"
}

// HiveKPISnapshot mirrors the operator's `kpi_snapshots.metrics_json`
// rollup. Fields are optional because the recorder only emits keys it has
// data for; missing keys render as "—" placeholders. Field names here are
// the contract the (future) snapshot recorder must honor.
export interface HiveKPISnapshot {
  snapshot_at?: string;
  window_seconds?: number;
  metrics?: {
    cost_per_merged_change_usd?: number;
    slice_to_merge_p50_seconds?: number;
    gate_pass_rate?: number;        // 0..1
    auto_merge_rate?: number;       // 0..1
    regression_rate?: number;       // 0..1
    council_roi?: number;           // merged-changes-per-council-USD
  };
}

// Operator returns snapshots with PascalCase fields (Go struct json tags
// follow Go naming). Accept both casings so a recorder that emits
// snake_case `metrics_json` payloads also works.
interface RawKPISnapshot {
  ID?: number;
  SnapshotAt?: string;
  WindowSeconds?: number;
  Metrics?: HiveKPISnapshot['metrics'];
  snapshot_at?: string;
  window_seconds?: number;
  metrics?: HiveKPISnapshot['metrics'];
}

const KPI_HISTORY_MAX = 24;

class HiveStore {
  // Per-panel data
  backlog = $state<BacklogItem[]>([]);
  pipelineRuns = $state<PipelineRun[]>([]);
  councilRuns = $state<CouncilRun[]>([]);
  evalScores = $state<EvalScore[]>([]);
  policy = $state<PolicyView | null>(null);

  // KPI snapshot for the rolling 1d window plus a small in-memory history
  // for sparkline trends. The history is only de-duped on snapshot_at so
  // repeated polls of the same snapshot don't pad the trend.
  kpis = $state<HiveKPISnapshot | null>(null);
  kpisHistory = $state<HiveKPISnapshot[]>([]);

  // Per-run debate transcripts, keyed by CouncilRun.ID. Populated
  // lazily by loadDebate() so the council list itself stays cheap.
  // Phase 5 slice 5.3 — feeds the CouncilPanel's "Debate Rounds"
  // expander.
  debateByRun = $state<Record<string, DebateLoadState>>({});

  // Pending adaptive policy proposals (Phase 7 slice 7.1/7.2). Refreshed
  // alongside the rest of fetchAll so the card stays in sync with the
  // 15s poll cadence used elsewhere.
  policyProposals = $state<PolicyProposal[]>([]);

  // Cost previews keyed by backlog_id (Phase 7 slice 7.3). Lazily
  // populated by BacklogPanel rows; never auto-polled because the
  // estimate is stable for a given backlog item until policy changes.
  costPreviews = $state<Record<string, CostEstimate>>({});

  // Connection state
  loading = $state(false);
  error = $state<string | null>(null);
  disabled = $state(false); // operator URL unset → 503 from proxy
  lastUpdated = $state<Date | null>(null);

  private pollTimer: ReturnType<typeof setInterval> | null = null;

  get pipelinesByState(): Record<string, number> {
    const out: Record<string, number> = {};
    for (const r of this.pipelineRuns) {
      out[r.State] = (out[r.State] ?? 0) + 1;
    }
    return out;
  }

  get backlogByState(): Record<string, number> {
    const out: Record<string, number> = {};
    for (const i of this.backlog) {
      out[i.State] = (out[i.State] ?? 0) + 1;
    }
    return out;
  }

  async fetchAll(): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const [policy, backlog, pipelines, council, scores, kpis] = await Promise.all([
        this.getJSON<PolicyView>('/api/hive/policy'),
        this.getJSON<BacklogItem[]>('/api/hive/backlog'),
        this.getJSON<PipelineRun[]>('/api/hive/pipeline/runs'),
        this.getJSON<CouncilRun[]>('/api/hive/council/runs'),
        this.getJSON<EvalScore[]>('/api/hive/eval/scores'),
        // KPI endpoint returns 404 until the snapshot recorder ships.
        // Tolerate that by passing { tolerate404: true }; null is fine.
        this.getJSON<RawKPISnapshot>('/api/hive/kpis?window=1d', { tolerate404: true }),
      ]);
      this.policy = policy;
      this.backlog = backlog ?? [];
      this.pipelineRuns = pipelines ?? [];
      this.councilRuns = council ?? [];
      this.evalScores = scores ?? [];
      this.applyKPISnapshot(kpis);
      this.lastUpdated = new Date();
      this.disabled = false;
      // Refresh adaptive policy proposals on the same tick (Phase 7
      // slice 7.4). Awaited but never throws; its own try/catch
      // silences errors so the rest of the panel stays green.
      void this.fetchPolicyProposals();
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      // Treat 503 from the proxy as "Hive disabled" rather than an error,
      // so the empty-state UX is calm, not red.
      if (msg.includes('503') || msg.toLowerCase().includes('not configured')) {
        this.disabled = true;
        this.error = null;
      } else {
        this.disabled = false;
        this.error = msg;
      }
    } finally {
      this.loading = false;
    }
  }

  private async getJSON<T>(path: string, opts: { tolerate404?: boolean } = {}): Promise<T | null> {
    const res = await globalThis.fetch(path);
    if (res.status === 503) {
      // Surface to fetchAll so it can flip the disabled flag.
      throw new Error(`hive proxy: 503 (operator not configured)`);
    }
    if (res.status === 404 && opts.tolerate404) {
      return null;
    }
    if (!res.ok) {
      throw new Error(`${path}: ${res.status}`);
    }
    // Some routes may return null body; tolerate it.
    const text = await res.text();
    if (!text) return null;
    return JSON.parse(text) as T;
  }

  private applyKPISnapshot(raw: RawKPISnapshot | null): void {
    if (!raw) {
      this.kpis = null;
      return;
    }
    const snap: HiveKPISnapshot = {
      snapshot_at: raw.SnapshotAt ?? raw.snapshot_at,
      window_seconds: raw.WindowSeconds ?? raw.window_seconds,
      metrics: raw.Metrics ?? raw.metrics ?? {},
    };
    this.kpis = snap;
    // Append to history only when snapshot_at advances; otherwise the
    // sparkline would just plot the same point N times.
    const last = this.kpisHistory[this.kpisHistory.length - 1];
    if (!last || last.snapshot_at !== snap.snapshot_at) {
      const next = [...this.kpisHistory, snap];
      this.kpisHistory = next.slice(-KPI_HISTORY_MAX);
    }
  }

  // Pull a single KPI metric series from the in-memory history. Missing
  // values are skipped (no zero-filling) so the sparkline reflects only
  // observed data points.
  metricSeries(key: keyof NonNullable<HiveKPISnapshot['metrics']>): number[] {
    const out: number[] = [];
    for (const snap of this.kpisHistory) {
      const v = snap.metrics?.[key];
      if (typeof v === 'number' && Number.isFinite(v)) out.push(v);
    }
    return out;
  }

  // loadDebate fetches the per-round transcript for one council run.
  // Cache-on-success: subsequent calls for the same id return without
  // network. Errors are surfaced via debateByRun[id].status === 'error'
  // so the panel can show a retry affordance instead of a silent fail.
  // The 'idle' / 'loading' transitions are explicit so the panel can
  // distinguish "never tried" from "in flight".
  async loadDebate(runID: string): Promise<void> {
    if (!runID) return;
    const cached = this.debateByRun[runID];
    if (cached && (cached.status === 'loaded' || cached.status === 'loading')) {
      return;
    }
    this.debateByRun = { ...this.debateByRun, [runID]: { status: 'loading' } };
    try {
      const rounds =
        (await this.getJSON<CouncilDebateRound[]>(
          `/api/hive/council/runs/${encodeURIComponent(runID)}/debate`,
        )) ?? [];
      this.debateByRun = {
        ...this.debateByRun,
        [runID]: { status: 'loaded', rounds },
      };
    } catch (e) {
      const message = e instanceof Error ? e.message : String(e);
      this.debateByRun = {
        ...this.debateByRun,
        [runID]: { status: 'error', message },
      };
    }
  }

  // postJSON is the mutation counterpart to getJSON. It surfaces 503 the
  // same way (so the disabled flag flips) but otherwise treats any
  // non-2xx as an error to surface in the UI. Body is JSON-encoded; pass
  // {} when the endpoint takes no payload.
  private async postJSON<T>(path: string, body: unknown): Promise<T | null> {
    const res = await globalThis.fetch(path, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(body ?? {}),
    });
    if (res.status === 503) {
      this.disabled = true;
      throw new Error('hive proxy: 503 (operator not configured)');
    }
    if (!res.ok) {
      throw new Error(`${path}: ${res.status}`);
    }
    const text = await res.text();
    if (!text) return null;
    return JSON.parse(text) as T;
  }

  // fetchPolicyProposals refreshes the pending-proposals list. Called
  // from fetchAll() so the panel piggybacks on the existing 15s poll.
  async fetchPolicyProposals(state: string = 'pending'): Promise<void> {
    if (this.disabled) return;
    try {
      const list = await this.getJSON<PolicyProposal[]>(
        `/api/hive/policy/proposals?state=${encodeURIComponent(state)}`,
      );
      this.policyProposals = list ?? [];
    } catch (e) {
      // Don't pollute store.error; proposals failures shouldn't blank
      // the rest of the Hive UI. Console is enough for triage.
      // eslint-disable-next-line no-console
      console.warn('fetchPolicyProposals failed', e);
    }
  }

  // fetchCostPreview is fire-and-store. Returns the estimate for callers
  // that want it inline; also caches into costPreviews keyed by
  // backlog_id so derived views can render without their own state.
  async fetchCostPreview(backlogID: string): Promise<CostEstimate | null> {
    if (this.disabled || !backlogID) return null;
    try {
      const est = await this.getJSON<CostEstimate>(
        `/api/hive/cost-preview?backlog_id=${encodeURIComponent(backlogID)}`,
      );
      if (est) {
        this.costPreviews = { ...this.costPreviews, [backlogID]: est };
      }
      return est ?? null;
    } catch {
      return null;
    }
  }

  async applyPolicyProposal(id: number): Promise<void> {
    await this.postJSON(`/api/hive/policy/proposals/${id}/apply`, {});
    await this.fetchPolicyProposals();
  }

  async rejectPolicyProposal(id: number): Promise<void> {
    await this.postJSON(`/api/hive/policy/proposals/${id}/reject`, {});
    await this.fetchPolicyProposals();
  }

  startPolling(intervalMs = 15000): void {
    this.stopPolling();
    void this.fetchAll();
    this.pollTimer = setInterval(() => void this.fetchAll(), intervalMs);
  }

  stopPolling(): void {
    if (this.pollTimer) {
      clearInterval(this.pollTimer);
      this.pollTimer = null;
    }
  }
}

export const hiveStore = new HiveStore();
