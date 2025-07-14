package scheduler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"
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
						"flexinfer.ai/gpu.util": "50",
						"flexinfer.ai/cost":     "5",
					},
				},
			},
			"node2": {
				ObjectMeta: metav1.ObjectMeta{
					Name: "node2",
					Annotations: map[string]string{
						"flexinfer.ai/gpu.util": "10",
						"flexinfer.ai/cost":     "2",
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

	sched := &Scheduler{cache: cache, tpsWeight: 0.7, utilWeight: 0.2, costWeight: 0.1}

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

	if result[0].Host == result[1].Host {
		t.Fatalf("hosts should differ")
	}
}
