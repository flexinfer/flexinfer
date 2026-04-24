package controllers

import (
	"fmt"
	"strings"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

const (
	promotionGateQuantizedArtifactV1 = "quantized-artifact-v1"
)

type promotionGateDecision struct {
	Required bool
	Allowed  bool
	Reason   string
	Message  string
}

func applyPromotionGate(model *aiv1alpha2.Model, desiredReplicas int32) int32 {
	decision := evaluatePromotionGate(model)
	if !decision.Required {
		return desiredReplicas
	}
	setModelCondition(model, aiv1alpha2.ConditionPromotionGate, decision.Allowed, decision.Reason, decision.Message)
	if !decision.Allowed {
		return 0
	}
	return desiredReplicas
}

func evaluatePromotionGate(model *aiv1alpha2.Model) promotionGateDecision {
	if model == nil || !hasQuantizedArtifactPromotionGate(model) {
		return promotionGateDecision{
			Required: false,
			Allowed:  true,
			Reason:   aiv1alpha2.ReasonPromotionGateNotRequired,
			Message:  "no quantized artifact promotion gate configured",
		}
	}

	if !requestsPrimaryPromotion(model) {
		return promotionGateDecision{
			Required: true,
			Allowed:  true,
			Reason:   aiv1alpha2.ReasonPromotionGateCanary,
			Message:  "quantized artifact gate allows scale-to-zero or canary operation",
		}
	}

	if hasPromotionValidationEvidence(model) {
		return promotionGateDecision{
			Required: true,
			Allowed:  true,
			Reason:   aiv1alpha2.ReasonPromotionGateValidated,
			Message:  "quantized artifact validation evidence permits primary promotion",
		}
	}

	return promotionGateDecision{
		Required: true,
		Allowed:  false,
		Reason:   aiv1alpha2.ReasonPromotionGateBlocked,
		Message: fmt.Sprintf(
			"primary promotion requires %s=passed or %s evidence",
			AnnotationPromotionValidation,
			AnnotationPromotionEvidence,
		),
	}
}

func hasQuantizedArtifactPromotionGate(model *aiv1alpha2.Model) bool {
	return strings.EqualFold(strings.TrimSpace(model.GetAnnotations()[AnnotationPromotionGate]), promotionGateQuantizedArtifactV1)
}

func requestsPrimaryPromotion(model *aiv1alpha2.Model) bool {
	if model.Spec.GetMinReplicas() > 0 {
		return true
	}
	return isWarmPrimaryModel(model)
}

func hasPromotionValidationEvidence(model *aiv1alpha2.Model) bool {
	annotations := model.GetAnnotations()
	validation := strings.ToLower(strings.TrimSpace(annotations[AnnotationPromotionValidation]))
	switch validation {
	case "passed", "pass", "success", "succeeded", "valid", "validated":
		return true
	}

	evidence := strings.TrimSpace(annotations[AnnotationPromotionEvidence])
	if evidence != "" && validation != "failed" && validation != "failure" && validation != "invalid" {
		return true
	}

	// Backward compatible with existing manifests that recorded validation in
	// the promotion-state annotation before the controller enforced the gate.
	state := strings.ToLower(strings.TrimSpace(annotations[AnnotationPromotionState]))
	return strings.Contains(state, "validated")
}
