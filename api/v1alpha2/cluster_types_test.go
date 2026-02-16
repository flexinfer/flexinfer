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

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestClusterSpecNormalizedAPIEndpoint(t *testing.T) {
	spec := ClusterSpec{
		APIEndpoint: "  https://k8s-us-west.example.com:6443/  ",
	}

	got := spec.NormalizedAPIEndpoint()
	want := "https://k8s-us-west.example.com:6443"
	if got != want {
		t.Fatalf("NormalizedAPIEndpoint() = %q, want %q", got, want)
	}
}

func TestClusterSpecValidateBasic(t *testing.T) {
	tests := []struct {
		name    string
		spec    ClusterSpec
		wantErr bool
	}{
		{
			name: "valid spec",
			spec: ClusterSpec{
				APIEndpoint: "https://k8s-us-west.example.com:6443",
				SecretRef:   corev1.LocalObjectReference{Name: "cluster-us-west-kubeconfig"},
			},
			wantErr: false,
		},
		{
			name: "missing endpoint",
			spec: ClusterSpec{
				SecretRef: corev1.LocalObjectReference{Name: "cluster-us-west-kubeconfig"},
			},
			wantErr: true,
		},
		{
			name: "missing secret ref",
			spec: ClusterSpec{
				APIEndpoint: "https://k8s-us-west.example.com:6443",
			},
			wantErr: true,
		},
		{
			name: "non-https endpoint rejected",
			spec: ClusterSpec{
				APIEndpoint: "http://k8s-us-west.example.com:6443",
				SecretRef:   corev1.LocalObjectReference{Name: "cluster-us-west-kubeconfig"},
			},
			wantErr: true,
		},
		{
			name: "missing host rejected",
			spec: ClusterSpec{
				APIEndpoint: "https://",
				SecretRef:   corev1.LocalObjectReference{Name: "cluster-us-west-kubeconfig"},
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
