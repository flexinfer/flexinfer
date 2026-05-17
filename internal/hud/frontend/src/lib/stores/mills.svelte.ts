// Mills store — backlog / pipeline runs / council runs / eval scores from
// the in-cluster loom-mills-operator, proxied through /api/mills/* by the
// HUD's domain/mills package. Each panel owns a slice of this store and
// polls the corresponding read endpoint at 15s.
//
// Empty/disabled state: when the proxy returns 503 ("operator not
// configured") we surface that via the `disabled` flag so panels can
// render a clear "Mills disabled" empty-state instead of a fetch error.

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
  // CurrentStage is the stage id the runner is currently driving
  // (plan_slice, research, implement, tests, pr_self_review, mr, ci_watch,
  // merge, cleanup). Surfaces what a run is "doing right now" without
  // requiring an extra detail fetch.
  CurrentStage?: string;
  Attempts: number;
  StartedAt?: string;
  EndedAt?: string;
  // Phase 6 (bounded recursion): top-level runs have ParentRunID == null
  // and Depth == 0. Subruns created via mills_pipeline_subrun_create
  // carry their parent's ID and depth+1.
  ParentRunID?: string | null;
  Depth?: number;
  // Detail-only fields (populated by GET /api/mills/pipeline/runs/{id});
  // list responses may omit them, so they're optional. WorktreePath +
  // MRIID let the drilldown surface direct links; CostUSD is the
  // aggregate stage spend for the header total.
  WorktreePath?: string;
  MRIID?: number | null;
  CostUSD?: number;
  ParentSessionID?: string;
}

// StageResult mirrors store.StageResult — one attempt at one stage.
// Outcome is *StageOutcome on the Go side: null/missing while in flight,
// "success" | "gate_fail" | "error" once finalized.
export interface StageResult {
  ID: number;
  PipelineRunID: string;
  Stage: string;
  Attempt: number;
  StartedAt: string;
  EndedAt?: string | null;
  Outcome?: 'success' | 'gate_fail' | 'error' | null;
  SpawnID?: string;
  CostUSD: number;
  Artifacts?: Record<string, unknown>;
  LogTail?: string;
}

// GateOutcome mirrors store.GateOutcome — one evaluated gate after a
// stage. Reasons are surfaced to the user verbatim so they can act on
// gate_fail without diving into the operator logs.
export interface GateOutcome {
  ID: number;
  PipelineRunID: string;
  AfterStage: string;
  GateName: string;
  Outcome: 'pass' | 'fail' | 'skip';
  Reasons?: string[];
  JudgedBy?: string;
  EvaluatedAt: string;
}

// PipelineRunDetail is the shape returned by handlePipelineRunGet —
// run + stages + gates in one round-trip so the drilldown drawer can
// render without per-section fetches.
export interface PipelineRunDetail {
  run: PipelineRun;
  stages: StageResult[];
  gates: GateOutcome[];
}

type PipelineDetailLoadState =
  | { status: 'idle' }
  | { status: 'loading' }
  | { status: 'loaded'; detail: PipelineRunDetail }
  | { status: 'error'; message: string };

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
// Stored in the millsStore so the panel can render a spinner / error
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

export interface MillsCapabilityRow {
  id: string;
  status?: 'green' | 'yellow' | 'red' | string;
  mode?: string;
  required_for_autonomy?: boolean;
  last_checked_at?: string;
  message?: string;
  source?: string;
  config_key?: string;
}

export interface MillsStatus {
  ok?: boolean;
  service?: string;
  time?: string;
  policy_enabled?: boolean;
  policy_version?: number;
  autonomy_ready?: boolean;
  autonomy_blockers?: string[];
  capabilities?: MillsCapabilityRow[];
  queue_depth?: number;
  active_pipeline_runs?: number;
  last_council_at?: string | null;
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

// CostEstimate mirrors the slice 7.3 /api/mills/cost-preview response.
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

// MillsKPISnapshot mirrors the operator's `kpi_snapshots.metrics_json`
// rollup. Fields are optional because the recorder only emits keys it has
// data for; missing keys render as "—" placeholders. Field names here are
// the contract the (future) snapshot recorder must honor.
export interface MillsKPISnapshot {
  snapshot_at?: string;
  window_seconds?: number;
  metrics?: {
    cost_per_merged_change_usd?: number;
    cost_per_merged_pipeline_usd?: number;
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
  Metrics?: MillsKPISnapshot['metrics'];
  snapshot_at?: string;
  window_seconds?: number;
  metrics?: MillsKPISnapshot['metrics'];
}

const KPI_HISTORY_MAX = 24;

// SystemHealth + computeSystemHealth live in a rune-free sibling module
// so fixtures / SSR can exercise them without a Svelte runtime. Re-export
// here so consumers keep `from './mills.svelte.ts'` ergonomics.
export type { SystemHealth, SystemHealthState } from './mills.systemHealth.ts';
import { computeSystemHealth } from './mills.systemHealth.ts';
import type { SystemHealth } from './mills.systemHealth.ts';

class MillsStore {
  // Per-panel data
  backlog = $state<BacklogItem[]>([]);
  pipelineRuns = $state<PipelineRun[]>([]);
  councilRuns = $state<CouncilRun[]>([]);
  evalScores = $state<EvalScore[]>([]);
  policy = $state<PolicyView | null>(null);
  status = $state<MillsStatus | null>(null);

  // KPI snapshot for the rolling 1d window plus a small in-memory history
  // for sparkline trends. The history is only de-duped on snapshot_at so
  // repeated polls of the same snapshot don't pad the trend.
  kpis = $state<MillsKPISnapshot | null>(null);
  kpisHistory = $state<MillsKPISnapshot[]>([]);

  // Per-run debate transcripts, keyed by CouncilRun.ID. Populated
  // lazily by loadDebate() so the council list itself stays cheap.
  // Phase 5 slice 5.3 — feeds the CouncilPanel's "Debate Rounds"
  // expander.
  debateByRun = $state<Record<string, DebateLoadState>>({});

  // Pipeline-run drilldown state. selectedRunID drives whether the
  // PipelineRunDetail drawer is open; pipelineDetailByRun caches the
  // {run, stages, gates} payload so re-opening the same run is
  // instant. The 15s background poll refreshes the cached entry only
  // when the drawer is currently open (see refreshOpenPipelineDetail).
  selectedRunID = $state<string | null>(null);
  pipelineDetailByRun = $state<Record<string, PipelineDetailLoadState>>({});

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

  get autonomyReady(): boolean | null {
    return this.status?.autonomy_ready ?? null;
  }

  get autonomyBlocked(): boolean {
    return this.status?.autonomy_ready === false && (this.status?.autonomy_blockers?.length ?? 0) > 0;
  }

  get autonomyBlockers(): string[] {
    return this.status?.autonomy_blockers ?? [];
  }

  // systemHealth is the input that drives the Overview "System Health"
  // banner. Recomputed on every access (cheap; bounded by the pipeline run
  // page) so it stays in lock-step with the 15s poll.
  get systemHealth(): SystemHealth {
    return computeSystemHealth({
      pipelineRuns: this.pipelineRuns,
      status: this.status,
      councilRuns: this.councilRuns,
      backlog: this.backlog,
    });
  }

  async fetchAll(): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const [status, policy, backlog, pipelines, council, scores, kpis] = await Promise.all([
        this.getJSON<MillsStatus>('/api/mills/status'),
        this.getJSON<PolicyView>('/api/mills/policy'),
        this.getJSON<BacklogItem[]>('/api/mills/backlog'),
        this.getJSON<PipelineRun[]>('/api/mills/pipeline/runs'),
        this.getJSON<CouncilRun[]>('/api/mills/council/runs'),
        this.getJSON<EvalScore[]>('/api/mills/eval/scores'),
        // KPI endpoint returns 404 until the snapshot recorder ships.
        // Tolerate that by passing { tolerate404: true }; null is fine.
        this.getJSON<RawKPISnapshot>('/api/mills/kpis?window=1d', { tolerate404: true }),
      ]);
      this.status = status;
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
      // Keep an open pipeline-detail drawer in sync with the 15s
      // cadence — the drawer otherwise frozen at open-time would
      // misrepresent in-flight runs as their stages advance.
      void this.refreshOpenPipelineDetail();
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      // Treat 503 from the proxy as "Mills disabled" rather than an error,
      // so the empty-state UX is calm, not red.
      if (msg.includes('503') || msg.toLowerCase().includes('not configured')) {
        this.disabled = true;
        this.status = null;
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
      throw new Error(`mills proxy: 503 (operator not configured)`);
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
    const snap: MillsKPISnapshot = {
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
  metricSeries(key: keyof NonNullable<MillsKPISnapshot['metrics']>): number[] {
    const out: number[] = [];
    for (const snap of this.kpisHistory) {
      const v = snap.metrics?.[key];
      if (typeof v === 'number' && Number.isFinite(v)) out.push(v);
    }
    return out;
  }

  // openPipelineDetail is the derived load-state for the currently
  // selected run id. Returns null when no run is selected so the
  // drawer component can render purely off this one signal.
  get openPipelineDetail(): PipelineDetailLoadState | null {
    if (!this.selectedRunID) return null;
    return this.pipelineDetailByRun[this.selectedRunID] ?? { status: 'idle' };
  }

  // openRunDetail opens the drilldown drawer for a pipeline run.
  // Cache-on-success: subsequent opens for the same id render
  // immediately from cache while a fresh fetch refreshes in the
  // background. The 'loading' state is only set when there's no
  // cached payload yet, so re-opening doesn't flash an empty drawer.
  openRunDetail(runID: string): void {
    if (!runID) return;
    this.selectedRunID = runID;
    const cached = this.pipelineDetailByRun[runID];
    if (!cached || cached.status === 'idle' || cached.status === 'error') {
      this.pipelineDetailByRun = {
        ...this.pipelineDetailByRun,
        [runID]: { status: 'loading' },
      };
    }
    void this.fetchPipelineDetail(runID);
  }

  closeRunDetail(): void {
    this.selectedRunID = null;
  }

  // refreshOpenPipelineDetail re-fetches the currently open run on
  // each background poll tick so the drawer stays live without
  // forcing the user to close+reopen. No-op when nothing is open.
  // Called from fetchAll() so it shares the 15s cadence.
  async refreshOpenPipelineDetail(): Promise<void> {
    const id = this.selectedRunID;
    if (!id) return;
    await this.fetchPipelineDetail(id);
  }

  private async fetchPipelineDetail(runID: string): Promise<void> {
    try {
      const detail = await this.getJSON<PipelineRunDetail>(
        `/api/mills/pipeline/runs/${encodeURIComponent(runID)}`,
      );
      if (!detail) {
        // Treat a null body as "not found" — surface as error so the
        // drawer can show a retry instead of an empty pane.
        this.pipelineDetailByRun = {
          ...this.pipelineDetailByRun,
          [runID]: { status: 'error', message: 'run not found' },
        };
        return;
      }
      this.pipelineDetailByRun = {
        ...this.pipelineDetailByRun,
        [runID]: { status: 'loaded', detail },
      };
    } catch (e) {
      const message = e instanceof Error ? e.message : String(e);
      this.pipelineDetailByRun = {
        ...this.pipelineDetailByRun,
        [runID]: { status: 'error', message },
      };
    }
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
          `/api/mills/council/runs/${encodeURIComponent(runID)}/debate`,
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
      throw new Error('mills proxy: 503 (operator not configured)');
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
        `/api/mills/policy/proposals?state=${encodeURIComponent(state)}`,
      );
      this.policyProposals = list ?? [];
    } catch (e) {
      // Don't pollute store.error; proposals failures shouldn't blank
      // the rest of the Mills UI. Console is enough for triage.
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
        `/api/mills/cost-preview?backlog_id=${encodeURIComponent(backlogID)}`,
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
    await this.postJSON(`/api/mills/policy/proposals/${id}/apply`, {});
    await this.fetchPolicyProposals();
  }

  async rejectPolicyProposal(id: number): Promise<void> {
    await this.postJSON(`/api/mills/policy/proposals/${id}/reject`, {});
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

export const millsStore = new MillsStore();
