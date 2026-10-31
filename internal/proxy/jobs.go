package proxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/envutil"
	"github.com/flexinfer/flexinfer/pkg/validation"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Async jobs (D2-C of .loom/10-research-gfx1100-gfx906-next-differentiators-
// 2026-10-31.md §6.2). Long work gets an async home: `background: true` (or
// `Prefer: respond-async`) on an inference path, or POST /v1/jobs, creates an
// InferenceJob and answers at once with the job object; GET /v1/jobs/{id}
// polls it, GET /v1/jobs/{id}/content returns the stored output, POST
// /v1/jobs/{id}/cancel stops it, and a Standard Webhooks endpoint can be told
// instead. The shape copies the OpenAI Responses `background` mode (OpenAI's
// own Videos API was removed on 2026-09-24). The runner (jobs_runner.go)
// executes each job through this proxy's own handler, so a job gets the same
// cold start, first-byte budget, breaker and reroutes as a synchronous call.

const (
	jobIDPrefix              = "job-"
	jobObjectType            = "job"
	defaultJobTTL            = 24 * time.Hour
	maxInlineJobRequestBytes = 256 << 10
	maxJobSubmitBytes        = 256 << 20

	jobFinalizer = "flexinfer.ai/job-storage"

	jobAnnotationCaller    = "flexinfer.ai/job-caller"
	jobAnnotationUserAgent = "flexinfer.ai/job-user-agent"
	jobLabelIdempotency    = "flexinfer.ai/job-idempotency"

	headerJobWebhookURL    = "X-FlexInfer-Webhook-Url"
	headerJobWebhookSecret = "X-FlexInfer-Webhook-Secret"
	headerIdempotencyKey   = "Idempotency-Key"

	codeJobsDisabled     = "jobs_disabled"
	codeJobNotFound      = "job_not_found"
	codeJobNotCompleted  = "job_not_completed"
	codeJobStreamUnsupp  = "job_stream_unsupported"
	jobWebhookSecretKey  = "secret"
	jobStoredBodyInput   = "body"
	jobStoredOutputName  = "output"
	jobStoredInputPrefix = "input-"
)

// jobBackgroundPaths are the inference paths where `background: true` or
// `Prefer: respond-async` turns a request into a job. POST /v1/jobs accepts
// any /v1/ inference path.
var jobBackgroundPaths = map[string]bool{
	"/v1/chat/completions":     true,
	"/v1/images/generations":   true,
	"/v1/images/edits":         true,
	"/v1/audio/transcriptions": true,
	"/v1/audio/speech":         true,
	"/v1/audio/generations":    true,
	"/v1/videos/generations":   true,
}

// jobSiblingPaths are served by sibling services (Kokoro), not a Model CR.
var jobSiblingPaths = map[string]bool{"/v1/audio/speech": true}

// JobsConfig enables the job API and runner.
type JobsConfig struct {
	Enabled bool
	// Store keeps inputs and outputs; StoreErr says why it could not be built.
	Store    JobStore
	StoreErr error
	// SpoolDir holds a running job's response until it is stored.
	SpoolDir string
	// PerModel bounds running jobs per lane (diffusers lanes, and all
	// diffusers lanes sharing one GPU, run one at a time).
	PerModel int
	// MaxRunning bounds running jobs per proxy replica.
	MaxRunning int
	// PollInterval is how often the runner lists jobs.
	PollInterval time.Duration
	// MaxOutputBytes caps a stored output; a larger one ends incomplete.
	MaxOutputBytes int64
	// MaxWait is how long a job may wait for its lane (cold starts, open
	// breakers, parked or preempted lanes) before it fails.
	MaxWait time.Duration
	// RunnerID names this replica in status.runner (POD_NAME).
	RunnerID string
}

// jobsConfigFromEnv reads FLEXINFER_JOBS_*.
func jobsConfigFromEnv() JobsConfig {
	cfg := JobsConfig{
		Enabled:        envutil.BoolOrDefault("FLEXINFER_JOBS_ENABLED", false),
		SpoolDir:       os.Getenv("FLEXINFER_JOBS_SPOOL_DIR"),
		PerModel:       envutil.IntOrDefault("FLEXINFER_JOBS_PER_MODEL", 2),
		MaxRunning:     envutil.IntOrDefault("FLEXINFER_JOBS_MAX_RUNNING", 8),
		PollInterval:   envutil.DurationOrDefault("FLEXINFER_JOBS_POLL_INTERVAL", 5*time.Second),
		MaxOutputBytes: parseByteSize(os.Getenv("FLEXINFER_JOBS_MAX_OUTPUT_BYTES"), 1<<30),
		MaxWait:        envutil.DurationOrDefault("FLEXINFER_JOBS_MAX_WAIT", 6*time.Hour),
		RunnerID:       os.Getenv("POD_NAME"),
	}
	if cfg.Enabled {
		cfg.Store, cfg.StoreErr = storeFromEnv(os.Getenv("FLEXINFER_JOBS_STORE"), os.Getenv)
	}
	return cfg
}

func (c JobsConfig) validate() error {
	if !c.Enabled {
		return nil
	}
	if c.Store == nil {
		return fmt.Errorf("FLEXINFER_JOBS_ENABLED=true but the job store is not usable: %w", c.StoreErr)
	}
	if c.PerModel < 1 || c.MaxRunning < 1 || c.PollInterval <= 0 || c.MaxWait <= 0 {
		return errors.New("FLEXINFER_JOBS_PER_MODEL, _MAX_RUNNING, _POLL_INTERVAL and _MAX_WAIT must be positive")
	}
	return nil
}

// ── job object ───────────────────────────────────────────────────

// jobObject is the API view of an InferenceJob, after the Responses
// background object: id, status, timestamps in Unix seconds, error, output.
type jobObject struct {
	ID          string        `json:"id"`
	Object      string        `json:"object"`
	CreatedAt   int64         `json:"created_at"`
	Status      string        `json:"status"`
	Background  bool          `json:"background"`
	Model       string        `json:"model"`
	Path        string        `json:"path"`
	Progress    int32         `json:"progress"`
	StartedAt   *int64        `json:"started_at"`
	CompletedAt *int64        `json:"completed_at"`
	ExpiresAt   *int64        `json:"expires_at"`
	Error       *jobError     `json:"error"`
	Output      *jobOutput    `json:"output"`
	FlexInfer   jobFlexInfer  `json:"flexinfer"`
	Webhook     *jobWebhookUI `json:"webhook,omitempty"`
}

type jobError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int32  `json:"status,omitempty"`
}

type jobOutput struct {
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
	Bytes       int64  `json:"bytes"`
	SHA256      string `json:"sha256"`
}

// jobFlexInfer carries the lane-side detail: what served it, where it waits
// and how long its lane needs.
type jobFlexInfer struct {
	Lane          *string `json:"lane"`
	QoSClass      *string `json:"qos_class"`
	QueuePosition *int    `json:"queue_position"`
	ETASeconds    *int    `json:"eta_seconds"`
	Attempts      int32   `json:"attempts"`
	Runner        *string `json:"runner"`
}

type jobWebhookUI struct {
	URL       string   `json:"url"`
	Events    []string `json:"events"`
	Delivered []string `json:"delivered"`
}

func unixPtr(t *metav1.Time) *int64 {
	if t == nil {
		return nil
	}
	v := t.Unix()
	return &v
}

func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// jobView renders j. position is its place among queued jobs for its lane
// (nil when not queued); eta the lane's ETA when the job waits on it.
func jobView(j *aiv1alpha2.InferenceJob, position, eta *int) jobObject {
	phase := j.Status.Phase
	if phase == "" {
		phase = aiv1alpha2.InferenceJobQueued
	}
	obj := jobObject{
		ID:          j.Name,
		Object:      jobObjectType,
		CreatedAt:   j.CreationTimestamp.Unix(),
		Status:      string(phase),
		Background:  true,
		Model:       j.Spec.Model,
		Path:        j.Spec.Path,
		Progress:    j.Status.Progress,
		StartedAt:   unixPtr(j.Status.StartedAt),
		CompletedAt: unixPtr(j.Status.CompletedAt),
		ExpiresAt:   unixPtr(j.Status.ExpiresAt),
		FlexInfer: jobFlexInfer{
			Lane:          strPtrOrNil(j.Status.Lane),
			QoSClass:      strPtrOrNil(j.Spec.QoSClass),
			QueuePosition: position,
			ETASeconds:    eta,
			Attempts:      j.Status.Attempts,
			Runner:        strPtrOrNil(j.Status.Runner),
		},
	}
	if e := j.Status.Error; e != nil {
		obj.Error = &jobError{Code: e.Code, Message: e.Message, Status: e.Status}
	}
	if o := j.Status.Output; o != nil && phase == aiv1alpha2.InferenceJobCompleted {
		obj.Output = &jobOutput{URL: "/v1/jobs/" + j.Name + "/content", ContentType: o.ContentType, Bytes: o.Bytes, SHA256: o.SHA256}
	}
	if wh := j.Spec.Webhook; wh != nil {
		ui := &jobWebhookUI{URL: wh.URL, Events: wh.Events, Delivered: []string{}}
		if len(ui.Events) == 0 {
			ui.Events = defaultJobWebhookEvents
		}
		if j.Status.Webhook != nil && j.Status.Webhook.Delivered != nil {
			ui.Delivered = j.Status.Webhook.Delivered
		}
		obj.Webhook = ui
	}
	return obj
}

// describeJob renders j with its queue position and lane ETA when it waits.
func (p *Proxy) describeJob(ctx context.Context, j *aiv1alpha2.InferenceJob) jobObject {
	var position, eta *int
	if !j.Status.Phase.IsTerminal() {
		lane := p.jobLaneModel(ctx, j.Spec.Model)
		if lane != nil && lane.Status.Phase != aiv1alpha2.ModelPhaseReady {
			eta = p.loadETA(lane, time.Now())
		}
		if j.Status.Phase == "" || j.Status.Phase == aiv1alpha2.InferenceJobQueued {
			var list aiv1alpha2.InferenceJobList
			if err := p.client.List(ctx, &list, client.InNamespace(p.namespace)); err == nil {
				pos := jobQueuePosition(list.Items, j, p.jobKeyFunc(ctx))
				position = &pos
			}
		}
	}
	return jobView(j, position, eta)
}

// jobQueuePosition counts the queued jobs on j's concurrency key that run
// before j (0 = next).
func jobQueuePosition(items []aiv1alpha2.InferenceJob, j *aiv1alpha2.InferenceJob, keyOf func(*aiv1alpha2.InferenceJob) string) int {
	key := keyOf(j)
	pos := 0
	for i := range items {
		other := &items[i]
		if other.Name == j.Name || keyOf(other) != key {
			continue
		}
		if phase := other.Status.Phase; phase != "" && phase != aiv1alpha2.InferenceJobQueued {
			continue
		}
		if jobBefore(other, j) {
			pos++
		}
	}
	return pos
}

// jobQueuedAt is when j entered the queue.
func jobQueuedAt(j *aiv1alpha2.InferenceJob) time.Time {
	if j.Status.QueuedAt != nil {
		return j.Status.QueuedAt.Time
	}
	return j.CreationTimestamp.Time
}

// jobBefore orders jobs first-come first-served, by name on ties.
func jobBefore(a, b *aiv1alpha2.InferenceJob) bool {
	ta, tb := jobQueuedAt(a), jobQueuedAt(b)
	if !ta.Equal(tb) {
		return ta.Before(tb)
	}
	return a.Name < b.Name
}

// ── HTTP API ─────────────────────────────────────────────────────

func writeJobsDisabled(w http.ResponseWriter) {
	validation.WriteError(w, http.StatusNotImplemented,
		"async jobs are not enabled on this proxy (FLEXINFER_JOBS_ENABLED)", validation.ErrorTypeInvalidRequest, codeJobsDisabled)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Debug("job response write failed", "error", err)
	}
}

// jobsGate answers for the job endpoints when jobs are off or the caller is
// not authenticated; it reports whether the handler may go on.
func (p *Proxy) jobsGate(w http.ResponseWriter, r *http.Request) bool {
	if !p.checkAuth(r) {
		validation.WriteUnauthorized(w, "Invalid or missing bearer token")
		return false
	}
	if p.jobs == nil {
		writeJobsDisabled(w)
		return false
	}
	return true
}

// createJobRequest is the POST /v1/jobs body.
type createJobRequest struct {
	Model          string          `json:"model"`
	Path           string          `json:"path"`
	Request        json.RawMessage `json:"request"`
	Webhook        *jobWebhookIn   `json:"webhook"`
	TTLSeconds     int64           `json:"ttl_seconds"`
	IdempotencyKey string          `json:"idempotency_key"`
	QoSClass       string          `json:"qos_class"`
}

type jobWebhookIn struct {
	URL    string   `json:"url"`
	Secret string   `json:"secret"`
	Events []string `json:"events"`
}

// handleCreateJob is POST /v1/jobs: a JSON request for any /v1/ path.
func (p *Proxy) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	if !p.jobsGate(w, r) {
		return
	}
	var in createJobRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxJobSubmitBytes)).Decode(&in); err != nil {
		validation.WriteBadRequest(w, "invalid job request: "+err.Error())
		return
	}
	if !strings.HasPrefix(in.Path, "/v1/") || strings.HasPrefix(in.Path, "/v1/jobs") || strings.HasPrefix(in.Path, "/v1/models") {
		validation.WriteInvalidFieldValue(w, "path", "must be a /v1/ inference path")
		return
	}
	if len(in.Request) == 0 || in.Request[0] != '{' {
		validation.WriteInvalidFieldValue(w, "request", "must be a JSON object (the body of the inference request)")
		return
	}
	body, model, err := stripBackgroundJSON(in.Request)
	if err != nil {
		validation.WriteInvalidFieldValue(w, "request", err.Error())
		return
	}
	if in.Model == "" {
		in.Model = model
	}
	if in.Model == "" {
		validation.WriteMissingField(w, "model")
		return
	}
	sub := jobSubmission{
		model: in.Model, path: in.Path, body: body, contentType: "application/json",
		webhook: in.Webhook, ttl: time.Duration(in.TTLSeconds) * time.Second,
		idempotencyKey: in.IdempotencyKey, qosClass: in.QoSClass,
	}
	p.submitJob(w, r, sub)
}

// handleGetJob is GET /v1/jobs/{id}.
func (p *Proxy) handleGetJob(w http.ResponseWriter, r *http.Request) {
	if !p.jobsGate(w, r) {
		return
	}
	j, ok := p.lookupJob(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, p.describeJob(r.Context(), j))
}

// handleListJobs is GET /v1/jobs?status=&model=&limit=.
func (p *Proxy) handleListJobs(w http.ResponseWriter, r *http.Request) {
	if !p.jobsGate(w, r) {
		return
	}
	var list aiv1alpha2.InferenceJobList
	if err := p.client.List(r.Context(), &list, client.InNamespace(p.namespace)); err != nil {
		validation.WriteInternalError(w, "listing jobs: "+err.Error())
		return
	}
	status, model := r.URL.Query().Get("status"), r.URL.Query().Get("model")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	items := list.Items
	sort.Slice(items, func(a, b int) bool { return items[a].CreationTimestamp.After(items[b].CreationTimestamp.Time) })
	keyOf := p.jobKeyFunc(r.Context())
	data := []jobObject{}
	for i := range items {
		j := &items[i]
		phase := string(j.Status.Phase)
		if phase == "" {
			phase = string(aiv1alpha2.InferenceJobQueued)
		}
		if (status != "" && phase != status) || (model != "" && j.Spec.Model != model && j.Status.Lane != model) {
			continue
		}
		if len(data) == limit {
			writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data, "has_more": true})
			return
		}
		var position *int
		if phase == string(aiv1alpha2.InferenceJobQueued) {
			pos := jobQueuePosition(items, j, keyOf)
			position = &pos
		}
		data = append(data, jobView(j, position, nil))
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data, "has_more": false})
}

// handleCancelJob is POST /v1/jobs/{id}/cancel. Cancelling a finished job is
// a no-op that returns it.
func (p *Proxy) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	if !p.jobsGate(w, r) {
		return
	}
	j, ok := p.lookupJob(w, r)
	if !ok {
		return
	}
	if !j.Status.Phase.IsTerminal() && !j.Spec.Cancel {
		patch := client.MergeFrom(j.DeepCopy())
		j.Spec.Cancel = true
		if err := p.client.Patch(r.Context(), j, patch); err != nil {
			validation.WriteInternalError(w, "cancelling job: "+err.Error())
			return
		}
		p.jobs.wake()
	}
	writeJSON(w, http.StatusOK, p.describeJob(r.Context(), j))
}

// handleJobContent is GET /v1/jobs/{id}/content: the stored response body.
func (p *Proxy) handleJobContent(w http.ResponseWriter, r *http.Request) {
	if !p.jobsGate(w, r) {
		return
	}
	j, ok := p.lookupJob(w, r)
	if !ok {
		return
	}
	if j.Status.Phase != aiv1alpha2.InferenceJobCompleted || j.Status.Output == nil {
		validation.WriteError(w, http.StatusConflict,
			fmt.Sprintf("job %s is %s; content is available once it is completed", j.Name, jobView(j, nil, nil).Status),
			validation.ErrorTypeInvalidRequest, codeJobNotCompleted)
		return
	}
	body, err := p.jobs.cfg.Store.Get(r.Context(), j.Status.Output.URI)
	if errors.Is(err, errJobObjectNotFound) {
		validation.WriteError(w, http.StatusGone, "job output no longer stored", validation.ErrorTypeNotFound, codeJobNotFound)
		return
	}
	if err != nil {
		validation.WriteInternalError(w, "reading job output: "+err.Error())
		return
	}
	defer func() { _ = body.Close() }()
	if ct := j.Status.Output.ContentType; ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	if j.Status.Output.Bytes > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(j.Status.Output.Bytes, 10))
	}
	if j.Status.Output.SHA256 != "" {
		w.Header().Set("ETag", `"`+j.Status.Output.SHA256+`"`)
	}
	if _, err := io.Copy(w, body); err != nil {
		slog.Debug("job content write failed", "job", j.Name, "error", err)
	}
}

func (p *Proxy) lookupJob(w http.ResponseWriter, r *http.Request) (*aiv1alpha2.InferenceJob, bool) {
	id := r.PathValue("id")
	j := &aiv1alpha2.InferenceJob{}
	if !strings.HasPrefix(id, jobIDPrefix) || !isValidModelResourceName(id) {
		validation.WriteError(w, http.StatusNotFound, "job "+id+" not found", validation.ErrorTypeNotFound, codeJobNotFound)
		return nil, false
	}
	if err := p.client.Get(r.Context(), client.ObjectKey{Namespace: p.namespace, Name: id}, j); err != nil {
		if apierrors.IsNotFound(err) {
			validation.WriteError(w, http.StatusNotFound, "job "+id+" not found", validation.ErrorTypeNotFound, codeJobNotFound)
		} else {
			validation.WriteInternalError(w, "reading job: "+err.Error())
		}
		return nil, false
	}
	return j, true
}

// ── submission ───────────────────────────────────────────────────

// jobSubmission is one job to create.
type jobSubmission struct {
	model, path    string
	body           []byte
	contentType    string
	webhook        *jobWebhookIn
	ttl            time.Duration
	idempotencyKey string
	qosClass       string
}

// jobsMiddleware turns a background request on a jobBackgroundPaths path into
// a job; every other request reaches next unchanged.
func (p *Proxy) jobsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !jobBackgroundPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		sub, background, err := readBackgroundRequest(r)
		if !background {
			next.ServeHTTP(w, r)
			return
		}
		if !p.jobsGate(w, r) {
			return
		}
		if err != nil {
			validation.WriteBadRequest(w, err.Error())
			return
		}
		if sub.model == "" {
			validation.WriteBadRequestWithCode(w, "X-Model-ID header or 'model' field required", validation.CodeMissingRequiredField)
			return
		}
		if u := r.Header.Get(headerJobWebhookURL); u != "" {
			sub.webhook = &jobWebhookIn{URL: u, Secret: r.Header.Get(headerJobWebhookSecret)}
		}
		sub.idempotencyKey = r.Header.Get(headerIdempotencyKey)
		sub.qosClass = r.Header.Get(headerQoS)
		p.submitJob(w, r, sub)
	})
}

// prefersAsync reports an RFC 7240 "Prefer: respond-async".
func prefersAsync(r *http.Request) bool {
	for _, v := range r.Header.Values("Prefer") {
		for _, pref := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(strings.SplitN(pref, ";", 2)[0]), "respond-async") {
				return true
			}
		}
	}
	return false
}

// readBackgroundRequest buffers r's body and reports whether it asks to run
// in the background. It always restores r.Body. The returned submission
// carries the body with the background marker removed.
func readBackgroundRequest(r *http.Request) (jobSubmission, bool, error) {
	ct := r.Header.Get("Content-Type")
	mediaType, params, _ := mime.ParseMediaType(ct)
	async := prefersAsync(r)
	if !async && mediaType != "application/json" && mediaType != "multipart/form-data" {
		return jobSubmission{}, false, nil
	}
	original := r.Body
	raw, err := io.ReadAll(io.LimitReader(original, maxJobSubmitBytes+1))
	if len(raw) > maxJobSubmitBytes {
		// Too large to be a job; hand the synchronous path the whole body.
		r.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(raw), original), original}
		return jobSubmission{}, async, fmt.Errorf("request body over %d bytes", maxJobSubmitBytes)
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	r.ContentLength = int64(len(raw))
	if err != nil {
		return jobSubmission{}, async, fmt.Errorf("reading request body: %w", err)
	}
	sub := jobSubmission{path: r.URL.Path, contentType: ct, model: r.Header.Get("X-Model-ID")}
	switch mediaType {
	case "application/json":
		var probe struct {
			Background bool `json:"background"`
			Stream     bool `json:"stream"`
		}
		if json.Unmarshal(raw, &probe) != nil || !(probe.Background || async) {
			return jobSubmission{}, false, nil
		}
		if probe.Stream {
			return sub, true, errors.New("background requests cannot stream yet; send stream=false and read the job's content")
		}
		body, model, err := stripBackgroundJSON(raw)
		if err != nil {
			return sub, true, err
		}
		sub.body = body
		if sub.model == "" {
			sub.model = model
		}
		return sub, true, nil
	case "multipart/form-data":
		body, model, background, err := stripBackgroundMultipart(raw, params["boundary"])
		if err != nil {
			return sub, async, err
		}
		if !background && !async {
			return jobSubmission{}, false, nil
		}
		sub.body = body
		if sub.model == "" {
			sub.model = model
		}
		return sub, true, nil
	}
	sub.body = raw
	return sub, async, nil
}

// stripBackgroundJSON drops "background" from a JSON object body and returns
// its "model".
func stripBackgroundJSON(raw []byte) ([]byte, string, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, "", fmt.Errorf("request body is not a JSON object: %w", err)
	}
	var model string
	_ = json.Unmarshal(fields["model"], &model)
	if _, ok := fields["background"]; !ok {
		return raw, model, nil
	}
	delete(fields, "background")
	out, err := json.Marshal(fields)
	return out, model, err
}

// stripBackgroundMultipart re-encodes a multipart body without its
// "background" field, keeping the boundary, and returns its "model".
func stripBackgroundMultipart(raw []byte, boundary string) ([]byte, string, bool, error) {
	if boundary == "" {
		return nil, "", false, errors.New("multipart body without a boundary")
	}
	reader := multipart.NewReader(bytes.NewReader(raw), boundary)
	var out bytes.Buffer
	writer := multipart.NewWriter(&out)
	if err := writer.SetBoundary(boundary); err != nil {
		return nil, "", false, err
	}
	var model string
	background := false
	for {
		part, err := reader.NextRawPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, "", false, fmt.Errorf("reading multipart body: %w", err)
		}
		data, err := io.ReadAll(part)
		if err != nil {
			return nil, "", false, err
		}
		switch part.FormName() {
		case "background":
			v := strings.ToLower(strings.TrimSpace(string(data)))
			background = v == "true" || v == "1"
			continue
		case "model":
			model = strings.TrimSpace(string(data))
		}
		pw, err := writer.CreatePart(textproto.MIMEHeader(part.Header))
		if err != nil {
			return nil, "", false, err
		}
		if _, err := pw.Write(data); err != nil {
			return nil, "", false, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", false, err
	}
	if !background {
		return raw, model, false, nil
	}
	return out.Bytes(), model, true, nil
}

// checkJobTarget answers the 404s a synchronous request for model on path
// would get, at submission instead of when the job runs. It reports whether
// the job may be created.
func (p *Proxy) checkJobTarget(w http.ResponseWriter, r *http.Request, model, path string) bool {
	ctx := r.Context()
	if p.resolver != nil {
		if members, isLabel := p.resolver.ResolveServiceLabelGroup(ctx, model); isLabel && len(members) > 0 {
			return true
		}
	}
	m := p.jobLaneModel(ctx, model)
	if m == nil {
		if jobSiblingPaths[path] {
			return true
		}
		name := model
		if p.resolver != nil {
			name = p.resolver.ResolveModelAlias(ctx, model)
		}
		if isValidModelResourceName(name) {
			if _, err := p.getModelDeployment(ctx, name); err == nil {
				return true
			}
		}
		validation.WriteModelNotFound(w, model)
		return false
	}
	if litellmExplicitlyDisabled(m) {
		p.writeLaneError(w, m, laneError{
			status: http.StatusNotFound, errType: validation.ErrorTypeNotFound, code: validation.CodeModelParked,
			message: fmt.Sprintf("Model '%s' not found: parked (spec.litellm.enabled=false) and not served by name", model),
			param:   "model", retryAfter: -1,
		})
		return false
	}
	if !modelServesPath(m, path) {
		validation.WriteModelNotFound(w, model)
		return false
	}
	return true
}

// jobLaneModel resolves a job's model name (alias, served name, LoRA adapter
// parent) to its Model, nil when there is none.
func (p *Proxy) jobLaneModel(ctx context.Context, model string) *aiv1alpha2.Model {
	if p.client == nil {
		return nil
	}
	name := model
	if p.resolver != nil {
		name = p.resolver.ResolveModelAlias(ctx, model)
		if parent, isLoRA := p.resolver.ResolveLoRAAdapter(ctx, name); isLoRA {
			name = parent
		}
	}
	if !isValidModelResourceName(name) {
		return nil
	}
	return p.peekModel(ctx, name)
}

// submitJob validates sub, stores what does not fit inline, creates the
// InferenceJob (or returns the one its idempotency key names) and answers
// with the job object.
func (p *Proxy) submitJob(w http.ResponseWriter, r *http.Request, sub jobSubmission) {
	ctx := r.Context()
	if !p.checkJobTarget(w, r, sub.model, sub.path) {
		return
	}
	caller := qosCallerName(r)
	idemLabel := ""
	if sub.idempotencyKey != "" {
		sum := sha256.Sum256([]byte(caller + "\x00" + sub.idempotencyKey))
		idemLabel = hex.EncodeToString(sum[:])[:40]
		var existing aiv1alpha2.InferenceJobList
		if err := p.client.List(ctx, &existing, client.InNamespace(p.namespace), client.MatchingLabels{jobLabelIdempotency: idemLabel}); err == nil && len(existing.Items) > 0 {
			writeJSON(w, http.StatusOK, p.describeJob(ctx, &existing.Items[0]))
			return
		}
	}
	name, err := newJobName()
	if err != nil {
		validation.WriteInternalError(w, "naming job: "+err.Error())
		return
	}
	ttl := sub.ttl
	if ttl <= 0 {
		ttl = defaultJobTTL
	}
	now := metav1.Now()
	job := &aiv1alpha2.InferenceJob{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: p.namespace,
			Annotations: map[string]string{jobAnnotationCaller: caller, jobAnnotationUserAgent: r.Header.Get("User-Agent")},
			Finalizers:  []string{jobFinalizer},
		},
		Spec: aiv1alpha2.InferenceJobSpec{
			Model: sub.model, Path: sub.path, ContentType: sub.contentType,
			TTLAfterFinished: &metav1.Duration{Duration: ttl},
			QoSClass:         sub.qosClass, IdempotencyKey: sub.idempotencyKey,
		},
	}
	if idemLabel != "" {
		job.Labels = map[string]string{jobLabelIdempotency: idemLabel}
	}
	mediaType, _, _ := mime.ParseMediaType(sub.contentType)
	if mediaType == "application/json" && len(sub.body) <= maxInlineJobRequestBytes {
		job.Spec.Request = &apiextensionsv1.JSON{Raw: sub.body}
		job.Spec.ContentType = ""
	} else {
		sum := sha256.Sum256(sub.body)
		digest := hex.EncodeToString(sum[:])
		uri, err := p.jobs.cfg.Store.Put(ctx, jobObjectKey(p.namespace, name, jobStoredInputPrefix+jobStoredBodyInput),
			bytes.NewReader(sub.body), int64(len(sub.body)), digest, sub.contentType)
		if err != nil {
			slog.Warn("job input store failed", "job", name, "error", err)
			validation.WriteInternalError(w, "storing job input: "+err.Error())
			return
		}
		job.Spec.InputRefs = []aiv1alpha2.InferenceJobObject{{
			Name: jobStoredBodyInput, URI: uri, ContentType: sub.contentType, Bytes: int64(len(sub.body)), SHA256: digest,
		}}
	}
	if wh := sub.webhook; wh != nil && wh.URL != "" {
		if !strings.HasPrefix(wh.URL, "http://") && !strings.HasPrefix(wh.URL, "https://") {
			validation.WriteInvalidFieldValue(w, "webhook.url", "must be an http(s) URL")
			return
		}
		job.Spec.Webhook = &aiv1alpha2.InferenceJobWebhook{URL: wh.URL, Events: wh.Events}
		if wh.Secret != "" {
			job.Spec.Webhook.SecretRef = &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: name + "-webhook"}, Key: jobWebhookSecretKey,
			}
		}
	}
	if err := p.client.Create(ctx, job); err != nil {
		validation.WriteInternalError(w, "creating job: "+err.Error())
		return
	}
	if wh := sub.webhook; wh != nil && wh.Secret != "" && job.Spec.Webhook != nil {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: name + "-webhook", Namespace: p.namespace,
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: aiv1alpha2.GroupVersion.String(), Kind: "InferenceJob", Name: name, UID: job.UID,
				}},
			},
			Data: map[string][]byte{jobWebhookSecretKey: []byte(wh.Secret)},
		}
		if err := p.client.Create(ctx, secret); err != nil {
			slog.Warn("job webhook secret create failed; deliveries will be unsigned", "job", name, "error", err)
		}
	}
	job.Status.Phase = aiv1alpha2.InferenceJobQueued
	job.Status.QueuedAt = &now
	if err := p.client.Status().Update(ctx, job); err != nil {
		slog.Debug("job queued status write failed; the runner sets it", "job", name, "error", err)
	}
	jobsSubmittedTotal.WithLabelValues(sub.model, sub.path).Inc()
	slog.Info("job submitted", append([]any{"job", name, "model", sub.model, "path", sub.path, "caller", caller},
		callerIdentityFrom(r).logAttrs()...)...)
	p.jobs.wake()
	w.Header().Set("Location", "/v1/jobs/"+name)
	writeJSON(w, http.StatusOK, p.describeJob(ctx, job))
}

// newJobName returns "job-" + 24 random hex characters.
func newJobName() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return jobIDPrefix + hex.EncodeToString(b[:]), nil
}
