package main

import "testing"

func TestNeo4jURICandidates(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		contains []string
	}{
		{
			name:     "empty uses default",
			in:       "",
			contains: []string{"bolt://localhost:7687"},
		},
		{
			name:     "host without scheme adds bolt",
			in:       "192.168.50.226:7687",
			contains: []string{"192.168.50.226:7687", "bolt://192.168.50.226:7687"},
		},
		{
			name:     "http admin endpoint adds bolt data endpoint",
			in:       "http://192.168.50.226:7474",
			contains: []string{"http://192.168.50.226:7474", "bolt://192.168.50.226:7687"},
		},
		{
			name:     "ws endpoint adds bolt data endpoint",
			in:       "ws://neo4j.local:7474",
			contains: []string{"ws://neo4j.local:7474", "bolt://neo4j.local:7687"},
		},
		{
			name:     "neo4j scheme on admin port adds 7687 variants",
			in:       "neo4j://neo4j.local:7474",
			contains: []string{"neo4j://neo4j.local:7474", "neo4j://neo4j.local:7687", "bolt://neo4j.local:7687"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := neo4jURICandidates(tt.in)
			if len(got) == 0 {
				t.Fatalf("expected at least one candidate")
			}
			for _, want := range tt.contains {
				if !contains(got, want) {
					t.Fatalf("expected candidates to contain %q, got %v", want, got)
				}
			}
		})
	}
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
