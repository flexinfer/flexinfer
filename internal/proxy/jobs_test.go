package proxy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/constants"
	"github.com/flexinfer/flexinfer/pkg/validation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// ── signing vectors ──────────────────────────────────────────────

// TestSignV4MatchesTheAWSExample is the GET Object example from the AWS
// Signature V4 documentation (header-based auth).
func TestSignV4MatchesTheAWSExample(t *testing.T) {
	auth := signV4(http.MethodGet, "/test.txt", "", map[string]string{
		"host":                 "examplebucket.s3.amazonaws.com",
		"range":                "bytes=0-9",
		"x-amz-content-sha256": emptySHA256,
		"x-amz-date":           "20130524T000000Z",
	}, emptySHA256, "AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "us-east-1", "s3", "20130524T000000Z")

	assert.Equal(t, "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, "+
		"SignedHeaders=host;range;x-amz-content-sha256;x-amz-date, "+
		"Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41", auth)
}

// TestSignWebhookMatchesTheStandardWebhooksExample is the example from the
// Standard Webhooks specification.
func TestSignWebhookMatchesTheStandardWebhooksExample(t *testing.T) {
	secret := webhookSecretBytes("whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw")
	got := signWebhook(secret, "msg_p5jXN8AQM9LWM0D4loKWxJek", 1614265330, []byte(`{"test": 2432232314}`))
	assert.Equal(t, "v1,g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE=", got)
	assert.Equal(t, []byte("raw-secret"), webhookSecretBytes("raw-secret"))
}

// ── stores ───────────────────────────────────────────────────────

func TestPVCStoreRoundTrip(t *testing.T) {
	s, err := newPVCStore(t.TempDir(), "flexinfer-jobs")
	require.NoError(t, err)
	ctx := context.Background()
	key := jobObjectKey("flexinfer-system", "job-1", "output")

	uri, err := s.Put(ctx, key, strings.NewReader("hello"), 5, "", "text/plain")
	require.NoError(t, err)
	assert.Equal(t, "pvc://flexinfer-jobs/jobs/flexinfer-system/job-1/output", uri)
	rc, err := s.Get(ctx, uri)
	require.NoError(t, err)
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	assert.Equal(t, "hello", string(got))

	require.NoError(t, s.Delete(ctx, uri))
	_, err = s.Get(ctx, uri)
	assert.ErrorIs(t, err, errJobObjectNotFound)
	require.NoError(t, s.Delete(ctx, uri), "deleting a missing object is not an error")

	_, err = s.Put(ctx, "../escape", strings.NewReader("x"), 1, "", "")
	assert.Error(t, err, "a key cannot leave the mount")
	_, err = s.Get(ctx, "pvc://other/jobs/x")
	assert.Error(t, err, "a URI from another claim is refused")
}

// fakeS3 is an in-memory bucket that checks every request's SigV4 signature.
type fakeS3 struct {
	t       *testing.T
	mu      sync.Mutex
	objects map[string][]byte
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	want := signV4(r.Method, r.URL.EscapedPath(), "", map[string]string{
		"host":                 r.Host,
		"x-amz-content-sha256": r.Header.Get("x-amz-content-sha256"),
		"x-amz-date":           r.Header.Get("x-amz-date"),
	}, r.Header.Get("x-amz-content-sha256"), "minio", "minio-secret", "us-east-1", "s3", r.Header.Get("x-amz-date"))
	if r.Header.Get("Authorization") != want {
		http.Error(w, "SignatureDoesNotMatch", http.StatusForbidden)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.Method {
	case http.MethodPut:
		b, _ := io.ReadAll(r.Body)
		f.objects[r.URL.Path] = b
	case http.MethodGet:
		b, ok := f.objects[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	case http.MethodDelete:
		delete(f.objects, r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}
}

func TestS3StoreSignsAndRoundTrips(t *testing.T) {
	bucket := &fakeS3{t: t, objects: map[string][]byte{}}
	srv := httptest.NewServer(bucket)
	defer srv.Close()
	s, err := newS3Store(srv.URL, "flexinfer-jobs", "", "minio", "minio-secret")
	require.NoError(t, err)
	ctx := context.Background()
	body := []byte(`{"data":[{"b64_json":"aGk="}]}`)
	digest := sha256.Sum256(body)
	sum := hex.EncodeToString(digest[:])

	uri, err := s.Put(ctx, jobObjectKey("ns", "job-2", "output"), bytes.NewReader(body), int64(len(body)), sum, "application/json")
	require.NoError(t, err)
	assert.Equal(t, "s3://flexinfer-jobs/jobs/ns/job-2/output", uri)
	assert.Contains(t, bucket.objects, "/flexinfer-jobs/jobs/ns/job-2/output")

	rc, err := s.Get(ctx, uri)
	require.NoError(t, err)
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	assert.Equal(t, body, got)

	require.NoError(t, s.Delete(ctx, uri))
	_, err = s.Get(ctx, uri)
	assert.ErrorIs(t, err, errJobObjectNotFound)

	_, err = newS3Store("not a url", "b", "", "a", "s")
	assert.Error(t, err)
	_, err = storeFromEnv("", func(string) string { return "" })
	assert.Error(t, err)
}

// ── request parsing ──────────────────────────────────────────────

func TestReadBackgroundRequest(t *testing.T) {
	t.Run("json background is stripped", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/v1/images/generations",
			strings.NewReader(`{"model":"image-gen","prompt":"a cat","background":true}`))
		r.Header.Set("Content-Type", "application/json")
		sub, bg, err := readBackgroundRequest(r)
		require.NoError(t, err)
		assert.True(t, bg)
		assert.Equal(t, "image-gen", sub.model)
		assert.NotContains(t, string(sub.body), "background")
		rest, _ := io.ReadAll(r.Body)
		assert.Contains(t, string(rest), `"background":true`, "the original body is restored")
	})
	t.Run("plain json is not a job", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"m"}`))
		r.Header.Set("Content-Type", "application/json")
		_, bg, err := readBackgroundRequest(r)
		assert.NoError(t, err)
		assert.False(t, bg)
	})
	t.Run("Prefer respond-async", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"m"}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Prefer", "respond-async, wait=10")
		_, bg, err := readBackgroundRequest(r)
		assert.NoError(t, err)
		assert.True(t, bg)
	})
	t.Run("streaming background is refused", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"m","stream":true,"background":true}`))
		r.Header.Set("Content-Type", "application/json")
		_, bg, err := readBackgroundRequest(r)
		assert.True(t, bg)
		assert.Error(t, err)
	})
	t.Run("an oversized plain body reaches the sync path whole", func(t *testing.T) {
		big := `{"model":"m","pad":"` + strings.Repeat("x", maxJobSubmitBytes) + `"}`
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(big))
		r.Header.Set("Content-Type", "application/json")
		_, bg, _ := readBackgroundRequest(r)
		assert.False(t, bg)
		rest, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.Len(t, rest, len(big))
	})
	t.Run("multipart background field is dropped", func(t *testing.T) {
		body, ct := multipartBody(t, map[string]string{"model": "whisper", "background": "true", "response_format": "json"}, "audio.wav", []byte("RIFFdata"))
		r := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
		r.Header.Set("Content-Type", ct)
		sub, bg, err := readBackgroundRequest(r)
		require.NoError(t, err)
		assert.True(t, bg)
		assert.Equal(t, "whisper", sub.model)
		fields := parseMultipart(t, sub.body, ct)
		assert.NotContains(t, fields, "background")
		assert.Equal(t, "RIFFdata", fields["file"])
		assert.Equal(t, "json", fields["response_format"])
	})
}

func multipartBody(t *testing.T, fields map[string]string, filename string, file []byte) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		require.NoError(t, mw.WriteField(k, v))
	}
	fw, err := mw.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, _ = fw.Write(file)
	require.NoError(t, mw.Close())
	return buf.Bytes(), mw.FormDataContentType()
}

func parseMultipart(t *testing.T, body []byte, ct string) map[string]string {
	t.Helper()
	boundary := strings.TrimPrefix(ct[strings.Index(ct, "boundary="):], "boundary=")
	mr := multipart.NewReader(bytes.NewReader(body), boundary)
	out := map[string]string{}
	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}
		b, _ := io.ReadAll(part)
		out[part.FormName()] = string(b)
	}
	return out
}

// ── end to end ───────────────────────────────────────────────────

// jobFixture is a proxy with jobs on a PVC store, one Ready lane per name
// served by an httptest upstream, and a webhook receiver.
type jobFixture struct {
	p        *Proxy
	client   client.Client
	upstream map[string]*httptest.Server
	bodies   chan []byte
	status   atomic.Int32
	hooks    chan *http.Request
	hookBody chan []byte
}

func newJobFixture(t *testing.T, models ...*aiv1alpha2.Model) *jobFixture {
	t.Helper()
	RegisterMetrics()
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, aiv1alpha2.AddToScheme(scheme))
	objs := []client.Object{}
	for _, m := range models {
		objs = append(objs, m)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).
		WithStatusSubresource(append(objs, &aiv1alpha2.InferenceJob{})...).Build()
	store, err := newPVCStore(t.TempDir(), "jobs")
	require.NoError(t, err)

	f := &jobFixture{client: c, upstream: map[string]*httptest.Server{}, bodies: make(chan []byte, 16),
		hooks: make(chan *http.Request, 16), hookBody: make(chan []byte, 16)}
	f.status.Store(http.StatusOK)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	f.p = &Proxy{
		client: c, namespace: "default", resolver: NewModelResolver(c, "default"),
		activator: &countingActivator{}, admission: &admissionFilter{}, maxQueueSize: 10,
		queueTimeout: time.Second, coldStartTimeout: time.Second, ctx: ctx, cancel: cancel,
	}
	for _, m := range models {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			f.bodies <- b
			code := int(f.status.Load())
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			if code == http.StatusOK {
				_, _ = io.WriteString(w, `{"created":1,"data":[{"b64_json":"aW1hZ2U="}]}`)
				return
			}
			_, _ = io.WriteString(w, `{"error":{"code":"model_cold_starting","message":"warming"}}`)
		}))
		t.Cleanup(srv.Close)
		f.upstream[m.Name] = srv
		f.p.directLoadTargets.Store(m.Name, srv.URL)
	}
	f.p.jobs = newJobRunner(f.p, JobsConfig{
		Enabled: true, Store: store, SpoolDir: t.TempDir(), PerModel: 2, MaxRunning: 8,
		PollInterval: time.Hour, MaxOutputBytes: 1 << 20, MaxWait: time.Minute, RunnerID: "proxy-a",
	})
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.hooks <- r
		f.hookBody <- b
	}))
	t.Cleanup(hook.Close)
	f.upstream["hook"] = hook
	return f
}

func readyLane(name, backend string, annotations map[string]string) *aiv1alpha2.Model {
	return &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Annotations: annotations},
		Spec:       aiv1alpha2.ModelSpec{Backend: backend, Source: "pvc://a/b"},
		Status:     aiv1alpha2.ModelStatus{Phase: aiv1alpha2.ModelPhaseReady},
	}
}

func (f *jobFixture) do(t *testing.T, method, path string, body io.Reader, headers map[string]string) (*httptest.ResponseRecorder, jobObject) {
	t.Helper()
	r := httptest.NewRequest(method, path, body)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	f.p.handler().ServeHTTP(rec, r)
	var obj jobObject
	if strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		_ = json.Unmarshal(rec.Body.Bytes(), &obj)
	}
	return rec, obj
}

// waitJob passes the runner until job reaches a terminal phase.
func (f *jobFixture) waitJob(t *testing.T, id string) *aiv1alpha2.InferenceJob {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		f.p.jobs.pass(context.Background())
		j := &aiv1alpha2.InferenceJob{}
		require.NoError(t, f.client.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: id}, j))
		if j.Status.Phase.IsTerminal() {
			return j
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job %s never finished", id)
	return nil
}

func TestBackgroundImageJobRunsStoresAndSignsItsWebhook(t *testing.T) {
	f := newJobFixture(t, readyLane("qwen-image", "diffusers",
		map[string]string{constants.AnnotationServePaths: "/v1/images/generations,/v1/images/edits"}))

	rec, obj := f.do(t, http.MethodPost, "/v1/images/generations",
		strings.NewReader(`{"model":"qwen-image","prompt":"a lighthouse","background":true}`),
		map[string]string{headerJobWebhookURL: f.upstream["hook"].URL, headerJobWebhookSecret: "whsec_" + base64.StdEncoding.EncodeToString([]byte("s3cret"))})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "job", obj.Object)
	assert.Equal(t, "queued", obj.Status)
	assert.True(t, obj.Background)
	assert.True(t, strings.HasPrefix(obj.ID, "job-"))
	assert.Equal(t, "/v1/jobs/"+obj.ID, rec.Header().Get("Location"))
	select {
	case <-f.bodies:
		t.Fatal("submission must not reach the lane")
	default:
	}

	j := f.waitJob(t, obj.ID)
	assert.Equal(t, aiv1alpha2.InferenceJobCompleted, j.Status.Phase)
	assert.Equal(t, "qwen-image", j.Status.Lane)
	assert.EqualValues(t, 100, j.Status.Progress)
	assert.NotContains(t, string(<-f.bodies), "background", "the lane gets a plain synchronous request")

	rec, got := f.do(t, http.MethodGet, "/v1/jobs/"+obj.ID, nil, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "completed", got.Status)
	require.NotNil(t, got.Output)
	assert.Equal(t, "/v1/jobs/"+obj.ID+"/content", got.Output.URL)
	assert.Equal(t, "application/json", got.Output.ContentType)

	rec, _ = f.do(t, http.MethodGet, got.Output.URL, nil, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"created":1,"data":[{"b64_json":"aW1hZ2U="}]}`, rec.Body.String())
	assert.Equal(t, `"`+got.Output.SHA256+`"`, rec.Header().Get("ETag"))

	select {
	case hr := <-f.hooks:
		body := <-f.hookBody
		assert.Equal(t, obj.ID+".job.completed", hr.Header.Get("webhook-id"))
		stamp := hr.Header.Get("webhook-timestamp")
		unix, err := strconv.ParseInt(stamp, 10, 64)
		require.NoError(t, err)
		assert.Equal(t, signWebhook([]byte("s3cret"), obj.ID+".job.completed", unix, body), hr.Header.Get("webhook-signature"))
		var ev jobWebhookEvent
		require.NoError(t, json.Unmarshal(body, &ev))
		assert.Equal(t, "job.completed", ev.Type)
		assert.Equal(t, obj.ID, ev.Data.ID)
	case <-time.After(5 * time.Second):
		t.Fatal("no webhook")
	}
}

func TestJobRetriesWhileTheLaneIsColdThenCompletes(t *testing.T) {
	f := newJobFixture(t, readyLane("workhorse", "vllm", nil))
	f.status.Store(http.StatusServiceUnavailable)
	jobRetryMinWait = 10 * time.Millisecond
	t.Cleanup(func() { jobRetryMinWait = 5 * time.Second })

	_, obj := f.do(t, http.MethodPost, "/v1/jobs",
		strings.NewReader(`{"path":"/v1/chat/completions","request":{"model":"workhorse","messages":[{"role":"user","content":"hi"}]}}`), nil)
	require.NotEmpty(t, obj.ID)
	f.p.jobs.pass(context.Background())
	<-f.bodies
	f.status.Store(http.StatusOK)

	j := f.waitJob(t, obj.ID)
	assert.Equal(t, aiv1alpha2.InferenceJobCompleted, j.Status.Phase)
	assert.Positive(t, f.p.activator.(*countingActivator).touches, "the wait between retries keeps the lane demanded")
}

func TestJobFailsOnALaneError(t *testing.T) {
	f := newJobFixture(t, readyLane("workhorse", "vllm", nil))
	f.status.Store(http.StatusBadRequest)

	_, obj := f.do(t, http.MethodPost, "/v1/jobs",
		strings.NewReader(`{"path":"/v1/chat/completions","request":{"model":"workhorse","messages":[]}}`), nil)
	j := f.waitJob(t, obj.ID)
	assert.Equal(t, aiv1alpha2.InferenceJobFailed, j.Status.Phase)
	require.NotNil(t, j.Status.Error)
	assert.EqualValues(t, http.StatusBadRequest, j.Status.Error.Status)
	assert.Equal(t, "model_cold_starting", j.Status.Error.Code, "the lane's own code is kept")
}

func TestCancelQueuedJob(t *testing.T) {
	f := newJobFixture(t, readyLane("workhorse", "vllm", nil))
	_, obj := f.do(t, http.MethodPost, "/v1/jobs",
		strings.NewReader(`{"path":"/v1/chat/completions","request":{"model":"workhorse","messages":[]}}`), nil)

	rec, got := f.do(t, http.MethodPost, "/v1/jobs/"+obj.ID+"/cancel", nil, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "queued", got.Status)
	j := f.waitJob(t, obj.ID)
	assert.Equal(t, aiv1alpha2.InferenceJobCancelled, j.Status.Phase)
	select {
	case <-f.bodies:
		t.Fatal("a cancelled job never reaches its lane")
	default:
	}

	rec, _ = f.do(t, http.MethodGet, "/v1/jobs/"+obj.ID+"/content", nil, nil)
	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestDiffusersLanesOnOneCardRunOneJobAtATime(t *testing.T) {
	shared := func(name string) *aiv1alpha2.Model {
		m := readyLane(name, "diffusers", nil)
		m.Spec.GPU = &aiv1alpha2.GPUSpec{Shared: "xtx-flex"}
		return m
	}
	f := newJobFixture(t, shared("qwen-image"), shared("wan-t2v"))
	ctx := context.Background()
	base := time.Now().Add(-time.Minute)
	for i, model := range []string{"qwen-image", "wan-t2v", "qwen-image"} {
		job := &aiv1alpha2.InferenceJob{
			ObjectMeta: metav1.ObjectMeta{Name: "job-" + strconv.Itoa(3-i), Namespace: "default"},
			Spec: aiv1alpha2.InferenceJobSpec{Model: model, Path: "/v1/images/generations",
				Request: &apiextensionsv1.JSON{Raw: []byte(`{"model":"` + model + `","prompt":"x"}`)}},
		}
		require.NoError(t, f.client.Create(ctx, job))
		queuedAt := metav1.NewTime(base.Add(time.Duration(i) * time.Second))
		job.Status = aiv1alpha2.InferenceJobStatus{Phase: aiv1alpha2.InferenceJobQueued, QueuedAt: &queuedAt}
		require.NoError(t, f.client.Status().Update(ctx, job))
	}
	f.status.Store(http.StatusServiceUnavailable)
	jobRetryMinWait = time.Hour
	t.Cleanup(func() { jobRetryMinWait = 5 * time.Second })

	f.p.jobs.pass(ctx)
	f.p.jobs.pass(ctx)

	var list aiv1alpha2.InferenceJobList
	require.NoError(t, f.client.List(ctx, &list))
	running := 0
	for _, j := range list.Items {
		if j.Status.Phase == aiv1alpha2.InferenceJobInProgress {
			running++
			assert.Equal(t, "job-3", j.Name, "the oldest job on the card runs first, not the lowest name")
		}
	}
	assert.Equal(t, 1, running, "one render at a time per shared card")
	f.p.jobs.mu.Lock()
	for _, cancel := range f.p.jobs.running {
		cancel()
	}
	f.p.jobs.mu.Unlock()
}

func TestStaleClaimIsReclaimedAndDeletionCleansStorage(t *testing.T) {
	f := newJobFixture(t, readyLane("workhorse", "vllm", nil))
	ctx := context.Background()
	old := metav1.NewTime(time.Now().Add(-10 * time.Minute))
	job := &aiv1alpha2.InferenceJob{
		ObjectMeta: metav1.ObjectMeta{Name: "job-stale", Namespace: "default"},
		Spec: aiv1alpha2.InferenceJobSpec{Model: "workhorse", Path: "/v1/chat/completions",
			Request: &apiextensionsv1.JSON{Raw: []byte(`{"model":"workhorse","messages":[]}`)}},
	}
	require.NoError(t, f.client.Create(ctx, job))
	job.Status = aiv1alpha2.InferenceJobStatus{Phase: aiv1alpha2.InferenceJobInProgress, Runner: "proxy-dead", HeartbeatAt: &old, Attempts: 1}
	require.NoError(t, f.client.Status().Update(ctx, job))

	j := f.waitJob(t, "job-stale")
	assert.Equal(t, aiv1alpha2.InferenceJobCompleted, j.Status.Phase)
	assert.Equal(t, "proxy-a", j.Status.Runner)
	assert.EqualValues(t, 2, j.Status.Attempts)
	assert.Contains(t, j.Finalizers, jobFinalizer)
	uri := j.Status.Output.URI

	require.NoError(t, f.client.Delete(ctx, j))
	f.p.jobs.pass(ctx)
	_, err := f.p.jobs.cfg.Store.Get(ctx, uri)
	assert.ErrorIs(t, err, errJobObjectNotFound, "the output goes with the job")
	err = f.client.Get(ctx, client.ObjectKey{Namespace: "default", Name: "job-stale"}, &aiv1alpha2.InferenceJob{})
	assert.True(t, err != nil, "the finalizer is released")
}

func TestMultipartTranscriptionJobReplaysTheUpload(t *testing.T) {
	f := newJobFixture(t, readyLane("whisper", "whispercpp",
		map[string]string{constants.AnnotationServePaths: "/v1/audio/transcriptions"}))
	body, ct := multipartBody(t, map[string]string{"model": "whisper", "background": "true"}, "a.wav", []byte("RIFF1234"))

	r := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	r.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	f.p.handler().ServeHTTP(rec, r)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var obj jobObject
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &obj))

	j := f.waitJob(t, obj.ID)
	require.Equal(t, aiv1alpha2.InferenceJobCompleted, j.Status.Phase, j.Status.Error)
	require.Len(t, j.Spec.InputRefs, 1)
	fields := parseMultipart(t, <-f.bodies, ct)
	assert.Equal(t, "RIFF1234", fields["file"])
	assert.NotContains(t, fields, "background")
}

func TestJobSubmissionGates(t *testing.T) {
	f := newJobFixture(t, readyLane("whisper", "whispercpp",
		map[string]string{constants.AnnotationServePaths: "/v1/audio/transcriptions"}))
	parked := parkedModel("parked", "Idle")
	require.NoError(t, f.client.Create(context.Background(), parked))

	rec, _ := f.do(t, http.MethodPost, "/v1/jobs",
		strings.NewReader(`{"path":"/v1/chat/completions","request":{"model":"whisper"}}`), nil)
	assert.Equal(t, http.StatusNotFound, rec.Code, "a lane that does not serve the path")
	rec, _ = f.do(t, http.MethodPost, "/v1/jobs",
		strings.NewReader(`{"path":"/v1/chat/completions","request":{"model":"parked"}}`), nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), validation.CodeModelParked)
	rec, _ = f.do(t, http.MethodPost, "/v1/jobs",
		strings.NewReader(`{"path":"/v1/jobs","request":{"model":"whisper"}}`), nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	rec, _ = f.do(t, http.MethodGet, "/v1/jobs/job-nope", nil, nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	f.p.jobs = nil
	rec, _ = f.do(t, http.MethodGet, "/v1/jobs", nil, nil)
	assert.Equal(t, http.StatusNotImplemented, rec.Code)
	rec, _ = f.do(t, http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"whisper","background":true}`), nil)
	assert.Equal(t, http.StatusNotImplemented, rec.Code)
}

func TestIdempotencyKeyReturnsTheFirstJob(t *testing.T) {
	f := newJobFixture(t, readyLane("workhorse", "vllm", nil))
	body := `{"path":"/v1/chat/completions","idempotency_key":"render-42","request":{"model":"workhorse","messages":[]}}`
	_, first := f.do(t, http.MethodPost, "/v1/jobs", strings.NewReader(body), nil)
	_, second := f.do(t, http.MethodPost, "/v1/jobs", strings.NewReader(body), nil)
	assert.Equal(t, first.ID, second.ID)

	rec, _ := f.do(t, http.MethodGet, "/v1/jobs?status=queued&model=workhorse", nil, nil)
	var list struct {
		Data []jobObject `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	assert.Len(t, list.Data, 1)
	require.NotNil(t, list.Data[0].FlexInfer.QueuePosition)
	assert.Equal(t, 0, *list.Data[0].FlexInfer.QueuePosition)
}
