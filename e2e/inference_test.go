// Package e2e provides inference-specific end-to-end tests.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// TestInferenceOllama tests a complete inference request to an Ollama model.
func TestInferenceOllama(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	RequireFlexInfer(t)
	RequireGPU(t)

	f := NewFixture(t)
	ctx := context.Background()
	ensureNamespace(t)

	// Create model
	modelName := f.UniqueModelName("inference-ollama")
	model := OllamaModel(modelName, "llama3.2:1b")

	if err := f.CreateModel(ctx, model); err != nil {
		t.Fatalf("Failed to create model: %v", err)
	}

	// Wait for model to be ready
	t.Log("Waiting for model to be ready...")
	readyModel, err := f.WaitForModelReady(ctx, modelName, *namespace)
	if err != nil {
		t.Fatalf("Model did not become ready: %v", err)
	}

	t.Logf("Model ready, endpoint: %s", readyModel.Status.Endpoint)

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
func TestInferenceColdStart(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	RequireFlexInfer(t)
	RequireGPU(t)

	f := NewFixture(t)
	ctx := context.Background()
	ensureNamespace(t)

	// Create serverless model with short idle timeout
	modelName := f.UniqueModelName("coldstart")
	idleTimeout := metav1.Duration{Duration: 30 * time.Second}
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      modelName,
			Namespace: *namespace,
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "ollama",
			Source:  "ollama://llama3.2:1b",
			GPU: &aiv1alpha2.GPUSpec{
				Count: int32Ptr(1),
			},
			Serverless: &aiv1alpha2.ServerlessSpec{
				IdleTimeout: &idleTimeout,
			},
		},
	}

	if err := f.CreateModel(ctx, model); err != nil {
		t.Fatalf("Failed to create model: %v", err)
	}

	// Wait for model to be ready initially
	t.Log("Waiting for model to be ready...")
	_, err := f.WaitForModelReady(ctx, modelName, *namespace)
	if err != nil {
		t.Fatalf("Model did not become ready: %v", err)
	}

	// Wait for model to scale to zero (idle timeout)
	t.Logf("Waiting %v for model to scale to zero...", idleTimeout.Duration+30*time.Second)
	time.Sleep(idleTimeout.Duration + 30*time.Second)

	// Check model phase
	currentModel, err := f.GetModel(ctx, modelName, *namespace)
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
	readyModel, err := f.GetModel(ctx, modelName, *namespace)
	if err != nil {
		t.Fatalf("Failed to get model after cold start: %v", err)
	}

	if readyModel.Status.Phase != aiv1alpha2.ModelPhaseReady {
		t.Fatalf("Expected model to be Ready after cold start, got %s", readyModel.Status.Phase)
	}

	t.Log("Cold start test passed")
}

// TestInferenceMultiModel tests inference across multiple models.
func TestInferenceMultiModel(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	RequireFlexInfer(t)
	RequireGPU(t)

	f := NewFixture(t)
	ctx := context.Background()
	ensureNamespace(t)

	// Create two models
	model1Name := f.UniqueModelName("multi-model-1")
	model2Name := f.UniqueModelName("multi-model-2")

	model1 := OllamaModel(model1Name, "llama3.2:1b")
	model2 := OllamaModel(model2Name, "qwen2.5:0.5b")

	if err := f.CreateModel(ctx, model1); err != nil {
		t.Fatalf("Failed to create model1: %v", err)
	}
	if err := f.CreateModel(ctx, model2); err != nil {
		t.Fatalf("Failed to create model2: %v", err)
	}

	// Wait for both models to be ready
	t.Log("Waiting for models to be ready...")
	_, err := f.WaitForModelReady(ctx, model1Name, *namespace)
	if err != nil {
		t.Fatalf("Model1 did not become ready: %v", err)
	}
	_, err = f.WaitForModelReady(ctx, model2Name, *namespace)
	if err != nil {
		t.Fatalf("Model2 did not become ready: %v", err)
	}

	// Get proxy endpoint
	proxyEndpoint, err := getProxyEndpoint(ctx)
	if err != nil {
		t.Fatalf("Failed to get proxy endpoint: %v", err)
	}

	// Send requests to both models
	t.Log("Sending requests to both models...")

	response1, err := sendChatCompletion(ctx, proxyEndpoint, model1Name, "Say 'model one'")
	if err != nil {
		t.Fatalf("Request to model1 failed: %v", err)
	}
	t.Logf("Model1 response: %s", response1)

	response2, err := sendChatCompletion(ctx, proxyEndpoint, model2Name, "Say 'model two'")
	if err != nil {
		t.Fatalf("Request to model2 failed: %v", err)
	}
	t.Logf("Model2 response: %s", response2)

	// Verify both responses are non-empty
	if response1 == "" || response2 == "" {
		t.Fatal("Expected non-empty responses from both models")
	}

	t.Log("Multi-model inference test passed")
}

// TestInferenceStreaming tests streaming inference responses.
func TestInferenceStreaming(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	RequireFlexInfer(t)
	RequireGPU(t)

	f := NewFixture(t)
	ctx := context.Background()
	ensureNamespace(t)

	// Create model
	modelName := f.UniqueModelName("streaming")
	model := OllamaModel(modelName, "llama3.2:1b")

	if err := f.CreateModel(ctx, model); err != nil {
		t.Fatalf("Failed to create model: %v", err)
	}

	// Wait for model to be ready
	t.Log("Waiting for model to be ready...")
	_, err := f.WaitForModelReady(ctx, modelName, *namespace)
	if err != nil {
		t.Fatalf("Model did not become ready: %v", err)
	}

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

// getProxyEndpoint returns the FlexInfer proxy endpoint URL.
func getProxyEndpoint(ctx context.Context) (string, error) {
	svc, err := clientset.CoreV1().Services("flexinfer-system").Get(ctx, "flexinfer-proxy", metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get proxy service: %w", err)
	}

	// Use ClusterIP for in-cluster tests
	if svc.Spec.ClusterIP != "" && svc.Spec.ClusterIP != "None" {
		port := int32(80)
		for _, p := range svc.Spec.Ports {
			if p.Name == "http" || p.Port == 80 {
				port = p.Port
				break
			}
		}
		return fmt.Sprintf("http://%s:%d", svc.Spec.ClusterIP, port), nil
	}

	return "", fmt.Errorf("no accessible endpoint found for proxy service")
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
	defer resp.Body.Close()

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
	defer resp.Body.Close()

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
