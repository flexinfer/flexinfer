package commands

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/gaming"
)

const gameTestNode = "cblevins-7900xtx"

func stubGameEnv(t *testing.T) {
	t.Helper()
	stubInClusterConfig(t)
	stubNotifyContext(t)
	origNs := namespace
	namespace = "flexinfer-system"
	origOwner := currentUserFn
	currentUserFn = func() string { return "testuser" }
	origFor, origIdle, origWait := gameStartFor, gameStartIdleTimeout, gameStartWait
	gameStartFor, gameStartIdleTimeout, gameStartWait = 0, 0, false
	origOwnerFlag := gameStartOwner
	gameStartOwner = ""
	origStopWait := gameStopWait
	gameStopWait = false
	t.Cleanup(func() {
		namespace = origNs
		currentUserFn = origOwner
		gameStartFor, gameStartIdleTimeout, gameStartWait = origFor, origIdle, origWait
		gameStartOwner = origOwnerFlag
		gameStopWait = origStopWait
	})
}

func TestRunGameStart_CreatesSessionWithTTLAndOwner(t *testing.T) {
	stubGameEnv(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	stubClient(t, c)
	cmd, stdout, _ := newTestCmd()

	gameStartFor = 90 * time.Minute
	if err := runGameStart(cmd, []string{gameTestNode}); err != nil {
		t.Fatalf("runGameStart: %v", err)
	}
	gs := &aiv1alpha2.GamingSession{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "gaming-" + gameTestNode, Namespace: "flexinfer-system"}, gs); err != nil {
		t.Fatalf("session not created: %v", err)
	}
	if gs.Spec.NodeName != gameTestNode || gs.Spec.Owner != "testuser" {
		t.Fatalf("spec = %+v, want node + defaulted owner", gs.Spec)
	}
	if gs.Spec.TTL == nil || gs.Spec.TTL.Duration != 90*time.Minute {
		t.Fatalf("ttl = %v, want 90m", gs.Spec.TTL)
	}
	if !strings.Contains(stdout.String(), "created") {
		t.Fatalf("stdout %q does not confirm creation", stdout.String())
	}
}

func TestRunGameStart_RefusesDoubleBooking(t *testing.T) {
	stubGameEnv(t)
	existing := &aiv1alpha2.GamingSession{
		ObjectMeta: metav1.ObjectMeta{Name: "gaming-" + gameTestNode, Namespace: "flexinfer-system"},
		Spec:       aiv1alpha2.GamingSessionSpec{NodeName: gameTestNode, Mode: "gaming", Owner: "someone"},
		Status:     aiv1alpha2.GamingSessionStatus{Phase: aiv1alpha2.GamingSessionActive},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(existing).Build()
	stubClient(t, c)
	cmd, _, _ := newTestCmd()

	err := runGameStart(cmd, []string{gameTestNode})
	if err == nil || !strings.Contains(err.Error(), "already has gaming session") {
		t.Fatalf("expected double-booking refusal, got %v", err)
	}
}

func TestRunGameStop_DeletesSession(t *testing.T) {
	stubGameEnv(t)
	existing := &aiv1alpha2.GamingSession{
		ObjectMeta: metav1.ObjectMeta{Name: "gaming-" + gameTestNode, Namespace: "flexinfer-system"},
		Spec:       aiv1alpha2.GamingSessionSpec{NodeName: gameTestNode, Mode: "gaming"},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(existing).Build()
	stubClient(t, c)
	cmd, stdout, _ := newTestCmd()

	if err := runGameStop(cmd, []string{gameTestNode}); err != nil {
		t.Fatalf("runGameStop: %v", err)
	}
	gs := &aiv1alpha2.GamingSession{}
	err := c.Get(context.Background(), types.NamespacedName{Name: existing.Name, Namespace: "flexinfer-system"}, gs)
	if err == nil {
		t.Fatal("session still exists after stop")
	}
	if !strings.Contains(stdout.String(), "deleted") {
		t.Fatalf("stdout %q does not confirm deletion", stdout.String())
	}
}

func TestRunGameStop_NoSession(t *testing.T) {
	stubGameEnv(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	stubClient(t, c)
	cmd, _, _ := newTestCmd()

	if err := runGameStop(cmd, []string{gameTestNode}); err == nil {
		t.Fatal("expected error when no session exists")
	}
}

func TestRunGameStatus_RendersConditions(t *testing.T) {
	stubGameEnv(t)
	expires := metav1.NewTime(time.Now().Add(time.Hour))
	existing := &aiv1alpha2.GamingSession{
		ObjectMeta: metav1.ObjectMeta{Name: "gaming-" + gameTestNode, Namespace: "flexinfer-system"},
		Spec:       aiv1alpha2.GamingSessionSpec{NodeName: gameTestNode, Mode: "gaming", Owner: "tester"},
		Status: aiv1alpha2.GamingSessionStatus{
			Phase:     aiv1alpha2.GamingSessionActive,
			ExpiresAt: &expires,
			Message:   "node in gaming mode",
			Conditions: []metav1.Condition{
				{Type: aiv1alpha2.GamingConditionSunshineReady, Status: metav1.ConditionTrue, Reason: aiv1alpha2.GamingReasonSunshineUp, LastTransitionTime: metav1.Now()},
				{Type: aiv1alpha2.GamingConditionStreamActive, Status: metav1.ConditionTrue, Reason: aiv1alpha2.GamingReasonClientConnected, LastTransitionTime: metav1.Now()},
			},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(existing).Build()
	stubClient(t, c)
	cmd, stdout, _ := newTestCmd()

	if err := runGameStatus(cmd, []string{gameTestNode}); err != nil {
		t.Fatalf("runGameStatus: %v", err)
	}
	outStr := stdout.String()
	for _, want := range []string{"gaming-" + gameTestNode, "Active", "SunshineReady", "StreamActive"} {
		if !strings.Contains(outStr, want) {
			t.Fatalf("status output missing %q:\n%s", want, outStr)
		}
	}
}

func TestRunGamePreflight_FailsWithoutApprovedImages(t *testing.T) {
	stubGameEnv(t)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "flexinfer-runtime-xyz",
			Namespace: "flexinfer-system",
			Labels:    map[string]string{"app.kubernetes.io/component": "flexinfer-runtime"},
		},
		Spec: corev1.PodSpec{
			NodeName:    gameTestNode,
			HostNetwork: true,
			Containers:  []corev1.Container{{Name: "runtime", Image: "registry.test/runtime@sha256:aaa"}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(pod).Build()
	stubClient(t, c)
	cmd, stdout, _ := newTestCmd()

	err := runGamePreflight(cmd, []string{gameTestNode})
	if err == nil {
		t.Fatal("expected preflight failure with no approved images")
	}
	if !strings.Contains(stdout.String(), "no approved gaming images") {
		t.Fatalf("output missing approved-images failure:\n%s", stdout.String())
	}
}

func TestRunGamePreflight_PassesWithPolicyAndWarnsDarkLabels(t *testing.T) {
	stubGameEnv(t)
	image := "registry.test/runtime@sha256:aaa"
	policy := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: gaming.PolicyConfigMapName, Namespace: "flexinfer-system"},
		Data:       map[string]string{gaming.KeyApprovedImages: image},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "flexinfer-runtime-xyz",
			Namespace: "flexinfer-system",
			Labels:    map[string]string{"app.kubernetes.io/component": "flexinfer-runtime"},
		},
		Spec: corev1.PodSpec{
			NodeName:    gameTestNode,
			HostNetwork: true,
			Containers:  []corev1.Container{{Name: "runtime", Image: image}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	pinnedOnly := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "solo-on-node", Namespace: "flexinfer-system"},
		Spec: aiv1alpha2.ModelSpec{
			Backend:       "vllm",
			NodeSelector:  map[string]string{"kubernetes.io/hostname": gameTestNode},
			ServiceLabels: []string{"only-here"},
		},
		Status: aiv1alpha2.ModelStatus{Phase: aiv1alpha2.ModelPhaseReady},
	}
	covered := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "covered-on-node", Namespace: "flexinfer-system"},
		Spec: aiv1alpha2.ModelSpec{
			Backend:       "vllm",
			NodeSelector:  map[string]string{"kubernetes.io/hostname": gameTestNode},
			ServiceLabels: []string{"shared-label"},
		},
	}
	twin := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "twin-elsewhere", Namespace: "flexinfer-system"},
		Spec: aiv1alpha2.ModelSpec{
			Backend:       "vllm",
			NodeSelector:  map[string]string{"kubernetes.io/hostname": "cblevins-5930k"},
			ServiceLabels: []string{"shared-label"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithRuntimeObjects(policy, pod, pinnedOnly, covered, twin).Build()
	stubClient(t, c)
	cmd, stdout, _ := newTestCmd()

	if err := runGamePreflight(cmd, []string{gameTestNode}); err != nil {
		t.Fatalf("preflight should pass with approved image, got %v\n%s", err, stdout.String())
	}
	outStr := stdout.String()
	if !strings.Contains(outStr, "only-here") {
		t.Fatalf("expected dark-label warning for 'only-here':\n%s", outStr)
	}
	if strings.Contains(outStr, "shared-label") {
		t.Fatalf("covered label must not be warned about:\n%s", outStr)
	}
	if !strings.Contains(outStr, "Preflight OK") {
		t.Fatalf("expected Preflight OK:\n%s", outStr)
	}
}
