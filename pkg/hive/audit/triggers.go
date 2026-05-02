package audit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/crb2nu/loom/pkg/hive/store"
)

// PoolPolicy is the configuration the QueueWorker passes to every
// dispatcher Run call. Operators load it from policy.yaml at boot; the
// QueueWorker treats it as immutable for the duration of one drain.
// Hot-reload of pool composition is a v2.1 concern — until then,
// changes require an operator restart.
type PoolPolicy struct {
	// Bulk is the default pool that runs on every audit attempt. Spec
	// recommends 2-3 FlexInfer-hosted models (e.g. Llama 4 70B + Qwen 3
	// 32B).
	Bulk []PoolMember

	// Escalation runs only when the bulk median lands in the
	// EscalationLowerBound..EscalationUpperBound band. Empty disables
	// escalation; production v2.0 wires Claude Opus + Codex GPT-5 here.
	Escalation []PoolMember
}

// QueueOptions tunes the in-memory queue's capacity + drain timeout.
// Defaults mirror what the operator boots with.
type QueueOptions struct {
	// Capacity is the max pending audit jobs the queue holds. New
	// enqueues that overflow the buffer drop the oldest pending entry
	// (which is a conscious trade — a flood is more likely a producer
	// bug than load worth pacing for, so we'd rather lose old jobs and
	// keep the latest news).
	Capacity int

	// PerJobTimeout caps how long one audit run can take. The
	// dispatcher's pool members each run with this deadline; on
	// timeout the audit is recorded with whatever survived. Default 90s.
	PerJobTimeout time.Duration

	// Logger is the structured logger; nil discards.
	Logger *slog.Logger

	// Clock is injected for deterministic tests; defaults to time.Now.
	Clock func() time.Time
}

// QueueWorker accepts audit jobs from the Triggers and drains them
// against a shared Dispatcher. Records every Result via the supplied
// AuditDAO. Best-effort: a recording failure logs but does not block
// the drain — the queue keeps moving even if the canonical store is
// briefly degraded.
type QueueWorker struct {
	dispatcher *Dispatcher
	dao        *store.AuditDAO
	policy     PoolPolicy
	opts       QueueOptions
	jobs       chan Request
	done       chan struct{}
	stopOnce   sync.Once
	logger     *slog.Logger

	// OnRecorded fires after a finding has been persisted to the
	// canonical store. Production wires squads.audit.Followup here so
	// low-survival findings auto-open advisory issues. Errors from the
	// hook are logged + swallowed — the worker keeps draining.
	OnRecorded func(ctx context.Context, finding *store.AuditFinding) error
}

// NewQueueWorker constructs a worker. The caller is responsible for
// calling Run(ctx) in a goroutine; Stop() drains and unblocks Run.
func NewQueueWorker(d *Dispatcher, dao *store.AuditDAO, p PoolPolicy, opts QueueOptions) *QueueWorker {
	if d == nil {
		panic("audit: queue worker requires a dispatcher")
	}
	if dao == nil {
		panic("audit: queue worker requires an AuditDAO")
	}
	if opts.Capacity <= 0 {
		opts.Capacity = 64
	}
	if opts.PerJobTimeout <= 0 {
		opts.PerJobTimeout = 90 * time.Second
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	logger := opts.Logger
	return &QueueWorker{
		dispatcher: d,
		dao:        dao,
		policy:     p,
		opts:       opts,
		jobs:       make(chan Request, opts.Capacity),
		done:       make(chan struct{}),
		logger:     logger,
	}
}

// Enqueue adds an audit job to the back of the queue. Returns false +
// logs a warn when the buffer is full so the producer can decide
// whether to drop or retry.
//
// The caller fills SubjectKind, SubjectID, and Artifact; the worker
// fills Pool + EscalationPool from PoolPolicy. Setting Pool on the
// supplied request is allowed but not required — when present, it
// overrides the policy default for that one job (used by the admin
// /api/hive/audit/run endpoint to test alternative pools).
func (w *QueueWorker) Enqueue(req Request) bool {
	if w == nil {
		return false
	}
	if len(req.Pool) == 0 {
		req.Pool = w.policy.Bulk
	}
	if len(req.EscalationPool) == 0 {
		req.EscalationPool = w.policy.Escalation
	}
	select {
	case w.jobs <- req:
		return true
	default:
		w.warn("audit: queue full, dropping job",
			"subject_kind", string(req.SubjectKind),
			"subject_id", req.SubjectID,
			"capacity", w.opts.Capacity,
		)
		return false
	}
}

// Run drains the queue until ctx is canceled or Stop() is called. Each
// job runs on a per-job derived context bounded by PerJobTimeout. Errors
// from the dispatcher (validation, render) are logged + skipped; errors
// from the canonical store recording are logged + skipped (the audit
// row is dropped; the dispatcher's in-memory Result is gone — operators
// can re-trigger via the admin /run endpoint).
func (w *QueueWorker) Run(ctx context.Context) {
	if w == nil {
		return
	}
	defer close(w.done)
	for {
		select {
		case <-ctx.Done():
			return
		case req, ok := <-w.jobs:
			if !ok {
				// Stop() closed the channel — drain done.
				return
			}
			w.process(ctx, req)
		}
	}
}

// Stop closes the in-flight signal. Run() returns once the current job
// drains. Safe to call multiple times.
func (w *QueueWorker) Stop() {
	if w == nil {
		return
	}
	w.stopOnce.Do(func() {
		close(w.jobs)
	})
}

// Done returns a channel that closes when Run() exits. Useful for tests
// that want to wait for graceful shutdown.
func (w *QueueWorker) Done() <-chan struct{} {
	if w == nil {
		return nil
	}
	return w.done
}

func (w *QueueWorker) process(parent context.Context, req Request) {
	jobCtx, cancel := context.WithTimeout(parent, w.opts.PerJobTimeout)
	defer cancel()
	res, err := w.dispatcher.Run(jobCtx, &req)
	if err != nil {
		w.warn("audit: dispatcher run failed",
			"subject_kind", string(req.SubjectKind),
			"subject_id", req.SubjectID,
			"error", err,
		)
		return
	}
	if res.Finding == nil {
		w.warn("audit: dispatcher returned nil finding",
			"subject_kind", string(req.SubjectKind),
			"subject_id", req.SubjectID,
		)
		return
	}
	if err := w.dao.RecordFinding(parent, res.Finding); err != nil {
		w.warn("audit: record finding failed",
			"subject_kind", string(req.SubjectKind),
			"subject_id", req.SubjectID,
			"error", err,
		)
		return
	}
	// Fire the post-record hook (followup writer in production). Errors
	// here are advisory — the audit row already persisted, and the hook
	// has its own logger; never block subsequent drains on a hook
	// failure.
	if w.OnRecorded != nil {
		if err := w.OnRecorded(parent, res.Finding); err != nil {
			w.warn("audit: OnRecorded hook returned error",
				"subject_kind", string(req.SubjectKind),
				"subject_id", req.SubjectID,
				"error", err,
			)
		}
	}
	w.info("audit: finding recorded",
		"subject_kind", string(req.SubjectKind),
		"subject_id", req.SubjectID,
		"survival", res.Finding.SurvivalScore,
		"severity", string(res.Finding.Severity),
		"escalated", res.Escalated,
	)
}

func (w *QueueWorker) warn(msg string, kv ...any) {
	if w == nil || w.logger == nil {
		return
	}
	w.logger.Warn(msg, kv...)
}

func (w *QueueWorker) info(msg string, kv ...any) {
	if w == nil || w.logger == nil {
		return
	}
	w.logger.Info(msg, kv...)
}

// ----- Triggers -----

// PolicyGate is the minimal contract Triggers needs to honor the v2
// policy.audit.enabled flag without importing pkg/hive (which would
// invert the existing one-way import boundary). Production wires this
// to a closure over hive.PolicyManager.Current().AuditEnabled so the
// trigger picks up policy hot-reloads without restart.
type PolicyGate interface {
	AuditEnabled() bool
}

// Triggers is the producer-side surface the operator wires into the
// council runner's OnArtifactsCommitted callback and the pipeline
// runner's OnMerged hook chain. Both translate domain events into
// Request envelopes the QueueWorker drains.
type Triggers struct {
	Worker *QueueWorker

	// LoadCouncilArtifact is how the trigger fetches the artifact
	// markdown for a freshly-committed council run. Production loads
	// from disk via os.ReadFile against the council branch; tests inject
	// a fake.
	LoadCouncilArtifact func(ctx context.Context, run *store.CouncilRun, refs []store.ArtifactRef) (artifact, sidecarJSON string, err error)

	// LoadMergedDiff is how the trigger fetches the unified diff for a
	// merged pipeline run. Production calls mcp-gitlab; tests fake.
	LoadMergedDiff func(ctx context.Context, run *store.PipelineRun, item *store.BacklogItem) (string, error)

	// Policy, when non-nil, gates every enqueue on policy.audit.enabled.
	// Nil preserves pre-v2 behavior — both callbacks always enqueue.
	// Wired to the live hive.PolicyManager so hot-reload flips take
	// effect on the next callback without restart.
	Policy PolicyGate

	Logger *slog.Logger
}

// OnArtifactsCommitted is the callback shape pkg/hive/runner.Runner
// fires after a successful council run persistence. Loads the artifact
// text + sidecar via LoadCouncilArtifact and enqueues an audit job.
// Best-effort: load failures log but do not block.
//
// Policy gate: when t.Policy is non-nil and AuditEnabled() returns
// false, the callback no-ops before any IO — no artifact load, no
// enqueue, no canonical-store write. Mirrors policy.audit.enabled.
func (t *Triggers) OnArtifactsCommitted(ctx context.Context, run *store.CouncilRun, refs []store.ArtifactRef) {
	if t == nil || t.Worker == nil || run == nil {
		return
	}
	if t.Policy != nil && !t.Policy.AuditEnabled() {
		return
	}
	if t.LoadCouncilArtifact == nil {
		t.warn("audit: trigger council loader missing; skipping enqueue", "run", run.ID)
		return
	}
	artifact, sidecar, err := t.LoadCouncilArtifact(ctx, run, refs)
	if err != nil {
		t.warn("audit: load council artifact failed", "run", run.ID, "error", err)
		return
	}
	if artifact == "" {
		t.warn("audit: council artifact empty; skipping enqueue", "run", run.ID)
		return
	}
	req := Request{
		SubjectKind: store.AuditSubjectCouncilArtifact,
		SubjectID:   run.ID,
		Artifact:    artifact,
	}
	// Sidecar JSON ride-alongs as the second artifact source so the
	// rubric prompt has the structured view alongside the markdown.
	// We append it after the markdown to keep the artifact body
	// self-contained when the sidecar is empty (rubric template renders
	// "{}" by default).
	if sidecar != "" {
		req.Artifact = artifact + "\n\n<!-- sidecar -->\n" + sidecar
	}
	if !t.Worker.Enqueue(req) {
		t.warn("audit: enqueue dropped (queue full)",
			"subject_kind", "council_artifact", "subject_id", run.ID)
	}
}

// OnPipelineMerged is the callback shape pipelineRunner.OnMerged
// expects. Loads the merged diff via LoadMergedDiff and enqueues an
// audit job. Errors are logged but never block the merge.
//
// Policy gate: when t.Policy is non-nil and AuditEnabled() returns
// false, the callback no-ops before any IO. Returns nil so chained
// OnMerged hooks see no merge-side error from a disabled audit.
func (t *Triggers) OnPipelineMerged(ctx context.Context, run *store.PipelineRun, item *store.BacklogItem) error {
	if t == nil || t.Worker == nil || run == nil {
		return nil
	}
	if t.Policy != nil && !t.Policy.AuditEnabled() {
		return nil
	}
	if t.LoadMergedDiff == nil {
		t.warn("audit: trigger merge loader missing; skipping enqueue", "run", run.ID)
		return nil
	}
	diff, err := t.LoadMergedDiff(ctx, run, item)
	if err != nil {
		t.warn("audit: load merged diff failed", "run", run.ID, "error", err)
		// Returning nil on purpose — chained OnMerged hooks must not
		// surface audit-loader failures as merge errors.
		return nil
	}
	if diff == "" {
		// An empty diff is unusual but not an error — log for the audit
		// trail and skip the enqueue.
		t.warn("audit: merged diff empty; skipping enqueue", "run", run.ID)
		return nil
	}
	req := Request{
		SubjectKind: store.AuditSubjectPipelineMerge,
		SubjectID:   run.ID,
		Artifact:    diff,
	}
	if !t.Worker.Enqueue(req) {
		t.warn("audit: enqueue dropped (queue full)",
			"subject_kind", "pipeline_merge", "subject_id", run.ID)
	}
	return nil
}

func (t *Triggers) warn(msg string, kv ...any) {
	if t == nil || t.Logger == nil {
		return
	}
	t.Logger.Warn(msg, kv...)
}

// LoadCouncilArtifactFromFS is the production loader. It reads every
// markdown file referenced in `refs` (skipping non-markdown ArtifactRefs)
// and concatenates them with their filename headers; the sidecar
// (kind="sidecar") is returned separately as JSON. Path resolution is
// relative to repoRoot.
func LoadCouncilArtifactFromFS(repoRoot string) func(ctx context.Context, run *store.CouncilRun, refs []store.ArtifactRef) (string, string, error) {
	return func(ctx context.Context, run *store.CouncilRun, refs []store.ArtifactRef) (string, string, error) {
		if repoRoot == "" {
			return "", "", errors.New("audit: repo root empty")
		}
		var (
			body    []byte
			sidecar []byte
		)
		for _, ref := range refs {
			if ref.Path == "" {
				continue
			}
			full := filepath.Join(repoRoot, filepath.Clean(ref.Path))
			data, err := os.ReadFile(full)
			if err != nil {
				return "", "", fmt.Errorf("audit: read %s: %w", ref.Path, err)
			}
			if ref.Kind == "sidecar" {
				sidecar = data
				continue
			}
			body = append(body, []byte("\n# "+ref.Path+"\n\n")...)
			body = append(body, data...)
		}
		return string(body), string(sidecar), nil
	}
}
