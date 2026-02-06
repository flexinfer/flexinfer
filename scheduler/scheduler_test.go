package scheduler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	extenderv1 "k8s.io/kube-scheduler/extender/v1"
)

type fakeCache struct {
	nodes      map[string]*corev1.Node
	configMaps map[string]*corev1.ConfigMap
}

func (f *fakeCache) GetNode(name string) (*corev1.Node, error) {
	if n, ok := f.nodes[name]; ok {
		return n, nil
	}
	return nil, fmt.Errorf("not found")
}

func (f *fakeCache) GetConfigMap(namespace, name string) (*corev1.ConfigMap, error) {
	key := namespace + "/" + name
	if cm, ok := f.configMaps[key]; ok {
		return cm, nil
	}
	return nil, fmt.Errorf("not found")
}

func TestScore(t *testing.T) {
	cache := &fakeCache{
		nodes: map[string]*corev1.Node{
			"node1": {
				ObjectMeta: metav1.ObjectMeta{
					Name: "node1",
					Annotations: map[string]string{
						"flexinfer.ai/gpu.util":       "50",
						"flexinfer.ai/cost":           "5",
						"flexinfer.ai/kv-cache-usage": "0.9",
					},
				},
			},
			"node2": {
				ObjectMeta: metav1.ObjectMeta{
					Name: "node2",
					Annotations: map[string]string{
						"flexinfer.ai/gpu.util":       "10",
						"flexinfer.ai/cost":           "2",
						"flexinfer.ai/kv-cache-usage": "0.1",
					},
				},
			},
		},
		configMaps: map[string]*corev1.ConfigMap{
			"default/md-benchmark-results": {
				ObjectMeta: metav1.ObjectMeta{
					Name:      "md-benchmark-results",
					Namespace: "default",
				},
				Data: map[string]string{"tokensPerSecond": "100"},
			},
		},
	}

	sched := &Scheduler{cache: cache, tpsWeight: 0.7, utilWeight: 0.2, costWeight: 0.1, cacheWeight: 0.5}

	args := extenderv1.ExtenderArgs{
		Pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "p",
				Namespace: "default",
				Labels:    map[string]string{"modeldeployment_cr": "md"},
			},
		},
		NodeNames: &[]string{"node1", "node2"},
	}

	body, _ := json.Marshal(args)
	req := httptest.NewRequest("POST", "/score", bytes.NewBuffer(body))
	rr := httptest.NewRecorder()

	sched.Score(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rr.Code)
	}

	var result []extenderv1.HostPriority
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(result) != 2 {
		t.Fatalf("expected 2 results got %d", len(result))
	}

	// Calculate expected scores
	// Node 1: TPS=100(0.7) - Util=50(0.2) - Cost=5(0.1) - Cache=0.9(0.5) = 70 - 10 - 0.5 - 0.45 = 59.05 -> 59
	// Node 2: TPS=100(0.7) - Util=10(0.2) - Cost=2(0.1) - Cache=0.1(0.5) = 70 - 2 - 0.2 - 0.05 = 67.75 -> 67

	score1 := getScore(result, "node1")
	score2 := getScore(result, "node2")

	if score2 <= score1 {
		t.Errorf("expected node2 score (%d) > node1 score (%d) due to lower cache usage", score2, score1)
	}
}

func TestScore_UsesGlobalBenchmarkResultsByDeviceClass(t *testing.T) {
	cache := &fakeCache{
		nodes: map[string]*corev1.Node{
			"a100": {
				ObjectMeta: metav1.ObjectMeta{
					Name: "a100",
					Labels: map[string]string{
						"flexinfer.ai/gpu.vendor": "NVIDIA",
						"flexinfer.ai/gpu.arch":   "sm_80",
						"flexinfer.ai/gpu.vram":   "40Gi",
						"flexinfer.ai/gpu.count":  "1",
						"flexinfer.ai/gpu.int4":   "true",
					},
				},
			},
			"h100": {
				ObjectMeta: metav1.ObjectMeta{
					Name: "h100",
					Labels: map[string]string{
						"flexinfer.ai/gpu.vendor": "NVIDIA",
						"flexinfer.ai/gpu.arch":   "sm_90",
						"flexinfer.ai/gpu.vram":   "80Gi",
						"flexinfer.ai/gpu.count":  "1",
						"flexinfer.ai/gpu.int4":   "true",
					},
				},
			},
		},
		configMaps: map[string]*corev1.ConfigMap{
			"default/" + defaultBenchmarkResultsConfigMap: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      defaultBenchmarkResultsConfigMap,
					Namespace: "default",
				},
				Data: map[string]string{},
			},
		},
	}

	model := "llama3:8b"
	backend := "ollama"
	cache.configMaps["default/"+defaultBenchmarkResultsConfigMap].Data[benchmarkKey(backend, model, deviceClassFromNode(cache.nodes["a100"]))] = "100"
	cache.configMaps["default/"+defaultBenchmarkResultsConfigMap].Data[benchmarkKey(backend, model, deviceClassFromNode(cache.nodes["h100"]))] = "300"

	sched := &Scheduler{
		cache:                     cache,
		benchmarkResultsConfigMap: defaultBenchmarkResultsConfigMap,
		tpsWeight:                 1.0,
		utilWeight:                0,
		costWeight:                0,
		cacheWeight:               0,
	}

	args := extenderv1.ExtenderArgs{
		Pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "p",
				Namespace: "default",
				Annotations: map[string]string{
					"flexinfer.ai/model":   model,
					"flexinfer.ai/backend": backend,
				},
			},
		},
		NodeNames: &[]string{"a100", "h100"},
	}

	body, _ := json.Marshal(args)
	req := httptest.NewRequest("POST", "/score", bytes.NewBuffer(body))
	rr := httptest.NewRecorder()
	sched.Score(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rr.Code)
	}

	var result []extenderv1.HostPriority
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}

	scoreA100 := getScore(result, "a100")
	scoreH100 := getScore(result, "h100")
	if scoreH100 <= scoreA100 {
		t.Fatalf("expected h100 score (%d) > a100 score (%d)", scoreH100, scoreA100)
	}
}

func TestFilter_FiltersByVRAMEstimateWhenFreeVRAMAvailable(t *testing.T) {
	cache := &fakeCache{
		nodes: map[string]*corev1.Node{
			"node-low": {
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-low",
					Labels: map[string]string{
						"flexinfer.ai/gpu.vendor": "NVIDIA",
					},
					Annotations: map[string]string{
						"flexinfer.ai/gpu-free-memory": "4000",
					},
				},
			},
			"node-high": {
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-high",
					Labels: map[string]string{
						"flexinfer.ai/gpu.vendor": "NVIDIA",
					},
					Annotations: map[string]string{
						"flexinfer.ai/gpu-free-memory": "8000",
					},
				},
			},
		},
		configMaps: map[string]*corev1.ConfigMap{},
	}

	sched := &Scheduler{cache: cache}

	args := extenderv1.ExtenderArgs{
		Pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "p",
				Namespace: "default",
				Annotations: map[string]string{
					"flexinfer.ai/gpu.vram-estimate-mb": "5000",
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "c",
						Resources: corev1.ResourceRequirements{
							Limits: corev1.ResourceList{
								"nvidia.com/gpu": resource.MustParse("1"),
							},
						},
					},
				},
			},
		},
		NodeNames: &[]string{"node-low", "node-high"},
	}

	body, _ := json.Marshal(args)
	req := httptest.NewRequest("POST", "/filter", bytes.NewBuffer(body))
	rr := httptest.NewRecorder()

	sched.Filter(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rr.Code)
	}

	var result extenderv1.ExtenderFilterResult
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if result.NodeNames == nil {
		t.Fatalf("expected NodeNames not nil")
	}

	// node-low should be filtered out; node-high should remain.
	if len(*result.NodeNames) != 1 || (*result.NodeNames)[0] != "node-high" {
		t.Fatalf("expected only node-high, got %v", *result.NodeNames)
	}
	if _, ok := result.FailedNodes["node-low"]; !ok {
		t.Fatalf("expected node-low to be in FailedNodes")
	}
}

func getScore(res []extenderv1.HostPriority, host string) int64 {
	for _, r := range res {
		if r.Host == host {
			return r.Score
		}
	}
	return -1
}
