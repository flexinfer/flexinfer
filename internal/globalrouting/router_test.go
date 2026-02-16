package globalrouting

import "testing"

func TestRouterRoundRobinSelectsHealthyClusters(t *testing.T) {
	registry := NewRegistry([]ClusterEndpoint{
		{Name: "cluster-a", URL: "https://a.example.com", Healthy: true},
		{Name: "cluster-b", URL: "https://b.example.com", Healthy: false},
		{Name: "cluster-c", URL: "https://c.example.com", Healthy: true},
	}, nil)
	router := NewRouter(registry)

	first, err := router.Select(StrategyRoundRobin)
	if err != nil {
		t.Fatalf("Select() first = %v", err)
	}
	second, err := router.Select(StrategyRoundRobin)
	if err != nil {
		t.Fatalf("Select() second = %v", err)
	}
	third, err := router.Select(StrategyRoundRobin)
	if err != nil {
		t.Fatalf("Select() third = %v", err)
	}

	if first.Name == "cluster-b" || second.Name == "cluster-b" || third.Name == "cluster-b" {
		t.Fatalf("round robin selected unhealthy cluster")
	}
	if first.Name != "cluster-a" {
		t.Fatalf("first selected cluster = %q, want cluster-a", first.Name)
	}
	if second.Name != "cluster-c" {
		t.Fatalf("second selected cluster = %q, want cluster-c", second.Name)
	}
	if third.Name != "cluster-a" {
		t.Fatalf("third selected cluster = %q, want cluster-a", third.Name)
	}
}

func TestRouterFailoverPrefersFailoverOrder(t *testing.T) {
	registry := NewRegistry([]ClusterEndpoint{
		{Name: "cluster-a", URL: "https://a.example.com", Healthy: false},
		{Name: "cluster-b", URL: "https://b.example.com", Healthy: true},
		{Name: "cluster-c", URL: "https://c.example.com", Healthy: true},
	}, []string{"cluster-a", "cluster-c", "cluster-b"})
	router := NewRouter(registry)

	selected, err := router.Select(StrategyFailover)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if selected.Name != "cluster-c" {
		t.Fatalf("selected cluster = %q, want cluster-c", selected.Name)
	}
}

func TestRouterFailoverFallsBackToHealthyWhenOrderMissing(t *testing.T) {
	registry := NewRegistry([]ClusterEndpoint{
		{Name: "cluster-a", URL: "https://a.example.com", Healthy: false},
		{Name: "cluster-b", URL: "https://b.example.com", Healthy: true},
	}, nil)
	router := NewRouter(registry)

	selected, err := router.Select(StrategyFailover)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if selected.Name != "cluster-b" {
		t.Fatalf("selected cluster = %q, want cluster-b", selected.Name)
	}
}

func TestRouterNoHealthyClusters(t *testing.T) {
	registry := NewRegistry([]ClusterEndpoint{
		{Name: "cluster-a", URL: "https://a.example.com", Healthy: false},
	}, nil)
	router := NewRouter(registry)

	_, err := router.Select(StrategyRoundRobin)
	if err == nil {
		t.Fatalf("Select() error = nil, want non-nil")
	}
	if err != ErrNoHealthyClusters {
		t.Fatalf("Select() error = %v, want %v", err, ErrNoHealthyClusters)
	}
}

func TestRouterLatencySelectsLowestObservedLatency(t *testing.T) {
	registry := NewRegistry([]ClusterEndpoint{
		{Name: "cluster-a", URL: "https://a.example.com", Healthy: true},
		{Name: "cluster-b", URL: "https://b.example.com", Healthy: true},
		{Name: "cluster-c", URL: "https://c.example.com", Healthy: true},
	}, nil)
	registry.SetLatency("cluster-a", 90)
	registry.SetLatency("cluster-b", 20)
	registry.SetLatency("cluster-c", 45)

	router := NewRouter(registry)
	selected, err := router.Select(StrategyLatency)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if selected.Name != "cluster-b" {
		t.Fatalf("selected cluster = %q, want cluster-b", selected.Name)
	}
}

func TestRouterLatencyFallsBackWhenNoProbeData(t *testing.T) {
	registry := NewRegistry([]ClusterEndpoint{
		{Name: "cluster-a", URL: "https://a.example.com", Healthy: true},
		{Name: "cluster-b", URL: "https://b.example.com", Healthy: true},
	}, nil)

	router := NewRouter(registry)
	selected, err := router.Select(StrategyLatency)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if selected.Name != "cluster-a" {
		t.Fatalf("selected cluster = %q, want cluster-a", selected.Name)
	}
}
