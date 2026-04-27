package controllers

import (
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

func TestApplyPromotionGate(t *testing.T) {
	minOne := int32(1)
	minZero := int32(0)

	tests := []struct {
		name         string
		model        *aiv1alpha2.Model
		desired      int32
		wantReplicas int32
		wantReason   string
		wantAllowed  bool
	}{
		{
			name: "validated primary is allowed",
			model: gatedModel(map[string]string{
				AnnotationPromotionValidation: "passed",
				AnnotationPromotionEvidence:   "probe://gemma4-26b/8k/2026-04-17",
			}, &minOne, "primary"),
			desired:      1,
			wantReplicas: 1,
			wantReason:   aiv1alpha2.ReasonPromotionGateValidated,
			wantAllowed:  true,
		},
		{
			name: "existing validated state is accepted as evidence",
			model: gatedModel(map[string]string{
				AnnotationPromotionState: "baseline-8k-validated",
			}, &minOne, "primary"),
			desired:      1,
			wantReplicas: 1,
			wantReason:   aiv1alpha2.ReasonPromotionGateValidated,
			wantAllowed:  true,
		},
		{
			name: "primary without evidence is blocked",
			model: gatedModel(map[string]string{
				AnnotationPromotionState: "candidate",
			}, &minOne, "primary"),
			desired:      1,
			wantReplicas: 0,
			wantReason:   aiv1alpha2.ReasonPromotionGateBlocked,
			wantAllowed:  false,
		},
		{
			name: "scale to zero canary is allowed without evidence",
			model: gatedModel(map[string]string{
				AnnotationPromotionState: "canary-reference-only",
			}, &minZero, "ondemand"),
			desired:      0,
			wantReplicas: 0,
			wantReason:   aiv1alpha2.ReasonPromotionGateCanary,
			wantAllowed:  true,
		},
		{
			name: "ondemand corrupted gemma4-style artifact stays allowed but not primary",
			model: gatedModel(map[string]string{
				AnnotationPromotionState: "conservative-on-demand-baseline",
			}, &minZero, "ondemand"),
			desired:      1,
			wantReplicas: 1,
			wantReason:   aiv1alpha2.ReasonPromotionGateCanary,
			wantAllowed:  true,
		},
		{
			name: "corrupted artifact cannot become warm primary without evidence",
			model: gatedModel(map[string]string{
				AnnotationPromotionState: "conservative-on-demand-baseline",
			}, &minOne, "primary"),
			desired:      1,
			wantReplicas: 0,
			wantReason:   aiv1alpha2.ReasonPromotionGateBlocked,
			wantAllowed:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := applyPromotionGate(tt.model, tt.desired)
			if got != tt.wantReplicas {
				t.Fatalf("applyPromotionGate() = %d, want %d", got, tt.wantReplicas)
			}
			cond := modelCondition(tt.model.Status.Conditions, aiv1alpha2.ConditionPromotionGate)
			if cond == nil {
				t.Fatal("PromotionGate condition was not set")
			}
			if cond.Reason != tt.wantReason {
				t.Fatalf("condition reason = %q, want %q", cond.Reason, tt.wantReason)
			}
			wantStatus := metav1.ConditionFalse
			if tt.wantAllowed {
				wantStatus = metav1.ConditionTrue
			}
			if cond.Status != wantStatus {
				t.Fatalf("condition status = %s, want %s", cond.Status, wantStatus)
			}
		})
	}
}

func TestApplyPromotionGateUngatedPreservesDesiredReplicas(t *testing.T) {
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen", Namespace: "flexinfer-system"},
		Spec:       aiv1alpha2.ModelSpec{Backend: "vllm"},
	}

	if got := applyPromotionGate(model, 1); got != 1 {
		t.Fatalf("applyPromotionGate() = %d, want 1", got)
	}
	if cond := modelCondition(model.Status.Conditions, aiv1alpha2.ConditionPromotionGate); cond != nil {
		t.Fatalf("ungated model should not get PromotionGate condition: %+v", cond)
	}
}

func gatedModel(extraAnnotations map[string]string, minReplicas *int32, warmPolicy string) *aiv1alpha2.Model {
	annotations := map[string]string{
		AnnotationPromotionGate: promotionGateQuantizedArtifactV1,
	}
	for k, v := range extraAnnotations {
		annotations[k] = v
	}

	return &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "gemma4-quantized",
			Namespace:   "flexinfer-system",
			Annotations: annotations,
			Generation:  7,
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Serverless: &aiv1alpha2.ServerlessSpec{
				Enabled:     boolRef(true),
				MinReplicas: minReplicas,
			},
			Config: &apiextensionsv1.JSON{Raw: []byte(`{"warmPolicy":"` + warmPolicy + `"}`)},
		},
	}
}
