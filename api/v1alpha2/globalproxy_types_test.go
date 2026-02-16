package v1alpha2

import "testing"

func TestGlobalProxySpecValidateBasic(t *testing.T) {
	valid := GlobalProxySpec{
		ExternalEndpoint: "global.flexinfer.example.com",
		Strategy:         GlobalRoutingStrategyFailover,
		Clusters: []GlobalProxyClusterEndpoint{
			{Name: "cluster-a", Endpoint: "https://cluster-a.example.com"},
			{Name: "cluster-b", Endpoint: "http://cluster-b.example.com:8080"},
		},
		FailoverOrder: []string{"cluster-a", "cluster-b"},
	}

	tests := []struct {
		name    string
		spec    GlobalProxySpec
		wantErr bool
	}{
		{name: "valid failover spec", spec: valid, wantErr: false},
		{
			name: "valid round robin without failover order",
			spec: GlobalProxySpec{
				ExternalEndpoint: "global.flexinfer.example.com",
				Clusters: []GlobalProxyClusterEndpoint{{
					Name:     "cluster-a",
					Endpoint: "https://cluster-a.example.com",
				}},
			},
			wantErr: false,
		},
		{
			name: "valid latency strategy",
			spec: GlobalProxySpec{
				ExternalEndpoint: "global.flexinfer.example.com",
				Strategy:         GlobalRoutingStrategyLatency,
				Clusters: []GlobalProxyClusterEndpoint{
					{Name: "cluster-a", Endpoint: "https://cluster-a.example.com"},
					{Name: "cluster-b", Endpoint: "https://cluster-b.example.com"},
				},
			},
			wantErr: false,
		},
		{
			name: "missing external endpoint",
			spec: GlobalProxySpec{
				Clusters: []GlobalProxyClusterEndpoint{{
					Name:     "cluster-a",
					Endpoint: "https://cluster-a.example.com",
				}},
			},
			wantErr: true,
		},
		{
			name: "external endpoint must be hostname",
			spec: GlobalProxySpec{
				ExternalEndpoint: "https://global.flexinfer.example.com",
				Clusters: []GlobalProxyClusterEndpoint{{
					Name:     "cluster-a",
					Endpoint: "https://cluster-a.example.com",
				}},
			},
			wantErr: true,
		},
		{
			name: "duplicate cluster names",
			spec: GlobalProxySpec{
				ExternalEndpoint: "global.flexinfer.example.com",
				Clusters: []GlobalProxyClusterEndpoint{
					{Name: "cluster-a", Endpoint: "https://cluster-a.example.com"},
					{Name: "cluster-a", Endpoint: "https://cluster-b.example.com"},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid cluster endpoint scheme",
			spec: GlobalProxySpec{
				ExternalEndpoint: "global.flexinfer.example.com",
				Clusters: []GlobalProxyClusterEndpoint{{
					Name:     "cluster-a",
					Endpoint: "grpc://cluster-a.example.com",
				}},
			},
			wantErr: true,
		},
		{
			name: "failover strategy requires order",
			spec: GlobalProxySpec{
				ExternalEndpoint: "global.flexinfer.example.com",
				Strategy:         GlobalRoutingStrategyFailover,
				Clusters: []GlobalProxyClusterEndpoint{{
					Name:     "cluster-a",
					Endpoint: "https://cluster-a.example.com",
				}},
			},
			wantErr: true,
		},
		{
			name: "failover order references unknown cluster",
			spec: GlobalProxySpec{
				ExternalEndpoint: "global.flexinfer.example.com",
				Strategy:         GlobalRoutingStrategyFailover,
				Clusters: []GlobalProxyClusterEndpoint{{
					Name:     "cluster-a",
					Endpoint: "https://cluster-a.example.com",
				}},
				FailoverOrder: []string{"cluster-b"},
			},
			wantErr: true,
		},
		{
			name: "latency strategy rejects failover order",
			spec: GlobalProxySpec{
				ExternalEndpoint: "global.flexinfer.example.com",
				Strategy:         GlobalRoutingStrategyLatency,
				Clusters: []GlobalProxyClusterEndpoint{
					{Name: "cluster-a", Endpoint: "https://cluster-a.example.com"},
				},
				FailoverOrder: []string{"cluster-a"},
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.spec.ValidateBasic()
			if tc.wantErr && err == nil {
				t.Fatalf("ValidateBasic() error = nil, want non-nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ValidateBasic() error = %v, want nil", err)
			}
		})
	}
}
