package proxy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/validation"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// The job runner. Each pass lists the namespace's InferenceJobs, claims queued
// ones its pools have room for by a status update on the observed
// resourceVersion (a conflict means another replica won), and runs each claim
// by dispatching the stored request through this proxy's own handler, so the
// job takes the synchronous path: alias and label resolution, serve-paths,
// admission, the cold-start queue (which stamps demand and activates the lane
// exactly as for a client), the lane's first-byte budget, its breaker and its
// reroutes. The response is spooled, stored and recorded on the job.
//
// Concurrency: jobs run per concurrency key. A diffusers lane runs one at a
// time, and all diffusers lanes sharing one GPU (spec.gpu.shared) share one
// key, so two queued renders never ask the shared-GPU election for the same
// card at once; any other lane runs FLEXINFER_JOBS_PER_MODEL at a time
// (flexinfer.ai/job-concurrency overrides both). A job waiting for its key is
// not demand; a claimed job is, from its first forward until it finishes,
// including the waits between retries.

const (
	jobHeartbeatInterval   = 15 * time.Second
	jobStaleAfter          = 3 * jobHeartbeatInterval
	jobRetryMax            = 60 * time.Second
	jobMaxServerErrRetries = 3
	jobMaxErrorBodyBytes   = 64 << 10
	jobDemandInterval      = 30 * time.Second

	annotationJobConcurrency = "flexinfer.ai/job-concurrency"

	jobProgressClaimed   = 5
	jobProgressLaneReady = 50
	jobProgressFirstByte = 90
)

// jobRetryMinWait is the shortest wait before a job retries its lane (a var
// so tests can shorten it).
var jobRetryMinWait = 5 * time.Second

// jobRunner runs InferenceJobs for one proxy replica.
type jobRunner struct {
	p       *Proxy
	cfg     JobsConfig
	handler http.Handler
	nudge   chan struct{}
	hooks   *http.Client
	now     func() time.Time

	mu         sync.Mutex
	running    map[string]context.CancelFunc
	cancelled  map[string]bool
	delivering map[string]bool
}

func newJobRunner(p *Proxy, cfg JobsConfig) *jobRunner {
	if cfg.RunnerID == "" {
		cfg.RunnerID, _ = os.Hostname()
	}
	if cfg.SpoolDir == "" {
		cfg.SpoolDir = os.TempDir()
	}
	return &jobRunner{
		p: p, cfg: cfg, handler: p.newServeMux(), nudge: make(chan struct{}, 1),
		hooks: &http.Client{Timeout: jobWebhookTimeout}, now: time.Now,
		running: map[string]context.CancelFunc{}, cancelled: map[string]bool{}, delivering: map[string]bool{},
	}
}

// wake asks for a pass now (after a submission or a cancel).
func (r *jobRunner) wake() {
	if r == nil {
		return
	}
	select {
	case r.nudge <- struct{}{}:
	default:
	}
}

// run passes every PollInterval, or sooner when woken, until ctx ends.
func (r *jobRunner) run(ctx context.Context) {
	ticker := time.NewTicker(r.cfg.PollInterval)
	defer ticker.Stop()
	slog.Info("job runner started", "runner", r.cfg.RunnerID, "per_model", r.cfg.PerModel, "max_running", r.cfg.MaxRunning)
	for {
		r.pass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-r.nudge:
		}
	}
}

// jobKey is a job's concurrency key and pool size.
func (r *jobRunner) jobKey(m *aiv1alpha2.Model, model string) (string, int) {
	pool := r.cfg.PerModel
	if m == nil {
		return "model/" + model, pool
	}
	key := "model/" + m.Name
	if m.Spec.Backend == "diffusers" {
		pool = 1
		if m.Spec.IsShared() {
			key = "gpu/" + m.Spec.GPU.Shared
		}
	}
	if v, err := strconv.Atoi(strings.TrimSpace(m.Annotations[annotationJobConcurrency])); err == nil && v > 0 {
		pool = v
	}
	return key, pool
}

// jobKeyFunc returns a key lookup that resolves each model name once.
func (p *Proxy) jobKeyFunc(ctx context.Context) func(*aiv1alpha2.InferenceJob) string {
	r := p.jobs
	if r == nil {
		r = &jobRunner{cfg: JobsConfig{PerModel: 1}}
	}
	keys := map[string]string{}
	return func(j *aiv1alpha2.InferenceJob) string {
		if k, ok := keys[j.Spec.Model]; ok {
			return k
		}
		k, _ := r.jobKey(p.jobLaneModel(ctx, j.Spec.Model), j.Spec.Model)
		keys[j.Spec.Model] = k
		return k
	}
}

func (r *jobRunner) isRunning(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.running[name]
	return ok
}

func (r *jobRunner) runningCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.running)
}

// cancelRunning stops a job this replica runs.
func (r *jobRunner) cancelRunning(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cancel, ok := r.running[name]; ok {
		r.cancelled[name] = true
		cancel()
	}
}

func heartbeatFresh(j *aiv1alpha2.InferenceJob, now time.Time) bool {
	return j.Status.HeartbeatAt != nil && now.Sub(j.Status.HeartbeatAt.Time) < jobStaleAfter
}

// pass is one list-and-claim round.
func (r *jobRunner) pass(ctx context.Context) {
	var list aiv1alpha2.InferenceJobList
	if err := r.p.client.List(ctx, &list, client.InNamespace(r.p.namespace)); err != nil {
		slog.Warn("job runner: listing jobs failed", "error", err)
		return
	}
	now := r.now()
	type candidate struct {
		job  *aiv1alpha2.InferenceJob
		key  string
		pool int
		lane string
	}
	busy := map[string]int{}
	var queued []candidate
	models := map[string]*aiv1alpha2.Model{}
	laneOf := func(model string) *aiv1alpha2.Model {
		if m, ok := models[model]; ok {
			return m
		}
		m := r.p.jobLaneModel(ctx, model)
		models[model] = m
		return m
	}
	for i := range list.Items {
		j := &list.Items[i]
		switch {
		case j.DeletionTimestamp != nil:
			r.finalize(ctx, j)
			continue
		case j.Status.Phase.IsTerminal():
			r.resumeWebhook(ctx, j)
			continue
		}
		mine := j.Status.Runner == r.cfg.RunnerID && r.isRunning(j.Name)
		active := j.Status.Phase == aiv1alpha2.InferenceJobInProgress && (mine || (j.Status.Runner != r.cfg.RunnerID && heartbeatFresh(j, now)))
		if j.Spec.Cancel {
			if mine {
				r.cancelRunning(j.Name)
			} else if !active {
				r.finish(ctx, j.Name, aiv1alpha2.InferenceJobCancelled, nil, nil)
			}
			continue
		}
		lane := laneOf(j.Spec.Model)
		key, pool := r.jobKey(lane, j.Spec.Model)
		if active {
			busy[key]++
			continue
		}
		laneName := ""
		if lane != nil {
			laneName = lane.Name
		}
		queued = append(queued, candidate{job: j, key: key, pool: pool, lane: laneName})
	}
	sort.SliceStable(queued, func(a, b int) bool { return jobBefore(queued[a].job, queued[b].job) })
	local := r.runningCount()
	for _, c := range queued {
		if local >= r.cfg.MaxRunning {
			return
		}
		if busy[c.key] >= c.pool {
			continue
		}
		if !r.claim(ctx, c.job, c.lane) {
			continue
		}
		busy[c.key]++
		local++
		r.start(c.job)
	}
}

// claim takes j for this replica with an update on its observed
// resourceVersion; a conflict means another replica claimed it first.
func (r *jobRunner) claim(ctx context.Context, j *aiv1alpha2.InferenceJob, lane string) bool {
	now := metav1.NewTime(r.now())
	claimed := j.DeepCopy()
	claimed.Status.Phase = aiv1alpha2.InferenceJobInProgress
	claimed.Status.Runner = r.cfg.RunnerID
	claimed.Status.HeartbeatAt = &now
	if claimed.Status.QueuedAt == nil {
		created := j.CreationTimestamp
		claimed.Status.QueuedAt = &created
	}
	if claimed.Status.StartedAt == nil {
		claimed.Status.StartedAt = &now
	}
	claimed.Status.Attempts++
	claimed.Status.Progress = jobProgressClaimed
	claimed.Status.Lane = lane
	if err := r.p.client.Status().Update(ctx, claimed); err != nil {
		slog.Debug("job runner: claim lost", "job", j.Name, "error", err)
		return false
	}
	*j = *claimed
	if !controllerutil.ContainsFinalizer(j, jobFinalizer) {
		patch := client.MergeFrom(j.DeepCopy())
		controllerutil.AddFinalizer(j, jobFinalizer)
		if err := r.p.client.Patch(ctx, j, patch); err != nil {
			slog.Warn("job runner: adding the storage finalizer failed", "job", j.Name, "error", err)
		}
	}
	slog.Info("job claimed", "job", j.Name, "model", j.Spec.Model, "lane", lane, "attempt", j.Status.Attempts, "runner", r.cfg.RunnerID)
	r.deliver(j, jobEventInProgress)
	return true
}

// start runs a claimed job in its own goroutine.
func (r *jobRunner) start(j *aiv1alpha2.InferenceJob) {
	base := r.p.ctx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithCancel(base)
	r.mu.Lock()
	r.running[j.Name] = cancel
	r.mu.Unlock()
	go func() {
		defer func() {
			cancel()
			r.mu.Lock()
			delete(r.running, j.Name)
			delete(r.cancelled, j.Name)
			r.mu.Unlock()
			r.wake()
		}()
		r.execute(ctx, j)
	}()
}

// jobAttempt is one forward of a job's request.
type jobAttempt struct {
	status     int
	header     http.Header
	spool      *jobSpoolWriter
	code       string
	message    string
	retryAfter time.Duration
}

// retryable reports whether waiting can fix the attempt's failure.
func (a *jobAttempt) retryable(serverErrs int) bool {
	switch a.status {
	case http.StatusServiceUnavailable, http.StatusGatewayTimeout, http.StatusBadGateway:
		return a.code != validation.CodeModelFailed
	case http.StatusTooManyRequests:
		return true
	case http.StatusInternalServerError:
		return serverErrs < jobMaxServerErrRetries
	}
	return false
}

// stampsDemand reports whether the wait before a retry should keep the lane
// demanded: a parked lane is never promoted by demand, so it is left alone.
func (a *jobAttempt) stampsDemand() bool {
	return a.code != validation.CodeModelParked
}

// execute runs j until it reaches a terminal phase, or until the proxy stops
// (the job is then reclaimed by the next runner once its heartbeat is stale).
func (r *jobRunner) execute(ctx context.Context, j *aiv1alpha2.InferenceJob) {
	var progress atomic.Int32
	progress.Store(jobProgressClaimed)
	// Stopped before every terminal write, so a late heartbeat can never
	// overwrite the final progress.
	stopHeartbeat := sync.OnceFunc(r.heartbeat(ctx, j, &progress))
	defer stopHeartbeat()

	deadline := jobQueuedAt(j).Add(r.cfg.MaxWait)
	serverErrs := 0
	for {
		a, err := r.attempt(ctx, j, &progress)
		if r.wasCancelled(j.Name) {
			a.close()
			stopHeartbeat()
			r.finish(context.Background(), j.Name, aiv1alpha2.InferenceJobCancelled, nil, nil)
			return
		}
		if ctx.Err() != nil {
			a.close()
			return
		}
		if err != nil {
			a.close()
			stopHeartbeat()
			r.finish(ctx, j.Name, aiv1alpha2.InferenceJobFailed, nil, &aiv1alpha2.InferenceJobError{Code: "job_request_unavailable", Message: err.Error()})
			return
		}
		if a.status/100 == 2 {
			r.complete(ctx, j, a, stopHeartbeat)
			return
		}
		if a.status == http.StatusInternalServerError {
			serverErrs++
		}
		if !a.retryable(serverErrs) || r.now().After(deadline) {
			a.close()
			msg := a.message
			if a.retryable(serverErrs) {
				msg = fmt.Sprintf("lane not available within %s: %s", r.cfg.MaxWait, msg)
			}
			stopHeartbeat()
			r.finish(ctx, j.Name, aiv1alpha2.InferenceJobFailed, nil, &aiv1alpha2.InferenceJobError{Code: a.code, Message: msg, Status: int32(a.status)})
			return
		}
		a.close()
		slog.Info("job waiting for its lane", "job", j.Name, "status", a.status, "code", a.code, "retry_after", a.retryAfter)
		if !r.waitToRetry(ctx, j, a) {
			continue
		}
	}
}

func (r *jobRunner) wasCancelled(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cancelled[name]
}

// waitToRetry sleeps for the attempt's Retry-After, keeping the lane demanded
// meanwhile. It returns false when ctx ended.
func (r *jobRunner) waitToRetry(ctx context.Context, j *aiv1alpha2.InferenceJob, a *jobAttempt) bool {
	wait := min(max(a.retryAfter, jobRetryMinWait), max(jobRetryMax, jobRetryMinWait))
	lane := r.p.jobLaneModel(ctx, j.Spec.Model)
	timer := time.NewTimer(wait)
	defer timer.Stop()
	tick := time.NewTicker(jobDemandInterval)
	defer tick.Stop()
	stamp := func() {
		if lane != nil && a.stampsDemand() && r.p.activator != nil {
			r.p.activator.TouchLastActiveTime(ctx, lane.Name)
		}
	}
	stamp()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return true
		case <-tick.C:
			stamp()
		}
	}
}

// attempt forwards j's request once through the proxy's handler.
func (r *jobRunner) attempt(ctx context.Context, j *aiv1alpha2.InferenceJob, progress *atomic.Int32) (*jobAttempt, error) {
	body, contentType, err := r.requestBody(ctx, j)
	if err != nil {
		return &jobAttempt{}, err
	}
	defer func() { _ = body.Close() }()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://flexinfer-jobs"+j.Spec.Path, body)
	if err != nil {
		return &jobAttempt{}, err
	}
	req.RemoteAddr = "job/" + j.Name
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-Request-ID", fmt.Sprintf("%s-%d", j.Name, j.Status.Attempts))
	if ua := j.Annotations[jobAnnotationUserAgent]; ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	if caller := j.Annotations[jobAnnotationCaller]; caller != "" {
		req.Header.Set(headerCaller, caller)
	}
	if j.Spec.QoSClass != "" {
		req.Header.Set(headerQoS, j.Spec.QoSClass)
	}
	if r.p.authEnabled && r.p.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+r.p.authToken)
	}
	spool, err := newJobSpoolWriter(r.cfg.SpoolDir, r.cfg.MaxOutputBytes, func() {
		if progress.Load() < jobProgressFirstByte {
			progress.Store(jobProgressFirstByte)
		}
	})
	if err != nil {
		return &jobAttempt{}, err
	}
	r.handler.ServeHTTP(spool, req)
	a := &jobAttempt{status: spool.status, header: spool.header, spool: spool}
	if a.status == 0 {
		// Only a canceled request leaves the proxy's handler without an
		// answer; anything else is treated as a lost response and retried.
		a.status, a.code, a.message = http.StatusBadGateway, "no_response", "the proxy wrote no response"
		return a, nil
	}
	if a.status/100 != 2 {
		a.code, a.message = spool.errorBody()
		if secs, err := strconv.Atoi(spool.header.Get("Retry-After")); err == nil && secs > 0 {
			a.retryAfter = time.Duration(secs) * time.Second
		}
	}
	return a, nil
}

func (a *jobAttempt) close() {
	if a != nil && a.spool != nil {
		a.spool.discard()
	}
}

// requestBody opens the job's request body: inline, from its ConfigMap or
// Secret ref, or from the stored "body" input.
func (r *jobRunner) requestBody(ctx context.Context, j *aiv1alpha2.InferenceJob) (io.ReadCloser, string, error) {
	contentType := j.Spec.ContentType
	if contentType == "" {
		contentType = "application/json"
	}
	if j.Spec.Request != nil && len(j.Spec.Request.Raw) > 0 {
		return io.NopCloser(bytes.NewReader(j.Spec.Request.Raw)), "application/json", nil
	}
	if ref := j.Spec.RequestRef; ref != nil {
		key := client.ObjectKey{Namespace: r.p.namespace, Name: ref.Name}
		var data []byte
		switch ref.Kind {
		case "Secret":
			var s corev1.Secret
			if err := r.p.client.Get(ctx, key, &s); err != nil {
				return nil, "", fmt.Errorf("request secret %s: %w", ref.Name, err)
			}
			data = s.Data[ref.Key]
		default:
			var cm corev1.ConfigMap
			if err := r.p.client.Get(ctx, key, &cm); err != nil {
				return nil, "", fmt.Errorf("request configmap %s: %w", ref.Name, err)
			}
			data = []byte(cm.Data[ref.Key])
			if data == nil {
				data = cm.BinaryData[ref.Key]
			}
		}
		if len(data) == 0 {
			return nil, "", fmt.Errorf("request %s %s has no key %q", ref.Kind, ref.Name, ref.Key)
		}
		return io.NopCloser(bytes.NewReader(data)), contentType, nil
	}
	for _, in := range j.Spec.InputRefs {
		if in.Name == jobStoredBodyInput {
			rc, err := r.cfg.Store.Get(ctx, in.URI)
			if err != nil {
				return nil, "", fmt.Errorf("stored request body: %w", err)
			}
			if in.ContentType != "" {
				contentType = in.ContentType
			}
			return rc, contentType, nil
		}
	}
	return nil, "", errors.New("job has no request body")
}

// complete stores a 2xx response as the job's output; stopHeartbeat runs once
// the upload is done, before the terminal write.
func (r *jobRunner) complete(ctx context.Context, j *aiv1alpha2.InferenceJob, a *jobAttempt, stopHeartbeat func()) {
	defer a.close()
	defer stopHeartbeat()
	contentType := a.header.Get("Content-Type")
	if a.spool.overflow {
		stopHeartbeat()
		r.finish(ctx, j.Name, aiv1alpha2.InferenceJobIncomplete, nil, &aiv1alpha2.InferenceJobError{
			Code: "output_too_large", Message: fmt.Sprintf("response exceeded %d bytes", r.cfg.MaxOutputBytes), Status: int32(a.status),
		})
		return
	}
	file, size, sum, err := a.spool.reader()
	if err != nil {
		stopHeartbeat()
		r.finish(ctx, j.Name, aiv1alpha2.InferenceJobIncomplete, nil, &aiv1alpha2.InferenceJobError{Code: "storage_error", Message: err.Error()})
		return
	}
	uri, err := r.cfg.Store.Put(ctx, jobObjectKey(r.p.namespace, j.Name, jobStoredOutputName), file, size, sum, contentType)
	stopHeartbeat()
	if err != nil {
		slog.Warn("job output store failed", "job", j.Name, "error", err)
		r.finish(ctx, j.Name, aiv1alpha2.InferenceJobIncomplete, nil, &aiv1alpha2.InferenceJobError{Code: "storage_error", Message: err.Error()})
		return
	}
	r.finish(ctx, j.Name, aiv1alpha2.InferenceJobCompleted, &aiv1alpha2.InferenceJobObject{
		URI: uri, ContentType: contentType, Bytes: size, SHA256: sum,
	}, nil)
}

// finish records a terminal phase and sends its webhook.
func (r *jobRunner) finish(ctx context.Context, name string, phase aiv1alpha2.InferenceJobPhase, output *aiv1alpha2.InferenceJobObject, jobErr *aiv1alpha2.InferenceJobError) {
	var done *aiv1alpha2.InferenceJob
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		j := &aiv1alpha2.InferenceJob{}
		if err := r.p.client.Get(ctx, client.ObjectKey{Namespace: r.p.namespace, Name: name}, j); err != nil {
			return err
		}
		if j.Status.Phase.IsTerminal() {
			done = j
			return nil
		}
		now := metav1.NewTime(r.now())
		ttl := defaultJobTTL
		if j.Spec.TTLAfterFinished != nil && j.Spec.TTLAfterFinished.Duration > 0 {
			ttl = j.Spec.TTLAfterFinished.Duration
		}
		expires := metav1.NewTime(now.Add(ttl))
		j.Status.Phase = phase
		j.Status.CompletedAt = &now
		j.Status.ExpiresAt = &expires
		j.Status.Output = output
		j.Status.Error = jobErr
		if phase == aiv1alpha2.InferenceJobCompleted {
			j.Status.Progress = 100
		}
		if err := r.p.client.Status().Update(ctx, j); err != nil {
			return err
		}
		done = j
		return nil
	})
	if err != nil {
		slog.Warn("job runner: recording the result failed", "job", name, "phase", phase, "error", err)
		return
	}
	slog.Info("job finished", "job", name, "phase", done.Status.Phase, "lane", done.Status.Lane)
	r.deliver(done, "job."+string(done.Status.Phase))
}

// heartbeat stamps status.heartbeatAt and progress until stopped.
func (r *jobRunner) heartbeat(ctx context.Context, j *aiv1alpha2.InferenceJob, progress *atomic.Int32) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(jobHeartbeatInterval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-tick.C:
			}
			if lane := r.p.jobLaneModel(ctx, j.Spec.Model); lane != nil &&
				lane.Status.Phase == aiv1alpha2.ModelPhaseReady && progress.Load() < jobProgressLaneReady {
				progress.Store(jobProgressLaneReady)
			}
			patch := fmt.Sprintf(`{"status":{"heartbeatAt":%q,"progress":%d}}`,
				r.now().UTC().Format(time.RFC3339), progress.Load())
			obj := &aiv1alpha2.InferenceJob{ObjectMeta: metav1.ObjectMeta{Namespace: j.Namespace, Name: j.Name}}
			if err := r.p.client.Status().Patch(ctx, obj, client.RawPatch(types.MergePatchType, []byte(patch))); err != nil {
				slog.Debug("job heartbeat failed", "job", j.Name, "error", err)
			}
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}

// finalize deletes a deleted job's stored objects, then lets it go.
func (r *jobRunner) finalize(ctx context.Context, j *aiv1alpha2.InferenceJob) {
	if !controllerutil.ContainsFinalizer(j, jobFinalizer) {
		return
	}
	r.cancelRunning(j.Name)
	uris := []string{}
	for _, in := range j.Spec.InputRefs {
		uris = append(uris, in.URI)
	}
	if j.Status.Output != nil {
		uris = append(uris, j.Status.Output.URI)
	}
	for _, uri := range uris {
		if err := r.cfg.Store.Delete(ctx, uri); err != nil {
			slog.Warn("job runner: deleting a stored object failed; retrying next pass", "job", j.Name, "uri", uri, "error", err)
			return
		}
	}
	patch := client.MergeFrom(j.DeepCopy())
	controllerutil.RemoveFinalizer(j, jobFinalizer)
	if err := r.p.client.Patch(ctx, j, patch); err != nil {
		slog.Debug("job runner: removing the storage finalizer failed", "job", j.Name, "error", err)
	}
}

// ── webhooks ─────────────────────────────────────────────────────

// deliver sends event for j in the background, retrying on the schedule and
// recording each attempt; an event already delivered is not sent again.
func (r *jobRunner) deliver(j *aiv1alpha2.InferenceJob, event string) {
	wh := j.Spec.Webhook
	if wh == nil || !jobWebhookWants(wh.Events, event) {
		return
	}
	done := 0
	if st := j.Status.Webhook; st != nil {
		for _, d := range st.Delivered {
			if d == event {
				return
			}
		}
		done = int(st.Attempts)
	}
	id := j.Name + "." + event
	r.mu.Lock()
	if r.delivering[id] {
		r.mu.Unlock()
		return
	}
	r.delivering[id] = true
	r.mu.Unlock()
	view := jobView(j, nil, nil)
	go func() {
		defer func() {
			r.mu.Lock()
			delete(r.delivering, id)
			r.mu.Unlock()
		}()
		ctx := r.p.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		secret := r.webhookSecret(ctx, j)
		body, err := marshalJobWebhookEvent(event, r.now(), view)
		if err != nil {
			return
		}
		schedule := jobWebhookRetrySchedule
		if done > 0 && event != jobEventInProgress && done < len(schedule) {
			schedule = schedule[done:]
		}
		for _, wait := range schedule {
			if wait > 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(wait):
				}
			}
			err := deliverWebhook(ctx, r.hooks, wh.URL, secret, id, r.now(), body)
			r.recordDelivery(ctx, j.Name, event, err)
			if err == nil {
				return
			}
			slog.Info("job webhook delivery failed", "job", j.Name, "event", event, "error", err)
		}
	}()
}

// resumeWebhook re-sends a terminal job's event after a proxy restart.
func (r *jobRunner) resumeWebhook(_ context.Context, j *aiv1alpha2.InferenceJob) {
	if j.Spec.Webhook == nil {
		return
	}
	if st := j.Status.Webhook; st != nil && int(st.Attempts) >= len(jobWebhookRetrySchedule) {
		return
	}
	r.deliver(j, "job."+string(j.Status.Phase))
}

func (r *jobRunner) webhookSecret(ctx context.Context, j *aiv1alpha2.InferenceJob) []byte {
	ref := j.Spec.Webhook.SecretRef
	if ref == nil {
		return nil
	}
	var s corev1.Secret
	if err := r.p.client.Get(ctx, client.ObjectKey{Namespace: r.p.namespace, Name: ref.Name}, &s); err != nil {
		slog.Warn("job webhook secret unreadable; delivering unsigned", "job", j.Name, "error", err)
		return nil
	}
	return webhookSecretBytes(string(s.Data[ref.Key]))
}

func (r *jobRunner) recordDelivery(ctx context.Context, name, event string, deliveryErr error) {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		j := &aiv1alpha2.InferenceJob{}
		if err := r.p.client.Get(ctx, client.ObjectKey{Namespace: r.p.namespace, Name: name}, j); err != nil {
			return err
		}
		if j.Status.Webhook == nil {
			j.Status.Webhook = &aiv1alpha2.InferenceJobWebhookStatus{}
		}
		now := metav1.NewTime(r.now())
		st := j.Status.Webhook
		st.Attempts++
		st.LastAttemptAt = &now
		if deliveryErr != nil {
			st.LastError = deliveryErr.Error()
		} else {
			st.LastError = ""
			st.Delivered = append(st.Delivered, event)
		}
		return r.p.client.Status().Update(ctx, j)
	})
	if err != nil {
		slog.Debug("job webhook status write failed", "job", name, "error", err)
	}
}

// ── response spool ───────────────────────────────────────────────

// jobSpoolWriter is the ResponseWriter a job's forward writes into: headers
// and status in memory, the body in a temporary file with its SHA-256.
type jobSpoolWriter struct {
	header    http.Header
	status    int
	file      *os.File
	hash      hash.Hash
	size      int64
	max       int64
	overflow  bool
	firstByte func()
	errBuf    bytes.Buffer
}

func newJobSpoolWriter(dir string, maxBytes int64, firstByte func()) (*jobSpoolWriter, error) {
	f, err := os.CreateTemp(dir, "flexinfer-job-*")
	if err != nil {
		return nil, fmt.Errorf("job spool: %w", err)
	}
	return &jobSpoolWriter{header: http.Header{}, file: f, hash: sha256.New(), max: maxBytes, firstByte: firstByte}, nil
}

func (s *jobSpoolWriter) Header() http.Header { return s.header }

func (s *jobSpoolWriter) WriteHeader(code int) {
	if s.status != 0 || code < http.StatusOK {
		return
	}
	s.status = code
	if s.firstByte != nil {
		s.firstByte()
	}
}

func (s *jobSpoolWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.WriteHeader(http.StatusOK)
	}
	if s.status/100 != 2 {
		if room := jobMaxErrorBodyBytes - s.errBuf.Len(); room > 0 {
			s.errBuf.Write(b[:min(len(b), room)])
		}
		return len(b), nil
	}
	if s.overflow || s.size+int64(len(b)) > s.max {
		s.overflow = true
		return len(b), nil
	}
	n, err := s.file.Write(b)
	s.hash.Write(b[:n])
	s.size += int64(n)
	return n, err
}

// Flush satisfies http.Flusher for streamed upstream bodies.
func (s *jobSpoolWriter) Flush() {}

// reader rewinds the spooled body for storing.
func (s *jobSpoolWriter) reader() (*os.File, int64, string, error) {
	if _, err := s.file.Seek(0, io.SeekStart); err != nil {
		return nil, 0, "", err
	}
	return s.file, s.size, hex.EncodeToString(s.hash.Sum(nil)), nil
}

// errorBody parses a non-2xx body as an OpenAI error.
func (s *jobSpoolWriter) errorBody() (code, message string) {
	var e struct {
		Error struct {
			Code    any    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Detail any `json:"detail"`
	}
	raw := s.errBuf.Bytes()
	if json.Unmarshal(raw, &e) == nil {
		switch c := e.Error.Code.(type) {
		case string:
			code = c
		case float64:
			code = strconv.Itoa(int(c))
		}
		message = e.Error.Message
		if message == "" && e.Detail != nil {
			if d, err := json.Marshal(e.Detail); err == nil {
				message = string(d)
			}
		}
	}
	if code == "" {
		code = "http_" + strconv.Itoa(s.status)
	}
	if message == "" {
		message = strings.TrimSpace(string(raw))
	}
	return code, message
}

func (s *jobSpoolWriter) discard() {
	if s.file == nil {
		return
	}
	_ = s.file.Close()
	_ = os.Remove(s.file.Name())
	s.file = nil
}
