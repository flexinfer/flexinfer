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

package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
)

// liveLaneGoldenEnv regenerates the golden pod templates instead of comparing.
const liveLaneGoldenEnv = "FLEXINFER_UPDATE_LIVE_LANE_GOLDEN"

// liveLaneFixtures are frozen, comment-stripped copies of serving lanes from
// deploy/models at master 4fd5efa0 (2026-10-30), with the GPU arch each
// lane's node reports. The fixtures do not follow later manifest edits on
// purpose: they pin the controller's render, not the lanes.
var liveLaneFixtures = []struct {
	name    string
	gpuArch string
}{
	{"qwen38-27b-xtx-warm-canary", "gfx1100"},     // vLLM, explicit per-lane compile-cache hostPath
	{"qwen38-27b-autoround-workhorse", "gfx1100"}, // vLLM, segmented attention, S4 compile-cache hostPath
	{"qwen35-9b-ablit-rp", "gfx1100"},             // vLLM, compile cache on the default hostPath
	{"gemma4-26b-a4b-gptq-5930k", "gfx1100"},      // vLLM, compile cache auto-enabled by gpu.shared
	{"qwen-image-21-gfx1100", "gfx1100"},          // diffusers, explicit per-lane compile-cache hostPath
	{"wan21-t2v-1p3b-gfx1100", "gfx1100"},         // diffusers, compile cache on the default hostPath
	{"bge-radeonvii", "gfx906"},                   // llama.cpp, no compile-cache support
}

// TestLiveLanePodTemplatesUnchanged renders each frozen lane through
// ensureDeployment and compares the pod template with the golden JSON in
// testdata/live-lanes. A changed pod template restarts the lane's GPU pod on
// the next controller roll and boots it cold, so a mismatch here is a live
// impact, not a cosmetic diff. The goldens were rendered by master 4fd5efa0,
// before spec.cache.compilationCache.scope existed: they prove the default
// scope renders byte-identical pods.
//
// Regenerate only for an intentional, live-impacting render change, and say
// so in the MR:
//
//	FLEXINFER_UPDATE_LIVE_LANE_GOLDEN=1 go test ./controllers -run TestLiveLanePodTemplatesUnchanged
func TestLiveLanePodTemplatesUnchanged(t *testing.T) {
	// Render inputs the controller reads from its own environment.
	for _, name := range []string{
		"DEFAULT_FLASH_LOADER_ENABLED",
		"DEFAULT_FLASH_LOADER_CONCURRENCY",
		"DEFAULT_FLASH_LOADER_BUFFER_KB",
		"DEFAULT_FLASH_LOADER_EXCLUDE",
		"DEFAULT_FLASH_LOADER_TMPFS_SIZE_LIMIT",
		"DEFAULT_FLASH_LOADER_VERIFY",
		"DEFAULT_SHM_SIZE_LIMIT",
	} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unset %s: %v", name, err)
		}
	}
	update := os.Getenv(liveLaneGoldenEnv) == "1"

	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("add kubernetes scheme: %v", err)
	}
	if err := aiv1alpha2.AddToScheme(s); err != nil {
		t.Fatalf("add flexinfer scheme: %v", err)
	}

	for _, fx := range liveLaneFixtures {
		t.Run(fx.name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", "live-lanes", fx.name+".yaml"))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			model := &aiv1alpha2.Model{}
			if err := yaml.UnmarshalStrict(raw, model); err != nil {
				t.Fatalf("decode fixture: %v", err)
			}
			b, ok := backend.Get(model.Spec.Backend)
			if !ok {
				t.Fatalf("backend %q not registered", model.Spec.Backend)
			}

			c := fake.NewClientBuilder().WithScheme(s).Build()
			r := &ModelReconciler{Client: c, Scheme: s}
			ctx := context.Background()
			if err := r.ensureDeployment(ctx, model, b, backend.GPUVendorAMD, fx.gpuArch, 1); err != nil {
				t.Fatalf("ensureDeployment: %v", err)
			}
			dep := &appsv1.Deployment{}
			if err := c.Get(ctx, types.NamespacedName{Namespace: model.Namespace, Name: model.Name}, dep); err != nil {
				t.Fatalf("get rendered deployment: %v", err)
			}
			got, err := json.MarshalIndent(dep.Spec.Template, "", "  ")
			if err != nil {
				t.Fatalf("marshal pod template: %v", err)
			}
			got = append(got, '\n')

			golden := filepath.Join("testdata", "live-lanes", fx.name+".podtemplate.golden.json")
			if update {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden (regenerate with %s=1): %v", liveLaneGoldenEnv, err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("pod template of %s changed: a controller roll would restart this lane.\n--- got\n%s\n--- want (%s)\n%s",
					fx.name, got, golden, want)
			}
		})
	}
}
