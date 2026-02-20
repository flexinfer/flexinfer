// Package e2e provides routing-focused end-to-end tests.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

const (
	proxyNamespace            = "flexinfer-system"
	routingPrefixAnnotation   = "flexinfer.ai/routing"
	routingPrefixStrategy     = "prefix"
	routingTargetServiceDNS   = "service-dns"
	routingModelLabel         = "flexinfer.ai/model"
	routingMetricTargetHits   = "flexinfer_proxy_routing_target_hits_total"
	routingMetricDecisions    = "flexinfer_proxy_routing_decisions_total"
	routingOutcomePod         = "pod"
	routingOutcomeFallback    = "service-fallback"
	routingKeySourceCanonical = "canonical"
	maxRoutingModelCandidates = 2
)

// TestRoutingPrefixCanonicalDeterminism validates that repeated canonical
// context requests for a multi-replica model consistently route to one pod.
func TestRoutingPrefixCanonicalDeterminism(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	RequireFlexInfer(t)
	RequireGPU(t)
	requireRoutingE2E(t)

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()

	f := NewFixture(t)
	modelName := provisionPrefixRoutingModel(t, ctx, f, 2)
	proxyEndpoint, err := getProxyEndpoint(ctx)
	if err != nil {
		t.Fatalf("Failed to get proxy endpoint: %v", err)
	}

	beforeTargets, err := routingMetricByLabel(ctx, proxyEndpoint, routingMetricTargetHits, map[string]string{
		"model":    modelName,
		"strategy": routingPrefixStrategy,
	}, "target")
	if err != nil {
		t.Fatalf("Failed to read routing target metrics: %v", err)
	}
	beforeDecisions, err := routingMetricByTwoLabels(ctx, proxyEndpoint, routingMetricDecisions, map[string]string{
		"model":    modelName,
		"strategy": routingPrefixStrategy,
	}, "key_source", "outcome")
	if err != nil {
		t.Fatalf("Failed to read routing decision metrics: %v", err)
	}

	for i := 0; i < 6; i++ {
		_, err := sendCanonicalContextChat(ctx, proxyEndpoint, modelName, fmt.Sprintf("doc-alpha-%d", i%2), nil)
		if err != nil {
			t.Fatalf("Canonical request %d failed: %v", i+1, err)
		}
	}

	afterTargets, err := routingMetricByLabel(ctx, proxyEndpoint, routingMetricTargetHits, map[string]string{
		"model":    modelName,
		"strategy": routingPrefixStrategy,
	}, "target")
	if err != nil {
		t.Fatalf("Failed to read routing target metrics after requests: %v", err)
	}
	afterDecisions, err := routingMetricByTwoLabels(ctx, proxyEndpoint, routingMetricDecisions, map[string]string{
		"model":    modelName,
		"strategy": routingPrefixStrategy,
	}, "key_source", "outcome")
	if err != nil {
		t.Fatalf("Failed to read routing decision metrics after requests: %v", err)
	}

	targetDelta := diffMetricValues(beforeTargets, afterTargets)
	decisionDelta := diffMetricValues(beforeDecisions, afterDecisions)

	podTargets := positivePodTargets(targetDelta)
	if len(podTargets) != 1 {
		t.Fatalf("expected requests to hit exactly one pod target for stable canonical routing, got targets=%v delta=%v", podTargets, targetDelta)
	}

	if got := decisionDelta[routingKeySourceCanonical+"|"+routingOutcomePod]; got <= 0 {
		t.Fatalf("expected canonical pod routing decisions to increase, got delta=%v", got)
	}

	if got := targetDelta[routingTargetServiceDNS]; got > 1 {
		t.Fatalf("expected near-zero service fallback during steady canonical routing, got service-dns delta=%v", got)
	}
}

// TestRoutingFallbackDuringPodRestart validates that prefix routing degrades
// safely during pod restart/churn and recovers to direct pod routing.
func TestRoutingFallbackDuringPodRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	RequireFlexInfer(t)
	RequireGPU(t)
	requireRoutingE2E(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	f := NewFixture(t)
	modelName := provisionPrefixRoutingModel(t, ctx, f, 2)
	proxyEndpoint, err := getProxyEndpoint(ctx)
	if err != nil {
		t.Fatalf("Failed to get proxy endpoint: %v", err)
	}

	beforeDecisions, err := routingMetricByTwoLabels(ctx, proxyEndpoint, routingMetricDecisions, map[string]string{
		"model":    modelName,
		"strategy": routingPrefixStrategy,
	}, "key_source", "outcome")
	if err != nil {
		t.Fatalf("Failed to read routing decision metrics: %v", err)
	}

	if err := restartDeployment(ctx, proxyNamespace, modelName); err != nil {
		t.Fatalf("Failed to restart deployment %s: %v", modelName, err)
	}

	attempts := 0
	successes := 0
	until := time.Now().Add(90 * time.Second)
	for time.Now().Before(until) {
		_, err := sendCanonicalContextChat(ctx, proxyEndpoint, modelName, "restart-doc", map[string]string{
			"X-Flexinfer-Cache-Key": fmt.Sprintf("bad key with spaces %d", attempts),
		})
		attempts++
		if err == nil {
			successes++
		}
		time.Sleep(2 * time.Second)
	}

	if attempts == 0 || successes == 0 {
		t.Fatalf("expected at least one successful request during churn window; attempts=%d successes=%d", attempts, successes)
	}

	if err := waitForDeploymentReady(ctx, proxyNamespace, modelName, 2); err != nil {
		t.Fatalf("Deployment did not recover after restart: %v", err)
	}
	if err := f.WaitForPodsReady(ctx, proxyNamespace, routingModelLabel+"="+modelName, 2); err != nil {
		t.Fatalf("Pods were not ready after restart: %v", err)
	}

	for i := 0; i < 3; i++ {
		_, err := sendCanonicalContextChat(ctx, proxyEndpoint, modelName, fmt.Sprintf("post-restart-doc-%d", i), nil)
		if err != nil {
			t.Fatalf("Post-restart validation request %d failed: %v", i+1, err)
		}
	}

	afterDecisions, err := routingMetricByTwoLabels(ctx, proxyEndpoint, routingMetricDecisions, map[string]string{
		"model":    modelName,
		"strategy": routingPrefixStrategy,
	}, "key_source", "outcome")
	if err != nil {
		t.Fatalf("Failed to read routing decision metrics after restart: %v", err)
	}
	decisionDelta := diffMetricValues(beforeDecisions, afterDecisions)

	if sumDecisionOutcome(decisionDelta, routingOutcomePod) <= 0 {
		t.Fatalf("expected pod routing decisions to recover after restart, got deltas=%v", decisionDelta)
	}
	if got := decisionDelta[routingKeySourceCanonical+"|"+routingOutcomePod]; got <= 0 {
		t.Fatalf("expected malformed explicit keys to fall back to canonical pod routing, got canonical|pod delta=%v (all deltas=%v)", got, decisionDelta)
	}
	if sumDecisionOutcome(decisionDelta, routingOutcomeFallback) >= float64(attempts+3) {
		t.Fatalf("all decisions used service fallback; expected pod recovery, got deltas=%v", decisionDelta)
	}
}

func provisionPrefixRoutingModel(t *testing.T, ctx context.Context, f *Fixture, replicas int32) string {
	t.Helper()

	candidates := readyChatModels(t, ctx, proxyNamespace)
	if len(candidates) == 0 {
		t.Skip("No Ready chat-compatible models found for routing e2e")
	}
	if len(candidates) > maxRoutingModelCandidates {
		candidates = candidates[:maxRoutingModelCandidates]
	}

	var lastErr error
	for _, base := range candidates {
		modelName := f.UniqueModelName("routing-prefix")
		enabled := true
		idle := metav1.Duration{Duration: 10 * time.Minute}

		model := &aiv1alpha2.Model{
			ObjectMeta: metav1.ObjectMeta{
				Name:      modelName,
				Namespace: proxyNamespace,
				Annotations: map[string]string{
					routingPrefixAnnotation: routingPrefixStrategy,
				},
			},
			Spec: aiv1alpha2.ModelSpec{
				Backend: base.Spec.Backend,
				Source:  base.Spec.Source,
				GPU:     cloneRoutingGPU(base.Spec.GPU),
				Serverless: &aiv1alpha2.ServerlessSpec{
					Enabled:     &enabled,
					MinReplicas: &replicas,
					IdleTimeout: &idle,
				},
			},
		}

		t.Logf("Trying routed model clone from base %s (backend=%s)", base.Name, base.Spec.Backend)
		if err := f.CreateModel(ctx, model); err != nil {
			lastErr = err
			t.Logf("Failed to create routed model %s: %v", modelName, err)
			continue
		}

		waitCtx, waitCancel := context.WithTimeout(ctx, 2*time.Minute)
		err := f.WaitForPodsReady(waitCtx, proxyNamespace, routingModelLabel+"="+modelName, int(replicas))
		waitCancel()
		if err != nil {
			lastErr = err
			t.Logf("Model %s did not reach %d ready pods: %v", modelName, replicas, err)
			deleteModelBestEffort(ctx, modelName, proxyNamespace, f)
			continue
		}

		return modelName
	}

	t.Skipf("Unable to provision a %d-replica prefix-routed model (cluster capacity or scheduling constraints). Last error: %v", replicas, lastErr)
	return ""
}

func cloneRoutingGPU(base *aiv1alpha2.GPUSpec) *aiv1alpha2.GPUSpec {
	one := int32(1)
	if base == nil {
		return &aiv1alpha2.GPUSpec{
			Vendor: aiv1alpha2.GPUVendorAMD,
			Count:  &one,
		}
	}
	clone := base.DeepCopy()
	clone.Shared = ""
	clone.Priority = nil
	clone.Count = &one
	return clone
}

func deleteModelBestEffort(ctx context.Context, name, ns string, f *Fixture) {
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
	}
	_ = k8sClient.Delete(ctx, model)
	_ = f.waitForModelDeleted(ctx, name, ns)
}

func restartDeployment(ctx context.Context, ns, name string) error {
	patch := []byte(fmt.Sprintf(
		`{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":"%s"}}}}}`,
		time.Now().UTC().Format(time.RFC3339Nano),
	))
	_, err := clientset.AppsV1().Deployments(ns).Patch(ctx, name, types.StrategicMergePatchType, patch, metav1.PatchOptions{})
	return err
}

func waitForDeploymentReady(ctx context.Context, ns, name string, replicas int32) error {
	return wait.PollUntilContextTimeout(ctx, 2*time.Second, timeouts.ModelReady, true, func(ctx context.Context) (bool, error) {
		dep, err := clientset.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		return deploymentReady(dep, replicas), nil
	})
}

func deploymentReady(dep *appsv1.Deployment, replicas int32) bool {
	return dep.Status.ObservedGeneration >= dep.Generation &&
		dep.Status.UpdatedReplicas >= replicas &&
		dep.Status.ReadyReplicas >= replicas &&
		dep.Status.AvailableReplicas >= replicas
}

func sendCanonicalContextChat(ctx context.Context, endpoint, model, doc string, headers map[string]string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeouts.InferenceRequest)
	defer cancel()

	payload := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": "Answer strictly from provided document context."},
			{"role": "user", "content": "Summarize the document in one sentence."},
		},
		"document_context": doc,
		"max_tokens":       96,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal routing request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create routing request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return "", fmt.Errorf("routing request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("routing request failed with status %d: %s", resp.StatusCode, string(data))
	}

	var out ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("failed to decode routing response: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("no choices in routing response")
	}
	return out.Choices[0].Message.Content, nil
}

func requireRoutingE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("FLEXINFER_E2E_ROUTING") != "1" {
		t.Skip("Set FLEXINFER_E2E_ROUTING=1 to run routing churn/provisioning e2e tests")
	}
}

type metricSample struct {
	labels map[string]string
	value  float64
}

func routingMetricByLabel(ctx context.Context, endpoint, metric string, required map[string]string, groupLabel string) (map[string]float64, error) {
	samples, err := fetchMetricSamples(ctx, endpoint, metric)
	if err != nil {
		return nil, err
	}

	out := make(map[string]float64)
	for _, sample := range samples {
		if !hasRequiredLabels(sample.labels, required) {
			continue
		}
		key := sample.labels[groupLabel]
		out[key] += sample.value
	}
	return out, nil
}

func routingMetricByTwoLabels(ctx context.Context, endpoint, metric string, required map[string]string, labelA, labelB string) (map[string]float64, error) {
	samples, err := fetchMetricSamples(ctx, endpoint, metric)
	if err != nil {
		return nil, err
	}

	out := make(map[string]float64)
	for _, sample := range samples {
		if !hasRequiredLabels(sample.labels, required) {
			continue
		}
		key := sample.labels[labelA] + "|" + sample.labels[labelB]
		out[key] += sample.value
	}
	return out, nil
}

func fetchMetricSamples(ctx context.Context, endpoint, metric string) ([]metricSample, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/metrics", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics request: %w", err)
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch metrics: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("metrics endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read metrics response: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	result := make([]metricSample, 0, 16)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !(strings.HasPrefix(line, metric+"{") || strings.HasPrefix(line, metric+" ")) {
			continue
		}
		sample, ok := parseMetricLine(metric, line)
		if ok {
			result = append(result, sample)
		}
	}
	return result, nil
}

func parseMetricLine(metric, line string) (metricSample, bool) {
	rest := strings.TrimPrefix(line, metric)
	labels := map[string]string{}

	if strings.HasPrefix(rest, "{") {
		end := strings.IndexByte(rest, '}')
		if end < 0 {
			return metricSample{}, false
		}
		labels = parseMetricLabels(rest[1:end])
		rest = strings.TrimSpace(rest[end+1:])
	} else {
		rest = strings.TrimSpace(rest)
	}

	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return metricSample{}, false
	}
	value, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return metricSample{}, false
	}
	return metricSample{labels: labels, value: value}, true
}

func parseMetricLabels(raw string) map[string]string {
	out := make(map[string]string)
	if strings.TrimSpace(raw) == "" {
		return out
	}
	for _, part := range splitLabels(raw) {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.Trim(strings.TrimSpace(kv[1]), `"`)
		out[key] = strings.ReplaceAll(val, `\"`, `"`)
	}
	return out
}

func splitLabels(raw string) []string {
	parts := make([]string, 0, 6)
	var b strings.Builder
	inQuotes := false
	escaped := false

	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if escaped {
			b.WriteByte(ch)
			escaped = false
			continue
		}
		if ch == '\\' {
			b.WriteByte(ch)
			escaped = true
			continue
		}
		if ch == '"' {
			inQuotes = !inQuotes
			b.WriteByte(ch)
			continue
		}
		if ch == ',' && !inQuotes {
			parts = append(parts, strings.TrimSpace(b.String()))
			b.Reset()
			continue
		}
		b.WriteByte(ch)
	}
	if b.Len() > 0 {
		parts = append(parts, strings.TrimSpace(b.String()))
	}
	return parts
}

func hasRequiredLabels(labels, required map[string]string) bool {
	for k, want := range required {
		if labels[k] != want {
			return false
		}
	}
	return true
}

func diffMetricValues(before, after map[string]float64) map[string]float64 {
	out := make(map[string]float64)
	for key, v := range after {
		out[key] = v - before[key]
	}
	for key, v := range before {
		if _, ok := after[key]; !ok {
			out[key] = -v
		}
	}
	return out
}

func positivePodTargets(delta map[string]float64) []string {
	targets := make([]string, 0, len(delta))
	for target, value := range delta {
		if target == routingTargetServiceDNS {
			continue
		}
		if value > 0 {
			targets = append(targets, target)
		}
	}
	return targets
}

func sumDecisionOutcome(delta map[string]float64, outcome string) float64 {
	sum := 0.0
	for key, value := range delta {
		if strings.HasSuffix(key, "|"+outcome) {
			sum += value
		}
	}
	return sum
}
