package main

import (
	"testing"

	"github.com/flexinfer/flexinfer/internal/globalrouting"
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
		{input: "weighted", wantErr: true},
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
