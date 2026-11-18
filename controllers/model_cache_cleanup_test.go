package controllers

import (
	"context"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

func localCacheCleanupScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{aiv1alpha2.AddToScheme, batchv1.AddToScheme, corev1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatalf("AddToScheme() error = %v", err)
		}
	}
	return scheme
}

func localCacheModel(name string) *aiv1alpha2.Model {
	model := &aiv1alpha2.Model{}
	model.Name = name
	model.Namespace = "flexinfer-system"
	model.Spec.Source = "HF://biMEMO/Qwen3.8-27B-int4-AutoRound"
	model.Spec.Backend = "vllm"
	model.Spec.NodeSelector = map[string]string{"kubernetes.io/hostname": "cblevins-7900xtx"}
	model.Spec.GPU = &aiv1alpha2.GPUSpec{Count: ptr.To(int32(1))}
	model.Spec.Cache = &aiv1alpha2.CacheSpec{Strategy: "Local"}
	return model
}

func newLocalCacheCleanupReconciler(t *testing.T, policy string, objs ...runtime.Object) *ModelReconciler {
	t.Helper()
	scheme := localCacheCleanupScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objs...).Build()
	return &ModelReconciler{Client: c, Scheme: scheme, LocalCacheOnDelete: policy}
}

func getCleanupJob(t *testing.T, r *ModelReconciler, model *aiv1alpha2.Model) (*batchv1.Job, bool) {
	t.Helper()
	job := &batchv1.Job{}
	err := r.Get(context.Background(), types.NamespacedName{Namespace: model.Namespace, Name: model.Name + "-cache-cleanup"}, job)
	if err != nil {
		return nil, false
	}
	return job, true
}

func TestParseLocalCacheOnDelete(t *testing.T) {
	for raw, want := range map[string]string{"": "retain", "retain": "retain", " Delete ": "delete", "DELETE": "delete"} {
		got, err := ParseLocalCacheOnDelete(raw)
		if err != nil || got != want {
			t.Errorf("ParseLocalCacheOnDelete(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"delte", "true", "keep", "yes"} {
		if _, err := ParseLocalCacheOnDelete(raw); err == nil {
			t.Errorf("ParseLocalCacheOnDelete(%q) accepted an unknown policy", raw)
		}
	}
}

// The default policy keeps today's behavior: no Job, nothing touched.
func TestCleanupLocalCache_DefaultRetainsCache(t *testing.T) {
	model := localCacheModel("qwen38-27b-so-canary")
	r := newLocalCacheCleanupReconciler(t, "")
	if err := r.cleanupLocalCache(context.Background(), model); err != nil {
		t.Fatalf("cleanupLocalCache() error = %v", err)
	}
	if _, ok := getCleanupJob(t, r, model); ok {
		t.Fatal("retain policy created a cleanup Job")
	}
}

func TestCleanupLocalCache_DeleteCreatesNodeJob(t *testing.T) {
	model := localCacheModel("qwen38-27b-so-canary")
	r := newLocalCacheCleanupReconciler(t, "delete")
	if err := r.cleanupLocalCache(context.Background(), model); err != nil {
		t.Fatalf("cleanupLocalCache() error = %v", err)
	}
	job, ok := getCleanupJob(t, r, model)
	if !ok {
		t.Fatal("delete policy created no cleanup Job")
	}
	if len(job.OwnerReferences) != 0 {
		t.Fatalf("cleanup Job must not be owned by the Model being deleted: %v", job.OwnerReferences)
	}
	if got := job.Annotations[AnnotationCacheKind]; got != "local-cleanup" {
		t.Fatalf("cache-kind annotation = %q", got)
	}
	if got := job.Annotations[AnnotationCachePath]; got != "/var/lib/flexinfer/models/flexinfer-system/qwen38-27b-so-canary" {
		t.Fatalf("cache-path annotation = %q", got)
	}
	pod := job.Spec.Template.Spec
	if got := pod.NodeSelector["kubernetes.io/hostname"]; got != "cblevins-7900xtx" {
		t.Fatalf("nodeSelector = %v, want the Model's node", pod.NodeSelector)
	}
	if len(pod.Tolerations) != 1 || pod.Tolerations[0].Key != "dedicated" {
		t.Fatalf("tolerations = %v, want the dedicated=gpu toleration", pod.Tolerations)
	}
	if len(pod.Volumes) != 1 || pod.Volumes[0].HostPath == nil {
		t.Fatalf("volumes = %v, want one hostPath", pod.Volumes)
	}
	// The base directory is mounted, not the Model's own directory, so rm can
	// remove the directory itself and DirectoryOrCreate cannot resurrect it.
	if got := pod.Volumes[0].HostPath.Path; got != "/var/lib/flexinfer/models" {
		t.Fatalf("hostPath = %q, want the cache base", got)
	}
	if len(pod.Containers) != 1 || len(pod.Containers[0].Args) != 1 {
		t.Fatalf("containers = %v", pod.Containers)
	}
	script := pod.Containers[0].Args[0]
	if !strings.Contains(script, `TARGET="/models/flexinfer-system/qwen38-27b-so-canary"`) {
		t.Fatalf("script does not target the Model's directory:\n%s", script)
	}
	if !strings.Contains(script, `rm -rf -- "$TARGET"`) || !strings.Contains(script, "/models/*/*)") {
		t.Fatalf("script lacks the guarded rm:\n%s", script)
	}
	if job.Spec.TTLSecondsAfterFinished == nil || *job.Spec.TTLSecondsAfterFinished != 600 {
		t.Fatalf("TTLSecondsAfterFinished = %v, want 600", job.Spec.TTLSecondsAfterFinished)
	}
	if pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken {
		t.Fatal("cleanup pod must not mount a service account token")
	}

	// A second pass during the same deletion must not fail on AlreadyExists.
	if err := r.cleanupLocalCache(context.Background(), model); err != nil {
		t.Fatalf("second cleanupLocalCache() error = %v", err)
	}
}

func TestCleanupLocalCache_CustomHostPath(t *testing.T) {
	model := localCacheModel("wan21-t2v-1p3b-gfx1100")
	model.Spec.Cache.HostPath = "/mnt/nvme/flexinfer"
	r := newLocalCacheCleanupReconciler(t, "delete")
	if err := r.cleanupLocalCache(context.Background(), model); err != nil {
		t.Fatalf("cleanupLocalCache() error = %v", err)
	}
	job, ok := getCleanupJob(t, r, model)
	if !ok {
		t.Fatal("no cleanup Job")
	}
	if got := job.Spec.Template.Spec.Volumes[0].HostPath.Path; got != "/mnt/nvme/flexinfer" {
		t.Fatalf("hostPath = %q", got)
	}
	if !strings.Contains(job.Spec.Template.Spec.Containers[0].Args[0], `TARGET="/models/flexinfer-system/wan21-t2v-1p3b-gfx1100"`) {
		t.Fatal("script does not target <base>/<namespace>/<name>")
	}
}

func TestCleanupLocalCache_AnnotationOverridesDefault(t *testing.T) {
	// delete annotation on a retain controller: Job created.
	model := localCacheModel("qwen38-27b-octave-kv-canary")
	model.Annotations = map[string]string{AnnotationLocalCacheOnDelete: "delete"}
	r := newLocalCacheCleanupReconciler(t, "retain")
	if err := r.cleanupLocalCache(context.Background(), model); err != nil {
		t.Fatalf("cleanupLocalCache() error = %v", err)
	}
	if _, ok := getCleanupJob(t, r, model); !ok {
		t.Fatal("delete annotation did not override the retain default")
	}

	// retain annotation on a delete controller: no Job.
	keep := localCacheModel("qwen38-27b-s4-step0-canary")
	keep.Annotations = map[string]string{AnnotationLocalCacheOnDelete: "retain"}
	r = newLocalCacheCleanupReconciler(t, "delete")
	if err := r.cleanupLocalCache(context.Background(), keep); err != nil {
		t.Fatalf("cleanupLocalCache() error = %v", err)
	}
	if _, ok := getCleanupJob(t, r, keep); ok {
		t.Fatal("retain annotation did not override the delete default")
	}
}

// A misspelled annotation must never delete: it falls back to the default.
func TestCleanupLocalCache_InvalidAnnotationFallsBackToDefault(t *testing.T) {
	model := localCacheModel("qwen38-27b-pool-canary")
	model.Annotations = map[string]string{AnnotationLocalCacheOnDelete: "delte"}
	r := newLocalCacheCleanupReconciler(t, "retain")
	if err := r.cleanupLocalCache(context.Background(), model); err != nil {
		t.Fatalf("cleanupLocalCache() error = %v", err)
	}
	if _, ok := getCleanupJob(t, r, model); ok {
		t.Fatal("invalid annotation deleted the cache")
	}
	policy, source, err := r.localCacheOnDeletePolicy(model)
	if policy != LocalCacheOnDeleteRetain || source != "default" || err == nil {
		t.Fatalf("policy = %q from %q, err = %v; want retain from default with an error", policy, source, err)
	}
}

func TestCleanupLocalCache_SkipsOtherStrategies(t *testing.T) {
	r := newLocalCacheCleanupReconciler(t, "delete")
	for _, strategy := range []string{"SharedPVC", "Memory", "None"} {
		model := localCacheModel("lane-" + strings.ToLower(strategy))
		model.Spec.Cache.Strategy = strategy
		if err := r.cleanupLocalCache(context.Background(), model); err != nil {
			t.Fatalf("cleanupLocalCache(%s) error = %v", strategy, err)
		}
		if _, ok := getCleanupJob(t, r, model); ok {
			t.Fatalf("%s strategy created a Local cache cleanup Job", strategy)
		}
	}
}

func TestCleanupLocalCache_FallsBackToStatusNode(t *testing.T) {
	model := localCacheModel("qwen38-27b-qos-canary")
	model.Spec.NodeSelector = nil
	model.Status.GPU = &aiv1alpha2.GPUStatus{Node: "cblevins-5930k"}
	r := newLocalCacheCleanupReconciler(t, "delete")
	if err := r.cleanupLocalCache(context.Background(), model); err != nil {
		t.Fatalf("cleanupLocalCache() error = %v", err)
	}
	job, ok := getCleanupJob(t, r, model)
	if !ok {
		t.Fatal("no cleanup Job")
	}
	if got := job.Spec.Template.Spec.NodeSelector["kubernetes.io/hostname"]; got != "cblevins-5930k" {
		t.Fatalf("nodeSelector = %v, want status.gpu.node", job.Spec.Template.Spec.NodeSelector)
	}
}

// With no node at all the directory is retained: there is nowhere to run.
func TestCleanupLocalCache_NoNodeRetains(t *testing.T) {
	model := localCacheModel("orphan-without-node")
	model.Spec.NodeSelector = nil
	r := newLocalCacheCleanupReconciler(t, "delete")
	if err := r.cleanupLocalCache(context.Background(), model); err != nil {
		t.Fatalf("cleanupLocalCache() error = %v", err)
	}
	if _, ok := getCleanupJob(t, r, model); ok {
		t.Fatal("a Model with no node got a cleanup Job")
	}
}

func TestJobForLocalCacheCleanup_RefusesEscapingPaths(t *testing.T) {
	model := localCacheModel("qwen38-27b-so-canary")
	model.Namespace = ".."
	r := newLocalCacheCleanupReconciler(t, "delete")
	if _, err := r.jobForLocalCacheCleanup(model, model.Spec.NodeSelector, nil); err == nil {
		t.Fatal("a namespace of .. must be refused")
	}
}

// A recreated Model cancels a cleanup Job that has not run; a finished one is
// left for its TTL.
func TestCancelPendingLocalCacheCleanup(t *testing.T) {
	model := localCacheModel("qwen38-27b-s4-step0-canary")
	pending := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: model.Name + "-cache-cleanup", Namespace: model.Namespace}}
	r := newLocalCacheCleanupReconciler(t, "delete", pending)
	r.cancelPendingLocalCacheCleanup(context.Background(), model)
	if _, ok := getCleanupJob(t, r, model); ok {
		t.Fatal("pending cleanup Job was not cancelled for the recreated Model")
	}

	done := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: model.Name + "-cache-cleanup", Namespace: model.Namespace}}
	done.Status.Succeeded = 1
	r = newLocalCacheCleanupReconciler(t, "delete", done)
	r.cancelPendingLocalCacheCleanup(context.Background(), model)
	if _, ok := getCleanupJob(t, r, model); !ok {
		t.Fatal("a finished cleanup Job was deleted; it should wait for its TTL")
	}

	// Not a Local cache: nothing is looked up or deleted.
	shared := localCacheModel("shared-lane")
	shared.Spec.Cache.Strategy = "SharedPVC"
	stray := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: shared.Name + "-cache-cleanup", Namespace: shared.Namespace}}
	r = newLocalCacheCleanupReconciler(t, "delete", stray)
	r.cancelPendingLocalCacheCleanup(context.Background(), shared)
	if _, ok := getCleanupJob(t, r, shared); !ok {
		t.Fatal("a non-Local Model deleted a Job")
	}
}
