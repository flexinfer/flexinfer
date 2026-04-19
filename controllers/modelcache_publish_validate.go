/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

// Pre-publish artifact validator gate for the ModelCache publish phase.
//
// When spec.publish.validate.enabled=true the controller runs the offline
// validator (build/scripts/validate_quantized_artifact.py, baked into the
// runtime image) as a one-shot Job before creating the publish job. The
// publish job is only created when the validator returns ok=true.
//
// Status: results land on modelCache.Status.Publish.Validate. The publish
// status is reused so the existing failure-handling and event paths just
// work; the publish phase fails with FailureMessage = "<validator errors>".

package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	"github.com/flexinfer/flexinfer/pkg/quantization"
)

// validatorJobMetadata is the JSON shape emitted by
// build/scripts/validate_quantized_artifact.py via --json.
type validatorJobMetadata struct {
	Ok       bool     `json:"ok"`
	Layout   string   `json:"layout,omitempty"`
	Family   string   `json:"family,omitempty"`
	Errors   []string `json:"errors,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// reconcilePublishValidate runs the pre-publish validator gate. The bool
// return signals whether reconcilePublish should continue to the publish
// job creation step (true) or short-circuit (false, e.g. waiting for the
// validator job or already-failed).
func (r *ModelCacheReconciler) reconcilePublishValidate(
	ctx context.Context,
	modelCache *aiv1alpha1.ModelCache,
	pvcName, modelPath string,
) (bool, ctrl.Result, error) {
	log := log.FromContext(ctx)

	spec := modelCache.Spec.Publish
	if spec == nil || spec.Validate == nil || !spec.Validate.Enabled {
		return true, ctrl.Result{}, nil
	}

	// Already validated successfully on a prior reconcile — proceed to publish.
	if modelCache.Status.Publish != nil &&
		modelCache.Status.Publish.Validate != nil &&
		modelCache.Status.Publish.Validate.Ok {
		return true, ctrl.Result{}, nil
	}

	// Already validated and failed (terminal). Don't create a publish job.
	// The publish-job-failed path will surface this to the operator on next
	// reconcile.
	if modelCache.Status.Publish != nil &&
		modelCache.Status.Publish.Validate != nil &&
		!modelCache.Status.Publish.Validate.Ok &&
		strings.HasPrefix(modelCache.Status.Publish.FailureMessage, "validator:") {
		return false, ctrl.Result{}, nil
	}

	jobName := modelCache.Name + quantization.ValidatorJobSuffix
	job := &batchv1.Job{}
	err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: modelCache.Namespace}, job)
	if err != nil && errors.IsNotFound(err) {
		return r.createValidatorJob(ctx, modelCache, pvcName, modelPath, jobName)
	} else if err != nil {
		return false, ctrl.Result{}, err
	}

	if job.Status.Succeeded > 0 {
		return r.handleValidatorJobSucceeded(ctx, modelCache, job)
	}
	if job.Status.Failed > 0 {
		return r.handleValidatorJobFailed(ctx, modelCache, job)
	}

	// Still running.
	log.V(1).Info("Validator job in progress", "cache", modelCache.Name)
	return false, ctrl.Result{RequeueAfter: requeueShort}, nil
}

func (r *ModelCacheReconciler) createValidatorJob(
	ctx context.Context,
	modelCache *aiv1alpha1.ModelCache,
	pvcName, modelPath, jobName string,
) (bool, ctrl.Result, error) {
	log := log.FromContext(ctx)

	tolerations := []corev1.Toleration{
		{
			Key:      "dedicated",
			Operator: corev1.TolerationOpEqual,
			Value:    "gpu",
			Effect:   corev1.TaintEffectNoSchedule,
		},
	}
	params := quantization.JobParams{
		Name:         modelCache.Name,
		Namespace:    modelCache.Namespace,
		PVCName:      pvcName,
		ModelPath:    modelPath,
		NodeSelector: modelCache.Spec.NodeSelector,
		Tolerations:  tolerations,
	}

	job, buildErr := quantization.BuildValidateArtifactJob(params, modelCache.Spec.Publish.Validate)
	if buildErr != nil {
		r.Recorder.Event(modelCache, corev1.EventTypeWarning, "PublishValidateFailed",
			fmt.Sprintf("Failed to build validator job: %s", buildErr))
		modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseFailed
		modelCache.Status.CurrentPhase = "publish-validate"
		if statusErr := r.Status().Update(ctx, modelCache); statusErr != nil {
			return false, ctrl.Result{}, statusErr
		}
		return false, ctrl.Result{}, nil
	}

	if err := ctrl.SetControllerReference(modelCache, job, r.Scheme); err != nil {
		return false, ctrl.Result{}, err
	}

	log.Info("Creating publish-validator job", "Job", job.Name)
	if err := r.Create(ctx, job); err != nil {
		return false, ctrl.Result{}, err
	}

	// Surface the validate substep through the publish phase so existing
	// status-monitoring tooling sees it without a CRD enum change.
	modelCache.Status.Phase = aiv1alpha1.ModelCachePhasePublishing
	modelCache.Status.CurrentPhase = "publish-validate"
	if err := r.Status().Update(ctx, modelCache); err != nil {
		return false, ctrl.Result{}, err
	}
	r.Recorder.Event(modelCache, corev1.EventTypeNormal, "PublishValidateStarted",
		fmt.Sprintf("Validator job created: %s", job.Name))

	return false, ctrl.Result{RequeueAfter: requeueLong}, nil
}

func (r *ModelCacheReconciler) handleValidatorJobSucceeded(
	ctx context.Context,
	modelCache *aiv1alpha1.ModelCache,
	job *batchv1.Job,
) (bool, ctrl.Result, error) {
	log := log.FromContext(ctx)

	meta := r.readValidatorMetadataFromPods(ctx, modelCache.Namespace, job.Name)

	completedAt := metav1.Now()
	if job.Status.CompletionTime != nil {
		completedAt = *job.Status.CompletionTime
	}

	if modelCache.Status.Publish == nil {
		modelCache.Status.Publish = &aiv1alpha1.PublishStatus{}
	}
	if modelCache.Status.Publish.Validate == nil {
		modelCache.Status.Publish.Validate = &aiv1alpha1.PublishValidateStatus{}
	}
	vstat := modelCache.Status.Publish.Validate
	vstat.ValidatedAt = &completedAt

	if meta == nil {
		// Job exit was 0 but we have no parsed JSON — treat as failure so
		// the operator gets a clear signal rather than a silent pass.
		vstat.Ok = false
		vstat.Errors = []string{"validator job succeeded but JSON metadata was missing from termination log"}
		modelCache.Status.Publish.FailureMessage = "validator: missing metadata"
		modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseFailed
		modelCache.Status.CurrentPhase = "publish-validate"
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return false, ctrl.Result{}, err
		}
		r.Recorder.Event(modelCache, corev1.EventTypeWarning, "PublishValidateFailed",
			"validator returned no parseable JSON metadata")
		return false, ctrl.Result{}, nil
	}

	vstat.Ok = meta.Ok
	vstat.Layout = meta.Layout
	vstat.Family = meta.Family
	vstat.Errors = append([]string(nil), meta.Errors...)
	vstat.Warnings = append([]string(nil), meta.Warnings...)

	failOnWarnings := modelCache.Spec.Publish.Validate.FailOnWarnings != nil &&
		*modelCache.Spec.Publish.Validate.FailOnWarnings
	hasBlockingWarnings := failOnWarnings && len(vstat.Warnings) > 0

	if !meta.Ok || hasBlockingWarnings {
		var blockers []string
		blockers = append(blockers, vstat.Errors...)
		if hasBlockingWarnings {
			for _, w := range vstat.Warnings {
				blockers = append(blockers, "warning: "+w)
			}
		}
		joined := strings.Join(blockers, "; ")
		modelCache.Status.Publish.FailureMessage = "validator: " + joined
		modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseFailed
		modelCache.Status.CurrentPhase = "publish-validate"
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return false, ctrl.Result{}, err
		}
		log.Info("Publish validator gated publish",
			"cache", modelCache.Name,
			"layout", vstat.Layout,
			"family", vstat.Family,
			"errors", len(vstat.Errors),
			"warnings", len(vstat.Warnings))
		r.Recorder.Event(modelCache, corev1.EventTypeWarning, "PublishValidateFailed",
			truncateString(joined, 256))
		return false, ctrl.Result{}, nil
	}

	// Pass: persist status and let the publish job creation proceed on the
	// next reconcile pass.
	modelCache.Status.CurrentPhase = "publish-validate"
	if err := r.Status().Update(ctx, modelCache); err != nil {
		return false, ctrl.Result{}, err
	}
	log.Info("Publish validator passed",
		"cache", modelCache.Name,
		"layout", vstat.Layout,
		"family", vstat.Family,
		"warnings", len(vstat.Warnings))
	r.Recorder.Event(modelCache, corev1.EventTypeNormal, "PublishValidatePassed",
		fmt.Sprintf("validator ok (layout=%s family=%s warnings=%d)",
			vstat.Layout, vstat.Family, len(vstat.Warnings)))
	return true, ctrl.Result{}, nil
}

func (r *ModelCacheReconciler) handleValidatorJobFailed(
	ctx context.Context,
	modelCache *aiv1alpha1.ModelCache,
	job *batchv1.Job,
) (bool, ctrl.Result, error) {
	failureMsg := captureContainerFailureLogs(ctx, r.Client, modelCache.Namespace, job.Name, "validator")
	if strings.TrimSpace(failureMsg) == "" {
		failureMsg = "validator job failed (no termination message)"
	}

	if modelCache.Status.Publish == nil {
		modelCache.Status.Publish = &aiv1alpha1.PublishStatus{}
	}
	if modelCache.Status.Publish.Validate == nil {
		modelCache.Status.Publish.Validate = &aiv1alpha1.PublishValidateStatus{}
	}
	modelCache.Status.Publish.Validate.Ok = false
	modelCache.Status.Publish.Validate.Errors = []string{truncateString(failureMsg, 1024)}
	modelCache.Status.Publish.FailureMessage = "validator: " + truncateString(failureMsg, 256)
	modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseFailed
	modelCache.Status.CurrentPhase = "publish-validate"
	if err := r.Status().Update(ctx, modelCache); err != nil {
		return false, ctrl.Result{}, err
	}
	r.Recorder.Event(modelCache, corev1.EventTypeWarning, "PublishValidateFailed",
		truncateString(failureMsg, 256))
	return false, ctrl.Result{}, nil
}

// readValidatorMetadataFromPods reads the validator's JSON output from the
// "validator" container's termination log of the validate job's pods.
func (r *ModelCacheReconciler) readValidatorMetadataFromPods(ctx context.Context, namespace, jobName string) *validatorJobMetadata {
	podList := &corev1.PodList{}
	if err := r.List(ctx, podList,
		client.InNamespace(namespace),
		client.MatchingLabels{"job-name": jobName},
	); err != nil {
		return nil
	}
	for _, pod := range podList.Items {
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Name != "validator" {
				continue
			}
			terminated := cs.State.Terminated
			if terminated == nil {
				terminated = cs.LastTerminationState.Terminated
			}
			if terminated == nil {
				continue
			}
			msg := strings.TrimSpace(terminated.Message)
			if msg == "" {
				continue
			}
			meta, err := parseValidatorJSON(msg)
			if err == nil {
				return meta
			}
		}
	}
	return nil
}

// parseValidatorJSON tolerates leading/trailing log lines around the JSON
// document (the wrapper script tees stdout into the termination log so
// shell `set -eu` traces or python warnings can sneak in).
func parseValidatorJSON(s string) (*validatorJobMetadata, error) {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no JSON object in validator output")
	}
	var m validatorJobMetadata
	if err := json.Unmarshal([]byte(s[start:end+1]), &m); err != nil {
		return nil, err
	}
	return &m, nil
}
