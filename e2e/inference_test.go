// Package e2e provides inference-specific end-to-end tests.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"sort"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// TestInferenceOllama tests a complete inference request via the proxy.
// Uses an existing Ready model in the system namespace.
func TestInferenceOllama(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	RequireFlexInfer(t)
	RequireGPU(t)

	ctx := context.Background()
	const proxyNS = "flexinfer-system"

	// Find an existing Ready model to test against
	modelName := findReadyModel(t, ctx, proxyNS)
	t.Logf("Using existing model: %s", modelName)

	// Get proxy service endpoint
	proxyEndpoint, err := getProxyEndpoint(ctx)
	if err != nil {
		t.Fatalf("Failed to get proxy endpoint: %v", err)
	}

	// Send inference request
	t.Log("Sending inference request...")
	response, err := sendChatCompletion(ctx, proxyEndpoint, modelName, "Say hello in exactly 3 words.")
	if err != nil {
		t.Fatalf("Inference request failed: %v", err)
	}

	t.Logf("Response: %s", response)

	// Verify response is non-empty
	if response == "" {
		t.Fatal("Expected non-empty response from model")
	}

	t.Log("Inference test passed")
}

// TestInferenceColdStart tests inference with cold start (scale from zero).
// Clones the spec from an existing Ready model so model data is pre-available,
// then transitions to MinReplicas=0 to enable scale-to-zero behavior.
// Tries each base model to find one whose node has spare GPU capacity.
func TestInferenceColdStart(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	RequireFlexInfer(t)
	RequireGPU(t)

	f := NewFixture(t)
	ctx := context.Background()

	const proxyNS = "flexinfer-system"

	idleTimeout := metav1.Duration{Duration: 30 * time.Second}

	// Try each ready model as a base — some may fail to schedule if the
	// node where the model PVC lives has no spare GPU capacity.
	candidates := readyChatModels(t, ctx, proxyNS)
	if len(candidates) == 0 {
		t.Skip("No Ready chat-compatible models found")
	}

	var modelName string
	for _, baseModel := range candidates {
		modelName = f.UniqueModelName("coldstart")
		t.Logf("Trying base model: %s (backend=%s)", baseModel.Name, baseModel.Spec.Backend)

		model := &aiv1alpha2.Model{
			ObjectMeta: metav1.ObjectMeta{
				Name:      modelName,
				Namespace: proxyNS,
			},
			Spec: aiv1alpha2.ModelSpec{
				Backend: baseModel.Spec.Backend,
				Source:  baseModel.Spec.Source,
				GPU: &aiv1alpha2.GPUSpec{
					Vendor: aiv1alpha2.GPUVendorAMD,
					Count:  int32Ptr(1),
				},
				Serverless: &aiv1alpha2.ServerlessSpec{
					MinReplicas: int32Ptr(1),
					IdleTimeout: &idleTimeout,
				},
			},
		}

		if err := f.CreateModel(ctx, model); err != nil {
			t.Fatalf("Failed to create model: %v", err)
		}

		// Phase 1: Wait 30s and check if the pod is still Pending (scheduling failure).
		// If the pod scheduled (Running/ContainerCreating), move to Phase 2.
		time.Sleep(30 * time.Second)
		pods, _ := f.GetPodsByLabel(ctx, proxyNS,
			fmt.Sprintf("flexinfer.ai/model=%s", modelName))
		podPending := len(pods) == 0
		for _, p := range pods {
			if p.Status.Phase == corev1.PodPending {
				podPending = true
			}
		}
		if podPending {
			t.Logf("Model from %s has Pending pod (scheduling issue), trying next", baseModel.Name)
			if delErr := k8sClient.Delete(ctx, model); delErr != nil {
				t.Logf("Warning: failed to clean up model %s: %v", modelName, delErr)
			}
			_ = f.waitForModelDeleted(ctx, modelName, proxyNS)
			modelName = ""
			continue
		}

		// Phase 2: Pod scheduled — wait for full model ready timeout.
		t.Logf("Pod scheduled, waiting for model %s to become Ready...", modelName)
		_, err := f.WaitForModelReady(ctx, modelName, proxyNS)
		if err == nil {
			t.Logf("Model %s became Ready from base %s", modelName, baseModel.Name)
			break
		}

		t.Logf("Model from %s did not become ready: %v", baseModel.Name, err)
		if delErr := k8sClient.Delete(ctx, model); delErr != nil {
			t.Logf("Warning: failed to clean up model %s: %v", modelName, delErr)
		}
		_ = f.waitForModelDeleted(ctx, modelName, proxyNS)
		modelName = ""
	}

	if modelName == "" {
		t.Skip("No base model could schedule a clone with available GPU capacity")
	}

	// Transition to MinReplicas=0 so the model can scale to zero after idle timeout.
	// With MinReplicas=1 the controller keeps at least one replica running.
	t.Log("Setting MinReplicas=0 to enable scale-to-zero...")
	currentModel, err := f.GetModel(ctx, modelName, proxyNS)
	if err != nil {
		t.Fatalf("Failed to get model: %v", err)
	}
	currentModel.Spec.Serverless.MinReplicas = int32Ptr(0)
	if err := k8sClient.Update(ctx, currentModel); err != nil {
		t.Fatalf("Failed to update model MinReplicas: %v", err)
	}

	// Wait for model to scale to zero (idle timeout)
	t.Logf("Waiting %v for model to scale to zero...", idleTimeout.Duration+30*time.Second)
	time.Sleep(idleTimeout.Duration + 30*time.Second)

	// Check model phase
	currentModel, err = f.GetModel(ctx, modelName, proxyNS)
	if err != nil {
		t.Fatalf("Failed to get model: %v", err)
	}
	t.Logf("Model phase before cold start: %s", currentModel.Status.Phase)

	// Get proxy endpoint
	proxyEndpoint, err := getProxyEndpoint(ctx)
	if err != nil {
		t.Fatalf("Failed to get proxy endpoint: %v", err)
	}

	// Send inference request (should trigger cold start)
	t.Log("Sending inference request to trigger cold start...")
	start := time.Now()
	response, err := sendChatCompletionWithTimeout(ctx, proxyEndpoint, modelName,
		"Say hello", timeouts.ColdStart)
	if err != nil {
		t.Fatalf("Cold start inference failed: %v", err)
	}
	coldStartDuration := time.Since(start)

	t.Logf("Cold start completed in %v", coldStartDuration)
	t.Logf("Response: %s", response)

	// Verify model is now ready again
	readyModel, err := f.GetModel(ctx, modelName, proxyNS)
	if err != nil {
		t.Fatalf("Failed to get model after cold start: %v", err)
	}

	if readyModel.Status.Phase != aiv1alpha2.ModelPhaseReady {
		t.Fatalf("Expected model to be Ready after cold start, got %s", readyModel.Status.Phase)
	}

	t.Log("Cold start test passed")
}

// TestInferenceMultiModel tests inference across multiple models.
// Uses existing Ready models to verify the proxy correctly routes to different backends.
func TestInferenceMultiModel(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	RequireFlexInfer(t)
	RequireGPU(t)

	ctx := context.Background()
	const proxyNS = "flexinfer-system"

	// Find at least 2 existing Ready models
	modelNames := findReadyModels(t, ctx, proxyNS, 2)
	t.Logf("Using existing models: %v", modelNames)

	// Get proxy endpoint
	proxyEndpoint, err := getProxyEndpoint(ctx)
	if err != nil {
		t.Fatalf("Failed to get proxy endpoint: %v", err)
	}

	// Send requests to both models
	t.Log("Sending requests to both models...")

	response1, err := sendChatCompletion(ctx, proxyEndpoint, modelNames[0], "Say 'model one'")
	if err != nil {
		t.Fatalf("Request to %s failed: %v", modelNames[0], err)
	}
	t.Logf("%s response: %s", modelNames[0], response1)

	response2, err := sendChatCompletion(ctx, proxyEndpoint, modelNames[1], "Say 'model two'")
	if err != nil {
		t.Fatalf("Request to %s failed: %v", modelNames[1], err)
	}
	t.Logf("%s response: %s", modelNames[1], response2)

	// Verify both responses are non-empty
	if response1 == "" || response2 == "" {
		t.Fatal("Expected non-empty responses from both models")
	}

	t.Log("Multi-model inference test passed")
}

// TestInferenceStreaming tests streaming inference responses.
// Uses an existing Ready model to verify SSE streaming works through the proxy.
func TestInferenceStreaming(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	RequireFlexInfer(t)
	RequireGPU(t)

	ctx := context.Background()
	const proxyNS = "flexinfer-system"

	// Find an existing Ready model to test against
	modelName := findReadyModel(t, ctx, proxyNS)
	t.Logf("Using existing model: %s", modelName)

	// Get proxy endpoint
	proxyEndpoint, err := getProxyEndpoint(ctx)
	if err != nil {
		t.Fatalf("Failed to get proxy endpoint: %v", err)
	}

	// Send streaming request
	t.Log("Sending streaming inference request...")
	chunkCount, err := sendStreamingChatCompletion(ctx, proxyEndpoint, modelName,
		"Count from 1 to 5, one number per line.")
	if err != nil {
		t.Fatalf("Streaming request failed: %v", err)
	}

	t.Logf("Received %d streaming chunks", chunkCount)

	// Verify we received multiple chunks
	if chunkCount < 2 {
		t.Fatalf("Expected multiple streaming chunks, got %d", chunkCount)
	}

	t.Log("Streaming inference test passed")
}

// Helper functions

// chatCompatibleBackends lists backends that support the /v1/chat/completions API.
// Image generation backends (diffusers) are excluded.
var chatCompatibleBackends = map[string]bool{
	"ollama":   true,
	"mlc-llm":  true,
	"llamacpp": true,
	"vllm":     true,
}

// backendPriority defines selection order for tests — lower is preferred.
// llamacpp and ollama have fast first-inference; mlc-llm may have long warmup.
var backendPriority = map[string]int{
	"llamacpp": 0,
	"ollama":   1,
	"vllm":     2,
	"mlc-llm":  3,
}

// readyChatModels returns all Ready chat-compatible models, sorted by backend priority.
func readyChatModels(t *testing.T, ctx context.Context, ns string) []aiv1alpha2.Model {
	t.Helper()
	var list aiv1alpha2.ModelList
	if err := k8sClient.List(ctx, &list, client.InNamespace(ns)); err != nil {
		t.Fatalf("Failed to list models: %v", err)
	}
	var result []aiv1alpha2.Model
	for _, m := range list.Items {
		if m.Status.Phase == aiv1alpha2.ModelPhaseReady && chatCompatibleBackends[m.Spec.Backend] {
			result = append(result, m)
		}
	}
	// Sort by backend priority (prefer llamacpp > ollama > vllm > mlc-llm)
	sort.Slice(result, func(i, j int) bool {
		return backendPriority[result[i].Spec.Backend] < backendPriority[result[j].Spec.Backend]
	})
	return result
}

// findReadyModel returns the name of the best available Ready chat-compatible model.
// Prefers backends with faster inference (llamacpp, ollama) over slower ones (mlc-llm).
func findReadyModel(t *testing.T, ctx context.Context, ns string) string {
	t.Helper()
	models := readyChatModels(t, ctx, ns)
	if len(models) == 0 {
		t.Skip("No Ready chat-compatible models found in namespace " + ns)
	}
	return models[0].Name
}

// findReadyModels returns up to n Ready model names that support chat completions.
// Skips the test if fewer than n models are available.
func findReadyModels(t *testing.T, ctx context.Context, ns string, n int) []string {
	t.Helper()
	models := readyChatModels(t, ctx, ns)
	if len(models) < n {
		t.Skipf("Need %d Ready chat-compatible models, found %d in namespace %s", n, len(models), ns)
	}
	names := make([]string, n)
	for i := 0; i < n; i++ {
		names[i] = models[i].Name
	}
	return names
}

// findReadyChatModelObj returns a full Ready Model object for cloning in tests.
// Prefers backends with faster inference for reliable cold start testing.
func findReadyChatModelObj(t *testing.T, ctx context.Context, ns string) *aiv1alpha2.Model {
	t.Helper()
	models := readyChatModels(t, ctx, ns)
	if len(models) == 0 {
		t.Skip("No Ready chat-compatible models found in namespace " + ns)
	}
	return &models[0]
}

// getProxyEndpoint returns the FlexInfer proxy endpoint URL.
// If running outside the cluster, it starts a kubectl port-forward.
func getProxyEndpoint(ctx context.Context) (string, error) {
	svc, err := clientset.CoreV1().Services("flexinfer-system").Get(ctx, "flexinfer-proxy", metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get proxy service: %w", err)
	}

	port := int32(80)
	for _, p := range svc.Spec.Ports {
		if p.Name == "http" || p.Port == 80 {
			port = p.Port
			break
		}
	}

	// Try ClusterIP first (works for in-cluster tests)
	clusterEndpoint := fmt.Sprintf("http://%s:%d", svc.Spec.ClusterIP, port)
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", svc.Spec.ClusterIP, port), 2*time.Second)
	if err == nil {
		conn.Close()
		return clusterEndpoint, nil
	}

	// Outside cluster — start port-forward
	localPort := 18080
	cmd := exec.CommandContext(ctx, "kubectl", "port-forward",
		"-n", "flexinfer-system", "svc/flexinfer-proxy",
		fmt.Sprintf("%d:%d", localPort, port))
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to start port-forward: %w", err)
	}

	// Wait for port-forward to be ready
	for i := 0; i < 20; i++ {
		time.Sleep(250 * time.Millisecond)
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", localPort), time.Second)
		if err == nil {
			conn.Close()
			return fmt.Sprintf("http://127.0.0.1:%d", localPort), nil
		}
	}

	return "", fmt.Errorf("port-forward to proxy service did not become ready")
}

// ChatCompletionRequest represents an OpenAI-compatible chat completion request.
type ChatCompletionRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Stream   bool          `json:"stream,omitempty"`
}

// ChatMessage represents a chat message.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatCompletionResponse represents an OpenAI-compatible chat completion response.
type ChatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// sendChatCompletion sends a chat completion request and returns the response content.
func sendChatCompletion(ctx context.Context, endpoint, model, prompt string) (string, error) {
	return sendChatCompletionWithTimeout(ctx, endpoint, model, prompt, timeouts.InferenceRequest)
}

// sendChatCompletionWithTimeout sends a chat completion request with a custom timeout.
func sendChatCompletionWithTimeout(ctx context.Context, endpoint, model, prompt string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	reqBody := ChatCompletionRequest{
		Model: model,
		Messages: []ChatMessage{
			{Role: "user", Content: prompt},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var chatResp ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	return chatResp.Choices[0].Message.Content, nil
}

// sendStreamingChatCompletion sends a streaming chat completion request and returns the chunk count.
func sendStreamingChatCompletion(ctx context.Context, endpoint, model, prompt string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, timeouts.InferenceRequest)
	defer cancel()

	reqBody := ChatCompletionRequest{
		Model:  model,
		Stream: true,
		Messages: []ChatMessage{
			{Role: "user", Content: prompt},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Count SSE chunks
	chunkCount := 0
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			// Count "data:" prefixes (SSE events)
			data := string(buf[:n])
			for i := 0; i < len(data); i++ {
				if i+5 <= len(data) && data[i:i+5] == "data:" {
					chunkCount++
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return chunkCount, fmt.Errorf("error reading stream: %w", err)
		}
	}

	return chunkCount, nil
}
