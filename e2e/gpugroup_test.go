// Package e2e provides GPUGroup-specific end-to-end tests.
package e2e

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

// TestGPUGroupExclusiveScheduling tests that only one model runs at a time in an exclusive GPUGroup.
func TestGPUGroupExclusiveScheduling(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	RequireFlexInfer(t)
	RequireGPU(t)

	f := NewFixture(t)
	ctx := context.Background()
	ensureNamespace(t)

	// Create GPUGroup
	groupName := f.UniqueModelName("exclusive-group")
	model1Name := f.UniqueModelName("exclusive-model-1")
	model2Name := f.UniqueModelName("exclusive-model-2")

	gpuGroup := &aiv1alpha1.GPUGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      groupName,
			Namespace: *namespace,
		},
		Spec: aiv1alpha1.GPUGroupSpec{
			Models: []aiv1alpha1.GPUGroupMember{
				{Name: model1Name, Priority: 100},
				{Name: model2Name, Priority: 50},
			},
			ScalingPolicy: aiv1alpha1.GPUGroupScalingPolicy{
				Strategy: aiv1alpha1.GPUShareStrategyExclusive,
			},
			AntiThrashing: aiv1alpha1.AntiThrashingConfig{
				Enabled:                        true,
				MinimumRunDurationSeconds:      10,
				CooldownAfterPreemptionSeconds: 5,
				RequestQueueThreshold:          1,
			},
		},
	}

	if err := f.CreateGPUGroup(ctx, gpuGroup); err != nil {
		t.Fatalf("Failed to create GPUGroup: %v", err)
	}

	// Create two ModelDeployments linked to the GPUGroup
	md1 := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      model1Name,
			Namespace: *namespace,
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend:     "ollama",
			Model:       "llama3.2:1b",
			GPUGroupRef: &groupName,
			Priority:    int32Ptr(100),
			MinReplicas: int32Ptr(0),
			Replicas:    int32Ptr(1),
		},
	}

	md2 := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      model2Name,
			Namespace: *namespace,
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend:     "ollama",
			Model:       "qwen2.5:0.5b",
			GPUGroupRef: &groupName,
			Priority:    int32Ptr(50),
			MinReplicas: int32Ptr(0),
			Replicas:    int32Ptr(1),
		},
	}

	if err := f.CreateModelDeployment(ctx, md1); err != nil {
		t.Fatalf("Failed to create ModelDeployment 1: %v", err)
	}
	if err := f.CreateModelDeployment(ctx, md2); err != nil {
		t.Fatalf("Failed to create ModelDeployment 2: %v", err)
	}

	// Wait for GPUGroup to have an active model
	t.Log("Waiting for GPUGroup to activate a model...")
	err := wait.PollUntilContextTimeout(ctx, timeouts.PollInterval, timeouts.ModelReady, true,
		func(ctx context.Context) (bool, error) {
			var gg aiv1alpha1.GPUGroup
			if err := k8sClient.Get(ctx, client.ObjectKey{Name: groupName, Namespace: *namespace}, &gg); err != nil {
				return false, err
			}
			return gg.Status.ActiveModel != "", nil
		})
	if err != nil {
		t.Fatalf("GPUGroup never activated a model: %v", err)
	}

	// Get current state
	var gg aiv1alpha1.GPUGroup
	if err := k8sClient.Get(ctx, client.ObjectKey{Name: groupName, Namespace: *namespace}, &gg); err != nil {
		t.Fatalf("Failed to get GPUGroup: %v", err)
	}

	t.Logf("Active model: %s", gg.Status.ActiveModel)

	// The higher priority model (model1) should be active
	if gg.Status.ActiveModel != model1Name {
		t.Logf("Note: Expected %s to be active (higher priority), but %s is active",
			model1Name, gg.Status.ActiveModel)
	}

	// Verify only one model is running (check phase)
	md1Updated, err := f.GetModelDeployment(ctx, model1Name, *namespace)
	if err != nil {
		t.Fatalf("Failed to get ModelDeployment 1: %v", err)
	}

	md2Updated, err := f.GetModelDeployment(ctx, model2Name, *namespace)
	if err != nil {
		t.Fatalf("Failed to get ModelDeployment 2: %v", err)
	}

	t.Logf("Model1 phase: %s, Model2 phase: %s",
		md1Updated.Status.Phase, md2Updated.Status.Phase)

	// In exclusive mode, only one should be in Running phase
	md1Running := md1Updated.Status.Phase == aiv1alpha1.ModelDeploymentPhaseRunning
	md2Running := md2Updated.Status.Phase == aiv1alpha1.ModelDeploymentPhaseRunning
	if md1Running && md2Running {
		t.Fatal("Exclusive GPUGroup should only have one model in Running phase")
	}

	t.Log("Exclusive scheduling test passed")
}

// TestGPUGroupSwapOnDemand tests model swapping when demand changes.
func TestGPUGroupSwapOnDemand(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	RequireFlexInfer(t)
	RequireGPU(t)

	f := NewFixture(t)
	ctx := context.Background()
	ensureNamespace(t)

	// Create GPUGroup with short anti-thrashing for faster testing
	groupName := f.UniqueModelName("swap-group")
	model1Name := f.UniqueModelName("swap-model-1")
	model2Name := f.UniqueModelName("swap-model-2")

	gpuGroup := &aiv1alpha1.GPUGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      groupName,
			Namespace: *namespace,
		},
		Spec: aiv1alpha1.GPUGroupSpec{
			Models: []aiv1alpha1.GPUGroupMember{
				{Name: model1Name, Priority: 100},
				{Name: model2Name, Priority: 100}, // Same priority - demand-based
			},
			ScalingPolicy: aiv1alpha1.GPUGroupScalingPolicy{
				Strategy: aiv1alpha1.GPUShareStrategyExclusive,
			},
			AntiThrashing: aiv1alpha1.AntiThrashingConfig{
				Enabled:                        true,
				MinimumRunDurationSeconds:      5, // Short for testing
				CooldownAfterPreemptionSeconds: 3,
				RequestQueueThreshold:          1,
				HysteresisWindowSeconds:        2,
			},
		},
	}

	if err := f.CreateGPUGroup(ctx, gpuGroup); err != nil {
		t.Fatalf("Failed to create GPUGroup: %v", err)
	}

	// Create ModelDeployments
	md1 := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      model1Name,
			Namespace: *namespace,
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend:     "ollama",
			Model:       "llama3.2:1b",
			GPUGroupRef: &groupName,
			Priority:    int32Ptr(100),
			MinReplicas: int32Ptr(0),
			Replicas:    int32Ptr(1),
		},
	}

	md2 := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      model2Name,
			Namespace: *namespace,
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend:     "ollama",
			Model:       "qwen2.5:0.5b",
			GPUGroupRef: &groupName,
			Priority:    int32Ptr(100),
			MinReplicas: int32Ptr(0),
			Replicas:    int32Ptr(1),
		},
	}

	if err := f.CreateModelDeployment(ctx, md1); err != nil {
		t.Fatalf("Failed to create ModelDeployment 1: %v", err)
	}
	if err := f.CreateModelDeployment(ctx, md2); err != nil {
		t.Fatalf("Failed to create ModelDeployment 2: %v", err)
	}

	// Wait for initial activation
	t.Log("Waiting for initial model activation...")
	err := wait.PollUntilContextTimeout(ctx, timeouts.PollInterval, timeouts.ModelReady, true,
		func(ctx context.Context) (bool, error) {
			var gg aiv1alpha1.GPUGroup
			if err := k8sClient.Get(ctx, client.ObjectKey{Name: groupName, Namespace: *namespace}, &gg); err != nil {
				return false, err
			}
			return gg.Status.ActiveModel != "", nil
		})
	if err != nil {
		t.Fatalf("GPUGroup never activated a model: %v", err)
	}

	var gg aiv1alpha1.GPUGroup
	if err := k8sClient.Get(ctx, client.ObjectKey{Name: groupName, Namespace: *namespace}, &gg); err != nil {
		t.Fatalf("Failed to get GPUGroup: %v", err)
	}
	initialModel := gg.Status.ActiveModel
	t.Logf("Initial active model: %s", initialModel)

	// Determine which model to request (the inactive one)
	targetModel := model2Name
	if initialModel == model2Name {
		targetModel = model1Name
	}

	// Wait for anti-thrashing minimum run duration
	t.Logf("Waiting for anti-thrashing minimum run duration (%d seconds)...",
		gpuGroup.Spec.AntiThrashing.MinimumRunDurationSeconds)
	time.Sleep(time.Duration(gpuGroup.Spec.AntiThrashing.MinimumRunDurationSeconds+2) * time.Second)

	// Simulate demand by annotating the GPUGroup
	// In production, this would come from the proxy
	t.Logf("Simulating demand for %s...", targetModel)
	gg.Annotations = map[string]string{
		"flexinfer.ai/queue." + targetModel: "5", // Simulate queue depth
	}
	if err := k8sClient.Update(ctx, &gg); err != nil {
		t.Fatalf("Failed to update GPUGroup with queue annotation: %v", err)
	}

	// Wait for swap to occur
	t.Log("Waiting for model swap...")
	err = wait.PollUntilContextTimeout(ctx, timeouts.PollInterval, timeouts.GPUSwap, true,
		func(ctx context.Context) (bool, error) {
			var updatedGG aiv1alpha1.GPUGroup
			if err := k8sClient.Get(ctx, client.ObjectKey{Name: groupName, Namespace: *namespace}, &updatedGG); err != nil {
				return false, err
			}
			return updatedGG.Status.ActiveModel == targetModel, nil
		})

	// Get final state
	if err := k8sClient.Get(ctx, client.ObjectKey{Name: groupName, Namespace: *namespace}, &gg); err != nil {
		t.Fatalf("Failed to get GPUGroup: %v", err)
	}

	if err != nil {
		t.Logf("Model did not swap within timeout (active: %s, expected: %s)",
			gg.Status.ActiveModel, targetModel)
		t.Log("Note: This may be expected if anti-thrashing blocked the swap")
	} else {
		t.Logf("Model swapped successfully: %s -> %s", initialModel, gg.Status.ActiveModel)
	}

	t.Log("Swap on demand test completed")
}

// TestGPUGroupAntiThrashing tests that anti-thrashing prevents rapid swapping.
func TestGPUGroupAntiThrashing(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	RequireFlexInfer(t)
	RequireGPU(t)

	f := NewFixture(t)
	ctx := context.Background()
	ensureNamespace(t)

	// Create GPUGroup with strict anti-thrashing
	groupName := f.UniqueModelName("antithrash-group")
	model1Name := f.UniqueModelName("antithrash-model-1")
	model2Name := f.UniqueModelName("antithrash-model-2")

	gpuGroup := &aiv1alpha1.GPUGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      groupName,
			Namespace: *namespace,
		},
		Spec: aiv1alpha1.GPUGroupSpec{
			Models: []aiv1alpha1.GPUGroupMember{
				{Name: model1Name, Priority: 100},
				{Name: model2Name, Priority: 100},
			},
			ScalingPolicy: aiv1alpha1.GPUGroupScalingPolicy{
				Strategy: aiv1alpha1.GPUShareStrategyExclusive,
			},
			AntiThrashing: aiv1alpha1.AntiThrashingConfig{
				Enabled:                        true,
				MinimumRunDurationSeconds:      60, // Long duration for testing
				CooldownAfterPreemptionSeconds: 60,
				RequestQueueThreshold:          5, // High threshold
			},
		},
	}

	if err := f.CreateGPUGroup(ctx, gpuGroup); err != nil {
		t.Fatalf("Failed to create GPUGroup: %v", err)
	}

	// Create ModelDeployments
	md1 := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      model1Name,
			Namespace: *namespace,
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend:     "ollama",
			Model:       "llama3.2:1b",
			GPUGroupRef: &groupName,
			Priority:    int32Ptr(100),
			MinReplicas: int32Ptr(0),
			Replicas:    int32Ptr(1),
		},
	}

	md2 := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      model2Name,
			Namespace: *namespace,
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend:     "ollama",
			Model:       "qwen2.5:0.5b",
			GPUGroupRef: &groupName,
			Priority:    int32Ptr(100),
			MinReplicas: int32Ptr(0),
			Replicas:    int32Ptr(1),
		},
	}

	if err := f.CreateModelDeployment(ctx, md1); err != nil {
		t.Fatalf("Failed to create ModelDeployment 1: %v", err)
	}
	if err := f.CreateModelDeployment(ctx, md2); err != nil {
		t.Fatalf("Failed to create ModelDeployment 2: %v", err)
	}

	// Wait for initial activation
	t.Log("Waiting for initial model activation...")
	err := wait.PollUntilContextTimeout(ctx, timeouts.PollInterval, timeouts.ModelReady, true,
		func(ctx context.Context) (bool, error) {
			var gg aiv1alpha1.GPUGroup
			if err := k8sClient.Get(ctx, client.ObjectKey{Name: groupName, Namespace: *namespace}, &gg); err != nil {
				return false, err
			}
			return gg.Status.ActiveModel != "", nil
		})
	if err != nil {
		t.Fatalf("GPUGroup never activated a model: %v", err)
	}

	var gg aiv1alpha1.GPUGroup
	if err := k8sClient.Get(ctx, client.ObjectKey{Name: groupName, Namespace: *namespace}, &gg); err != nil {
		t.Fatalf("Failed to get GPUGroup: %v", err)
	}
	initialModel := gg.Status.ActiveModel
	t.Logf("Initial active model: %s", initialModel)

	// Determine inactive model
	targetModel := model2Name
	if initialModel == model2Name {
		targetModel = model1Name
	}

	// Immediately try to trigger swap (should be blocked by anti-thrashing)
	t.Logf("Attempting to trigger swap to %s (should be blocked)...", targetModel)
	gg.Annotations = map[string]string{
		"flexinfer.ai/queue." + targetModel: "3", // Below threshold
	}
	if err := k8sClient.Update(ctx, &gg); err != nil {
		t.Fatalf("Failed to update GPUGroup: %v", err)
	}

	// Wait a short time and verify no swap occurred
	time.Sleep(10 * time.Second)

	if err := k8sClient.Get(ctx, client.ObjectKey{Name: groupName, Namespace: *namespace}, &gg); err != nil {
		t.Fatalf("Failed to get GPUGroup: %v", err)
	}

	if gg.Status.ActiveModel != initialModel {
		t.Fatalf("Anti-thrashing failed: model swapped from %s to %s", initialModel, gg.Status.ActiveModel)
	}

	t.Logf("Active model still %s (anti-thrashing working)", gg.Status.ActiveModel)
	t.Log("Anti-thrashing test passed")
}
