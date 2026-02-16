/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha2

import "testing"

func TestFederatedModelSpecValidateBasic(t *testing.T) {
	replicas := int32(2)

	tests := []struct {
		name    string
		spec    FederatedModelSpec
		wantErr bool
	}{
		{
			name: "valid explicit clusters",
			spec: FederatedModelSpec{
				Template: ModelSpec{Backend: "vllm", Source: "HF://org/model"},
				Placement: FederatedModelPlacement{
					Clusters:           []string{"us-west", "us-east"},
					ReplicasPerCluster: &replicas,
				},
			},
			wantErr: false,
		},
		{
			name: "missing placement target",
			spec: FederatedModelSpec{
				Template:  ModelSpec{Backend: "vllm", Source: "HF://org/model"},
				Placement: FederatedModelPlacement{},
			},
			wantErr: true,
		},
		{
			name: "weighted strategy missing weights",
			spec: FederatedModelSpec{
				Template: ModelSpec{Backend: "vllm", Source: "HF://org/model"},
				Placement: FederatedModelPlacement{
					Clusters: []string{"us-west"},
				},
				Routing: &FederatedModelRouting{
					Strategy: RoutingStrategyWeighted,
				},
			},
			wantErr: true,
		},
		{
			name: "failover strategy missing order",
			spec: FederatedModelSpec{
				Template: ModelSpec{Backend: "vllm", Source: "HF://org/model"},
				Placement: FederatedModelPlacement{
					Clusters: []string{"us-west"},
				},
				Routing: &FederatedModelRouting{
					Strategy: RoutingStrategyFailover,
				},
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := tc.spec.ValidateBasic()
			if tc.wantErr && err == nil {
				t.Fatal("ValidateBasic() error = nil, want error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ValidateBasic() unexpected error: %v", err)
			}
		})
	}
}
