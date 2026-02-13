package commands

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

func newTestCmd() (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	cmd := &cobra.Command{}
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	return cmd, stdout, stderr
}

func stubInClusterConfig(t *testing.T) {
	t.Helper()
	orig := inClusterConfigFn
	inClusterConfigFn = func() (*rest.Config, error) { return &rest.Config{}, nil }
	t.Cleanup(func() { inClusterConfigFn = orig })
}

func stubNotifyContext(t *testing.T) {
	t.Helper()
	orig := notifyContextFn
	notifyContextFn = func(parent context.Context, _ ...os.Signal) (context.Context, context.CancelFunc) {
		return context.WithCancel(parent)
	}
	t.Cleanup(func() { notifyContextFn = orig })
}

func stubClient(t *testing.T, c client.Client) {
	t.Helper()
	orig := newClientFn
	newClientFn = func(_ *rest.Config, _ client.Options) (client.Client, error) { return c, nil }
	t.Cleanup(func() { newClientFn = orig })
}

func TestGetNamespace(t *testing.T) {
	origAll := allNs
	origNs := namespace
	t.Cleanup(func() {
		allNs = origAll
		namespace = origNs
	})

	allNs = false
	namespace = "flexinfer-system"
	if got := getNamespace(); got != "flexinfer-system" {
		t.Fatalf("getNamespace()=%q, want %q", got, "flexinfer-system")
	}

	allNs = true
	if got := getNamespace(); got != "" {
		t.Fatalf("getNamespace()=%q, want empty for all namespaces", got)
	}
}

func TestGetKubeConfig_InClusterWins(t *testing.T) {
	origKubeconfig := kubeconfig
	t.Cleanup(func() { kubeconfig = origKubeconfig })

	kubeconfig = ""

	stubNotifyContext(t)

	origInCluster := inClusterConfigFn
	origBuild := buildConfigFromFlagsFn
	inClusterConfigFn = func() (*rest.Config, error) { return &rest.Config{Host: "https://in-cluster"}, nil }
	buildConfigFromFlagsFn = func(_, _ string) (*rest.Config, error) {
		t.Fatalf("BuildConfigFromFlags should not be called when in-cluster succeeds")
		return nil, nil
	}
	t.Cleanup(func() {
		inClusterConfigFn = origInCluster
		buildConfigFromFlagsFn = origBuild
	})

	cfg, err := getKubeConfig()
	if err != nil {
		t.Fatalf("getKubeConfig() error: %v", err)
	}
	if cfg.Host != "https://in-cluster" {
		t.Fatalf("cfg.Host=%q, want %q", cfg.Host, "https://in-cluster")
	}
}

func TestGetKubeConfig_FallbackToHomeKubeconfig(t *testing.T) {
	origKubeconfig := kubeconfig
	t.Cleanup(func() { kubeconfig = origKubeconfig })

	kubeconfig = ""

	origInCluster := inClusterConfigFn
	origHome := userHomeDirFn
	origBuild := buildConfigFromFlagsFn
	inClusterConfigFn = func() (*rest.Config, error) { return nil, errors.New("no in-cluster") }
	userHomeDirFn = func() (string, error) { return "/tmp/flexinfer-test-home", nil }
	buildConfigFromFlagsFn = func(_, path string) (*rest.Config, error) {
		want := "/tmp/flexinfer-test-home/.kube/config"
		if path != want {
			t.Fatalf("BuildConfigFromFlags path=%q, want %q", path, want)
		}
		return &rest.Config{Host: "https://from-kubeconfig"}, nil
	}
	t.Cleanup(func() {
		inClusterConfigFn = origInCluster
		userHomeDirFn = origHome
		buildConfigFromFlagsFn = origBuild
	})

	cfg, err := getKubeConfig()
	if err != nil {
		t.Fatalf("getKubeConfig() error: %v", err)
	}
	if cfg.Host != "https://from-kubeconfig" {
		t.Fatalf("cfg.Host=%q, want %q", cfg.Host, "https://from-kubeconfig")
	}
}

func TestRunList_NoItems(t *testing.T) {
	stubNotifyContext(t)
	stubInClusterConfig(t)

	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	stubClient(t, c)

	cmd, stdout, _ := newTestCmd()

	origAll := allNs
	origNs := namespace
	t.Cleanup(func() {
		allNs = origAll
		namespace = origNs
	})

	allNs = false
	namespace = "flexinfer-system"

	if err := runList(cmd, nil); err != nil {
		t.Fatalf("runList() error: %v", err)
	}
	if !strings.Contains(stdout.String(), "No ModelDeployments found") {
		t.Fatalf("stdout=%q, expected no deployments message", stdout.String())
	}
}

func TestRunList_PrintsItems(t *testing.T) {
	stubNotifyContext(t)
	stubInClusterConfig(t)

	replicas := int32(0)
	minReplicas := int32(0)
	lastAccess := metav1.NewTime(time.Now().Add(-10 * time.Minute))

	md := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "qwen3-8b-amd",
			Namespace: "flexinfer-system",
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend:       "llamacpp",
			Model:         "HF://Qwen/Qwen3-8B-GGUF",
			Replicas:      &replicas,
			MinReplicas:   &minReplicas,
			ModelCacheRef: func() *string { s := "node-local"; return &s }(),
		},
		Status: aiv1alpha1.ModelDeploymentStatus{
			LastAccessTime:  &lastAccess,
			TokensPerSecond: "12.3",
			Phase:           "",
			AllocatedGPU:    &aiv1alpha1.GPUAllocation{Node: "cblevins-5930k", Vendor: "amd", Architecture: "gfx1100", MemoryMB: 24576},
			Endpoints:       &aiv1alpha1.ModelEndpoints{Internal: "http://x", External: "https://y"},
			Conditions:      nil,
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(md).Build()
	stubClient(t, c)

	cmd, stdout, _ := newTestCmd()

	origAll := allNs
	origNs := namespace
	t.Cleanup(func() {
		allNs = origAll
		namespace = origNs
	})

	allNs = false
	namespace = "flexinfer-system"

	if err := runList(cmd, nil); err != nil {
		t.Fatalf("runList() error: %v", err)
	}
	out := stdout.String()
	if !(strings.Contains(out, "NAME") && strings.Contains(out, "BACKEND") && strings.Contains(out, "STATUS")) {
		t.Fatalf("expected header in stdout, got: %q", out)
	}
	if !strings.Contains(out, "qwen3-8b-amd") {
		t.Fatalf("expected deployment name in stdout, got: %q", out)
	}
	if !strings.Contains(out, "Scaled(0)") {
		t.Fatalf("expected Scaled(0) in stdout, got: %q", out)
	}
	if !strings.Contains(out, "0 (0→1)") {
		t.Fatalf("expected serverless replicas formatting in stdout, got: %q", out)
	}
	if !strings.Contains(out, "12.3/s") {
		t.Fatalf("expected TPS formatting in stdout, got: %q", out)
	}
}

func TestRunScale_UpdatesReplicas(t *testing.T) {
	stubNotifyContext(t)
	stubInClusterConfig(t)

	replicas := int32(1)
	md := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen3-8b-amd", Namespace: "flexinfer-system"},
		Spec:       aiv1alpha1.ModelDeploymentSpec{Backend: "llamacpp", Model: "x", Replicas: &replicas},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(md).Build()
	stubClient(t, c)

	cmd, _, _ := newTestCmd()

	origNs := namespace
	t.Cleanup(func() { namespace = origNs })
	namespace = "flexinfer-system"

	if err := runScale(cmd, []string{"qwen3-8b-amd", "3"}); err != nil {
		t.Fatalf("runScale() error: %v", err)
	}

	got := &aiv1alpha1.ModelDeployment{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "qwen3-8b-amd", Namespace: "flexinfer-system"}, got); err != nil {
		t.Fatalf("Get updated ModelDeployment: %v", err)
	}
	if got.Spec.Replicas == nil || *got.Spec.Replicas != 3 {
		t.Fatalf("updated replicas=%v, want 3", got.Spec.Replicas)
	}
}

func TestRunScale_InvalidReplicas(t *testing.T) {
	cmd, _, _ := newTestCmd()
	if err := runScale(cmd, []string{"qwen3-8b-amd", "nope"}); err == nil {
		t.Fatalf("expected error for invalid replicas")
	}
	if err := runScale(cmd, []string{"qwen3-8b-amd", "-1"}); err == nil {
		t.Fatalf("expected error for negative replicas")
	}
}

func TestRunDelete_ForceDeletes(t *testing.T) {
	stubNotifyContext(t)
	stubInClusterConfig(t)

	md := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen3-8b-amd", Namespace: "flexinfer-system"},
		Spec:       aiv1alpha1.ModelDeploymentSpec{Backend: "llamacpp", Model: "x"},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(md).Build()
	stubClient(t, c)

	cmd, _, _ := newTestCmd()

	origForce := forceDelete
	origNs := namespace
	t.Cleanup(func() {
		forceDelete = origForce
		namespace = origNs
	})
	forceDelete = true
	namespace = "flexinfer-system"

	if err := runDelete(cmd, []string{"qwen3-8b-amd"}); err != nil {
		t.Fatalf("runDelete() error: %v", err)
	}

	got := &aiv1alpha1.ModelDeployment{}
	err := c.Get(context.Background(), types.NamespacedName{Name: "qwen3-8b-amd", Namespace: "flexinfer-system"}, got)
	if err == nil || !apierrors.IsNotFound(err) {
		t.Fatalf("expected NotFound after delete, got: %v", err)
	}
}

func TestRunDelete_PromptCancel(t *testing.T) {
	stubNotifyContext(t)
	stubInClusterConfig(t)

	md := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen3-8b-amd", Namespace: "flexinfer-system"},
		Spec:       aiv1alpha1.ModelDeploymentSpec{Backend: "llamacpp", Model: "x"},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(md).Build()
	stubClient(t, c)

	cmd, stdout, _ := newTestCmd()
	cmd.SetIn(strings.NewReader("n\n"))

	origForce := forceDelete
	origNs := namespace
	t.Cleanup(func() {
		forceDelete = origForce
		namespace = origNs
	})
	forceDelete = false
	namespace = "flexinfer-system"

	if err := runDelete(cmd, []string{"qwen3-8b-amd"}); err != nil {
		t.Fatalf("runDelete() error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Deletion cancelled") {
		t.Fatalf("expected cancel message, got: %q", stdout.String())
	}

	got := &aiv1alpha1.ModelDeployment{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "qwen3-8b-amd", Namespace: "flexinfer-system"}, got); err != nil {
		t.Fatalf("expected object to still exist after cancel: %v", err)
	}
}

func TestRunCacheStatus_PrintsMemorySummary(t *testing.T) {
	stubNotifyContext(t)
	stubInClusterConfig(t)

	mcMem := &aiv1alpha1.ModelCache{
		ObjectMeta: metav1.ObjectMeta{Name: "ram-cache", Namespace: "flexinfer-system"},
		Spec: aiv1alpha1.ModelCacheSpec{
			StorageStrategy: aiv1alpha1.StorageStrategyMemory,
			Source:          "HF://Qwen/Qwen3-8B-GGUF",
		},
		Status: aiv1alpha1.ModelCacheStatus{
			Phase:      aiv1alpha1.ModelCachePhaseReady,
			ReadyNodes: 2,
			TotalNodes: 2,
			Path:       "/dev/shm/models/qwen",
		},
	}
	mcDisk := &aiv1alpha1.ModelCache{
		ObjectMeta: metav1.ObjectMeta{Name: "disk-cache", Namespace: "flexinfer-system"},
		Spec: aiv1alpha1.ModelCacheSpec{
			StorageStrategy: aiv1alpha1.StorageStrategyNodeLocal,
			Source:          "HF://Qwen/Qwen2.5-7B",
		},
		Status: aiv1alpha1.ModelCacheStatus{
			Phase:      aiv1alpha1.ModelCachePhasePending,
			ReadyNodes: 1,
			TotalNodes: 2,
			Path:       "/var/lib/flexinfer/cache",
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(mcMem, mcDisk).Build()
	stubClient(t, c)

	cmd, stdout, _ := newTestCmd()

	origAll := allNs
	origNs := namespace
	t.Cleanup(func() {
		allNs = origAll
		namespace = origNs
	})
	allNs = false
	namespace = "flexinfer-system"

	if err := runCacheStatus(cmd, nil); err != nil {
		t.Fatalf("runCacheStatus() error: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "RAM-cached models:") || !strings.Contains(out, "ram-cache") {
		t.Fatalf("expected RAM cache summary in output, got: %q", out)
	}
}

func TestRunStatus_PrintsEvents(t *testing.T) {
	stubNotifyContext(t)
	stubInClusterConfig(t)

	replicas := int32(1)
	minReplicas := int32(0)
	lastAccess := metav1.NewTime(time.Now().Add(-2 * time.Minute))
	condTime := metav1.NewTime(time.Now().Add(-30 * time.Second))

	md := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen3-8b-amd", Namespace: "flexinfer-system"},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend:     "llamacpp",
			Model:       "HF://Qwen/Qwen3-8B-GGUF",
			Replicas:    &replicas,
			MinReplicas: &minReplicas,
		},
		Status: aiv1alpha1.ModelDeploymentStatus{
			Phase:          aiv1alpha1.ModelDeploymentPhaseRunning,
			LastAccessTime: &lastAccess,
			AllocatedGPU:   &aiv1alpha1.GPUAllocation{Node: "cblevins-gtx980ti", Vendor: "nvidia", Architecture: "sm_52", MemoryMB: 6144},
			Conditions: []metav1.Condition{
				{
					Type:               "Ready",
					Status:             metav1.ConditionFalse,
					Reason:             "WaitingForGPU",
					Message:            "no suitable GPU available",
					LastTransitionTime: condTime,
				},
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(md).Build()
	stubClient(t, c)

	// Fake events clientset
	ev := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{Name: "e1", Namespace: "flexinfer-system"},
		InvolvedObject: corev1.ObjectReference{
			Kind:      "ModelDeployment",
			Name:      "qwen3-8b-amd",
			Namespace: "flexinfer-system",
		},
		Type:          "Normal",
		Reason:        "Created",
		Message:       "deployment created",
		LastTimestamp: metav1.NewTime(time.Now().Add(-1 * time.Minute)),
	}
	cs := k8sfake.NewSimpleClientset(ev)

	origClientset := newClientsetFn
	newClientsetFn = func(_ *rest.Config) (kubernetes.Interface, error) {
		return cs, nil
	}
	t.Cleanup(func() { newClientsetFn = origClientset })

	cmd, stdout, _ := newTestCmd()

	origNs := namespace
	t.Cleanup(func() { namespace = origNs })
	namespace = "flexinfer-system"

	if err := runStatus(cmd, []string{"qwen3-8b-amd"}); err != nil {
		t.Fatalf("runStatus() error: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "GPU Allocation:") || !strings.Contains(out, "sm_52") {
		t.Fatalf("expected GPU allocation info in output, got: %q", out)
	}
	if !strings.Contains(out, "Recent Events:") || !strings.Contains(out, "Created") {
		t.Fatalf("expected Recent Events in output, got: %q", out)
	}
	if !strings.Contains(out, "Message: no suitable GPU available") {
		t.Fatalf("expected condition message in output, got: %q", out)
	}
}

func TestRunLogs_NoPods(t *testing.T) {
	stubNotifyContext(t)
	stubInClusterConfig(t)

	cs := k8sfake.NewSimpleClientset()
	origClientset := newClientsetFn
	newClientsetFn = func(_ *rest.Config) (kubernetes.Interface, error) {
		return cs, nil
	}
	t.Cleanup(func() { newClientsetFn = origClientset })

	cmd, _, _ := newTestCmd()

	origNs := namespace
	t.Cleanup(func() { namespace = origNs })
	namespace = "flexinfer-system"

	err := runLogs(cmd, []string{"qwen3-8b-amd"})
	if err == nil || !strings.Contains(err.Error(), "no pods found") {
		t.Fatalf("expected no pods error, got: %v", err)
	}
}

func TestRunBenchmark_DeletesJobAndResultsConfigMap(t *testing.T) {
	stubNotifyContext(t)
	stubInClusterConfig(t)

	md := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen3-8b-amd", Namespace: "flexinfer-system"},
		Spec:       aiv1alpha1.ModelDeploymentSpec{Backend: "llamacpp", Model: "x"},
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen3-8b-amd-benchmark", Namespace: "flexinfer-system"},
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen3-8b-amd-benchmark-results", Namespace: "flexinfer-system"},
		Data:       map[string]string{"tokensPerSecond": "123"},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(md, job, cm).Build()
	stubClient(t, c)

	cmd, _, _ := newTestCmd()

	origAll := allNs
	origNs := namespace
	t.Cleanup(func() {
		allNs = origAll
		namespace = origNs
	})
	allNs = false
	namespace = "flexinfer-system"

	if err := runBenchmark(cmd, []string{"qwen3-8b-amd"}); err != nil {
		t.Fatalf("runBenchmark() error: %v", err)
	}

	// Job and results ConfigMap should be deleted.
	if err := c.Get(ctx(), types.NamespacedName{Name: job.Name, Namespace: job.Namespace}, &batchv1.Job{}); err == nil {
		t.Fatalf("expected benchmark job to be deleted")
	}
	if err := c.Get(ctx(), types.NamespacedName{Name: cm.Name, Namespace: cm.Namespace}, &corev1.ConfigMap{}); err == nil {
		t.Fatalf("expected benchmark results configmap to be deleted")
	}
}
