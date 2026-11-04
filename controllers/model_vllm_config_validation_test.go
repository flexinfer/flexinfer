package controllers

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
)

// A vLLM Model that sets a key the backend refuses (here a partial-prefill cap
// whose flag no vLLM release has) fails before anything renders, instead of
// launching a server that aborts in argument parsing.
func TestReconcile_VLLMRefusedConfigFailsBeforeRender(t *testing.T) {
	for _, tt := range []struct {
		name    string
		config  string
		wantMsg string
	}{
		{"dead partial-prefill flag", `{"maxNumPartialPrefills":2}`, "vLLM has no --max-num-partial-prefills flag"},
		{"unquoted vllmVersion", `{"vllmVersion":0.33}`, "spec.config.vllmVersion must be a quoted string"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			model := &aiv1alpha2.Model{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "vllm-refused-config",
					Namespace:  steadyStateNamespace,
					Generation: 1,
					Finalizers: []string{aiv1alpha2.ModelFinalizer},
				},
				Spec: aiv1alpha2.ModelSpec{
					Backend:      backend.NameVLLM,
					Source:       "HF://Qwen/Qwen3-0.6B",
					GPU:          &aiv1alpha2.GPUSpec{Vendor: aiv1alpha2.GPUVendorAMD},
					NodeSelector: map[string]string{"kubernetes.io/hostname": "cblevins-7900xtx"},
					Config:       &apiextensionsv1.JSON{Raw: []byte(tt.config)},
				},
				Status: aiv1alpha2.ModelStatus{Phase: aiv1alpha2.ModelPhasePending},
			}
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "cblevins-7900xtx",
					Labels: map[string]string{"kubernetes.io/hostname": "cblevins-7900xtx", "gpu.amd.com/gpu-architecture": "gfx1100"},
				},
				Status: corev1.NodeStatus{
					Capacity:    corev1.ResourceList{"amd.com/gpu": resource.MustParse("1")},
					Allocatable: corev1.ResourceList{"amd.com/gpu": resource.MustParse("1")},
					Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
				},
			}
			s := steadyStateScheme(t)
			cl := fake.NewClientBuilder().
				WithScheme(s).
				WithObjects(model, node).
				WithStatusSubresource(&aiv1alpha2.Model{}).
				Build()
			r := &ModelReconciler{Client: cl, Scheme: s, Recorder: record.NewFakeRecorder(100)}

			if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(model)}); err != nil {
				t.Fatalf("Reconcile() error = %v", err)
			}
			got := getModelFromClient(t, cl, model.Namespace, model.Name)
			if got.Status.Phase != aiv1alpha2.ModelPhaseFailed {
				t.Fatalf("phase = %q, want Failed", got.Status.Phase)
			}
			cond := findCondition(got.Status.Conditions, aiv1alpha2.ConditionModelSchedulable)
			if cond == nil || cond.Reason != aiv1alpha2.ReasonConfigInvalid || !strings.Contains(cond.Message, tt.wantMsg) {
				t.Fatalf("Schedulable condition = %+v, want ConfigInvalid containing %q", cond, tt.wantMsg)
			}
			var deps appsv1.DeploymentList
			if err := cl.List(context.Background(), &deps, client.InNamespace(model.Namespace)); err != nil {
				t.Fatalf("list deployments: %v", err)
			}
			if len(deps.Items) != 0 {
				t.Fatalf("rendered %d Deployments for a refused Model", len(deps.Items))
			}
		})
	}
}
