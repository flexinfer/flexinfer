package commands

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

func testJobs() []*aiv1alpha2.InferenceJob {
	done := metav1.NewTime(time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC))
	return []*aiv1alpha2.InferenceJob{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "job-aaa", Namespace: "flexinfer-system", CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Hour))},
			Spec:       aiv1alpha2.InferenceJobSpec{Model: "qwen-image", Path: "/v1/images/generations"},
			Status: aiv1alpha2.InferenceJobStatus{Phase: aiv1alpha2.InferenceJobCompleted, Progress: 100, Lane: "qwen-image-21",
				CompletedAt: &done, Output: &aiv1alpha2.InferenceJobObject{URI: "s3://flexinfer-jobs/jobs/x/output", ContentType: "application/json", Bytes: 42}},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "job-bbb", Namespace: "flexinfer-system", CreationTimestamp: metav1.NewTime(time.Now())},
			Spec:       aiv1alpha2.InferenceJobSpec{Model: "wan-t2v", Path: "/v1/videos/generations"},
		},
	}
}

func setupJobsCLI(t *testing.T) *fake.ClientBuilder {
	t.Helper()
	stubInClusterConfig(t)
	stubNotifyContext(t)
	origNS, origAll := namespace, allNs
	namespace, allNs = "flexinfer-system", false
	t.Cleanup(func() {
		namespace, allNs = origNS, origAll
		jobsPhase, jobsModel, jobsProxy, jobsOutFile = "", "", "", ""
	})
	b := fake.NewClientBuilder().WithScheme(scheme)
	for _, j := range testJobs() {
		b = b.WithObjects(j)
	}
	return b
}

func TestJobsListFiltersAndOrders(t *testing.T) {
	stubClient(t, setupJobsCLI(t).Build())
	cmd, stdout, _ := newTestCmd()
	if err := runJobsList(cmd, nil); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if strings.Index(out, "job-bbb") > strings.Index(out, "job-aaa") {
		t.Errorf("newest first:\n%s", out)
	}
	if !strings.Contains(out, "queued") || !strings.Contains(out, "completed") {
		t.Errorf("phases missing:\n%s", out)
	}

	jobsPhase = "completed"
	cmd, stdout, _ = newTestCmd()
	if err := runJobsList(cmd, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String(), "job-bbb") || !strings.Contains(stdout.String(), "qwen-image-21") {
		t.Errorf("--phase completed:\n%s", stdout.String())
	}
}

func TestJobsGetAndCancel(t *testing.T) {
	c := setupJobsCLI(t).Build()
	stubClient(t, c)
	cmd, stdout, _ := newTestCmd()
	if err := runJobsGet(cmd, []string{"job-aaa"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "s3://flexinfer-jobs/jobs/x/output") {
		t.Errorf("get:\n%s", stdout.String())
	}

	cmd, stdout, _ = newTestCmd()
	if err := runJobsCancel(cmd, []string{"job-bbb"}); err != nil {
		t.Fatal(err)
	}
	var j aiv1alpha2.InferenceJob
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "flexinfer-system", Name: "job-bbb"}, &j); err != nil {
		t.Fatal(err)
	}
	if !j.Spec.Cancel {
		t.Error("cancel did not set spec.cancel")
	}
	cmd, stdout, _ = newTestCmd()
	if err := runJobsCancel(cmd, []string{"job-aaa"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "already completed") {
		t.Errorf("cancel of a finished job: %s", stdout.String())
	}
}

func TestJobsContentDownloadsThroughTheProxy(t *testing.T) {
	setupJobsCLI(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/jobs/job-aaa/content" || r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "nope", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("mp4bytes"))
	}))
	defer srv.Close()
	jobsProxy, jobsToken = srv.URL, "tok"
	jobsOutFile = filepath.Join(t.TempDir(), "out.mp4")
	t.Cleanup(func() { jobsToken = "" })

	cmd, _, stderr := newTestCmd()
	if err := runJobsContent(cmd, []string{"job-aaa"}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(jobsOutFile)
	if string(got) != "mp4bytes" || !strings.Contains(stderr.String(), "video/mp4") {
		t.Errorf("content = %q, stderr %q", got, stderr.String())
	}
	if err := runJobsContent(cmd, []string{"job-zzz"}); err == nil {
		t.Error("a 404 from the proxy is an error")
	}
}
