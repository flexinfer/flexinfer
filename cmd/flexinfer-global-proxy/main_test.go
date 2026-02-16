package main

import (
	"testing"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/internal/globalrouting"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestParseStrategy(t *testing.T) {
	tests := []struct {
		input   string
		want    globalrouting.Strategy
		wantErr bool
	}{
		{input: "", want: globalrouting.StrategyRoundRobin, wantErr: false},
		{input: "round-robin", want: globalrouting.StrategyRoundRobin, wantErr: false},
		{input: "roundrobin", want: globalrouting.StrategyRoundRobin, wantErr: false},
		{input: "failover", want: globalrouting.StrategyFailover, wantErr: false},
		{input: "latency", want: globalrouting.StrategyLatency, wantErr: false},
		{input: "weighted", want: globalrouting.StrategyWeighted, wantErr: false},
		{input: "bogus", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got, err := parseStrategy(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseStrategy(%q) error = nil, want non-nil", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseStrategy(%q) error = %v, want nil", tc.input, err)
			}
			if got != tc.want {
				t.Fatalf("parseStrategy(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestParseClusters(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{name: "valid two clusters", input: "us-west=https://west.example.com,us-east=http://east.example.com:8080", want: 2, wantErr: false},
		{name: "empty input", input: "", wantErr: true},
		{name: "missing equals", input: "us-west", wantErr: true},
		{name: "duplicate name", input: "us-west=https://west.example.com,us-west=https://west2.example.com", wantErr: true},
		{name: "invalid scheme", input: "us-west=grpc://west.example.com", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseClusters(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseClusters(%q) error = nil, want non-nil", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseClusters(%q) error = %v, want nil", tc.input, err)
			}
			if len(got) != tc.want {
				t.Fatalf("parseClusters(%q) len = %d, want %d", tc.input, len(got), tc.want)
			}
			for _, cluster := range got {
				if !cluster.Healthy {
					t.Fatalf("cluster %q healthy = false, want true", cluster.Name)
				}
			}
		})
	}
}

func TestParseWeights(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    map[string]int
		wantErr bool
	}{
		{
			name:  "valid weights",
			input: "cluster-a=3,cluster-b=1",
			want: map[string]int{
				"cluster-a": 3,
				"cluster-b": 1,
			},
		},
		{name: "empty input", input: "", want: map[string]int{}},
		{name: "invalid format", input: "cluster-a", wantErr: true},
		{name: "invalid value", input: "cluster-a=0", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseWeights(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseWeights(%q) error = nil, want non-nil", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseWeights(%q) error = %v, want nil", tc.input, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("len(parseWeights(%q)) = %d, want %d", tc.input, len(got), len(tc.want))
			}
			for key, value := range tc.want {
				if got[key] != value {
					t.Fatalf("weight[%q] = %d, want %d", key, got[key], value)
				}
			}
		})
	}
}

func TestApplyClusterWeights(t *testing.T) {
	clusters := []globalrouting.ClusterEndpoint{
		{Name: "cluster-a", URL: "https://a.example.com", Healthy: true, Weight: 1},
		{Name: "cluster-b", URL: "https://b.example.com", Healthy: true, Weight: 1},
	}
	weights := map[string]int{"cluster-a": 4}
	if err := applyClusterWeights(clusters, weights); err != nil {
		t.Fatalf("applyClusterWeights() error = %v", err)
	}
	if clusters[0].Weight != 4 || clusters[1].Weight != 1 {
		t.Fatalf("weights after apply = [%d,%d], want [4,1]", clusters[0].Weight, clusters[1].Weight)
	}

	bad := map[string]int{"cluster-c": 2}
	if err := applyClusterWeights(clusters, bad); err == nil {
		t.Fatalf("applyClusterWeights() error = nil, want non-nil for unknown cluster")
	}
}

func TestRuntimeConfigFromGlobalProxy(t *testing.T) {
	weightA := int32(3)
	gp := &aiv1alpha2.GlobalProxy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "global",
			Namespace: "flexinfer-system",
		},
		Spec: aiv1alpha2.GlobalProxySpec{
			Strategy: aiv1alpha2.GlobalRoutingStrategyWeighted,
			Clusters: []aiv1alpha2.GlobalProxyClusterEndpoint{
				{Name: "cluster-a", Endpoint: "https://a.example.com", Weight: &weightA},
				{Name: "cluster-b", Endpoint: "https://b.example.com"},
			},
			FailoverOrder: []string{"cluster-a", "cluster-b"},
		},
	}

	cfg, err := runtimeConfigFromGlobalProxy(gp)
	if err != nil {
		t.Fatalf("runtimeConfigFromGlobalProxy() error = %v", err)
	}
	if cfg.strategy != globalrouting.StrategyWeighted {
		t.Fatalf("strategy = %q, want %q", cfg.strategy, globalrouting.StrategyWeighted)
	}
	if len(cfg.clusters) != 2 {
		t.Fatalf("len(clusters) = %d, want 2", len(cfg.clusters))
	}
	if cfg.clusters[0].Weight != 3 {
		t.Fatalf("cluster-a weight = %d, want 3", cfg.clusters[0].Weight)
	}
	if cfg.clusters[1].Weight != 1 {
		t.Fatalf("cluster-b default weight = %d, want 1", cfg.clusters[1].Weight)
	}
}

func TestRuntimeConfigFromGlobalProxyValidation(t *testing.T) {
	gp := &aiv1alpha2.GlobalProxy{
		Spec: aiv1alpha2.GlobalProxySpec{
			Strategy: aiv1alpha2.GlobalRoutingStrategyRoundRobin,
			Clusters: []aiv1alpha2.GlobalProxyClusterEndpoint{
				{Name: "cluster-a", Endpoint: "grpc://a.example.com"},
			},
		},
	}

	if _, err := runtimeConfigFromGlobalProxy(gp); err == nil {
		t.Fatalf("runtimeConfigFromGlobalProxy() error = nil, want non-nil")
	}
}
