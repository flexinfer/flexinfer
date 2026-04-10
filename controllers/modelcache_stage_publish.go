package controllers

import (
	"context"
	"fmt"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	"github.com/flexinfer/flexinfer/pkg/quantization"
)

type publishStage string

const (
	publishStageSource      publishStage = "source"
	publishStageAbliterated publishStage = "abliterated"
)

type stagePublishAnnotationKeys struct {
	refKey     string
	digestKey  string
	versionKey string
}

func stagePublishKeys(stage publishStage) stagePublishAnnotationKeys {
	switch stage {
	case publishStageSource:
		return stagePublishAnnotationKeys{
			refKey:     annotationPublishSourceRef,
			digestKey:  annotationPublishSourceDigest,
			versionKey: annotationPublishSourceVersion,
		}
	case publishStageAbliterated:
		return stagePublishAnnotationKeys{
			refKey:     annotationPublishAblitRef,
			digestKey:  annotationPublishAblitDigest,
			versionKey: annotationPublishAblitVersion,
		}
	default:
		return stagePublishAnnotationKeys{}
	}
}

func hasOCIPublishTarget(spec *aiv1alpha1.PublishSpec) bool {
	if spec == nil || spec.OCIRef == nil || strings.TrimSpace(*spec.OCIRef) == "" {
		return false
	}
	for _, target := range spec.Targets {
		if target == aiv1alpha1.PublishTargetOCI {
			return true
		}
	}
	return false
}

func deriveStageOCIRef(ociRef string, stage publishStage) string {
	if strings.TrimSpace(ociRef) == "" {
		return ""
	}
	prefix := ""
	raw := ociRef
	for _, candidate := range []string{"oci://", "oras://"} {
		if strings.HasPrefix(raw, candidate) {
			prefix = candidate
			raw = strings.TrimPrefix(raw, candidate)
			break
		}
	}

	repo := raw
	tag := ""
	if at := strings.Index(raw, "@"); at >= 0 {
		repo = raw[:at]
	} else {
		slash := strings.LastIndex(raw, "/")
		colon := strings.LastIndex(raw, ":")
		if colon > slash {
			repo = raw[:colon]
			tag = raw[colon+1:]
		}
	}

	stageTag := string(stage)
	if tag == "" {
		return prefix + repo + ":" + stageTag
	}
	return prefix + repo + ":" + tag + "-" + stageTag
}

func stagePublishDesiredRef(spec *aiv1alpha1.PublishSpec, stage publishStage) string {
	if !hasOCIPublishTarget(spec) {
		return ""
	}
	return deriveStageOCIRef(strings.TrimSpace(*spec.OCIRef), stage)
}

func stagePublishDesiredVersion(mc *aiv1alpha1.ModelCache, stage publishStage) string {
	switch stage {
	case publishStageSource:
		return sourceHash(mc.Spec.Source)
	case publishStageAbliterated:
		return sourceHash(mc.Spec.Source) + ":" + ablitSpecHash(mc.Spec.Abliteration)
	default:
		return ""
	}
}

func stagePublishJobName(cacheName string, stage publishStage) string {
	return cacheName + "-publish-" + string(stage)
}

func stagePublishCurrentPhase(stage publishStage) string {
	return "publish-" + string(stage)
}

func stagePublishUpToDate(mc *aiv1alpha1.ModelCache, stage publishStage, desiredRef, desiredVersion string) bool {
	if mc == nil || mc.Annotations == nil || desiredRef == "" || desiredVersion == "" {
		return false
	}
	keys := stagePublishKeys(stage)
	return mc.Annotations[keys.refKey] == desiredRef &&
		mc.Annotations[keys.versionKey] == desiredVersion &&
		strings.TrimSpace(mc.Annotations[keys.digestKey]) != ""
}

func stagePublishSpec(base *aiv1alpha1.PublishSpec, ref string) *aiv1alpha1.PublishSpec {
	if base == nil || strings.TrimSpace(ref) == "" {
		return nil
	}
	overwrite := "overwrite"
	spec := &aiv1alpha1.PublishSpec{
		Targets:        []aiv1alpha1.PublishTarget{aiv1alpha1.PublishTargetOCI},
		OCIRef:         &ref,
		SecretRef:      base.SecretRef,
		MaxMemoryGB:    base.MaxMemoryGB,
		TimeoutSeconds: base.TimeoutSeconds,
		TagPolicy:      &overwrite,
	}
	return spec
}

func (r *ModelCacheReconciler) reconcileStagePublish(
	ctx context.Context,
	modelCache *aiv1alpha1.ModelCache,
	pvcName, modelPath string,
	stage publishStage,
) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	desiredRef := stagePublishDesiredRef(modelCache.Spec.Publish, stage)
	desiredVersion := stagePublishDesiredVersion(modelCache, stage)
	if desiredRef == "" || desiredVersion == "" || stagePublishUpToDate(modelCache, stage, desiredRef, desiredVersion) {
		return ctrl.Result{}, nil
	}

	jobName := stagePublishJobName(modelCache.Name, stage)
	job := &batchv1.Job{}
	err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: modelCache.Namespace}, job)
	if err != nil && errors.IsNotFound(err) {
		tolerations := []corev1.Toleration{{
			Key:      "dedicated",
			Operator: corev1.TolerationOpEqual,
			Value:    "gpu",
			Effect:   corev1.TaintEffectNoSchedule,
		}}
		params := quantization.JobParams{
			Name:         modelCache.Name,
			Namespace:    modelCache.Namespace,
			PVCName:      pvcName,
			ModelPath:    modelPath,
			NodeSelector: modelCache.Spec.NodeSelector,
			Tolerations:  tolerations,
		}
		stageSpec := stagePublishSpec(modelCache.Spec.Publish, desiredRef)
		newJob, buildErr := quantization.BuildPublishJob(params, stageSpec)
		if buildErr != nil {
			r.Recorder.Event(modelCache, corev1.EventTypeWarning, "StagePublishFailed",
				fmt.Sprintf("Failed to build %s publish job: %s", stage, buildErr))
			modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseFailed
			modelCache.Status.CurrentPhase = stagePublishCurrentPhase(stage)
			if statusErr := r.Status().Update(ctx, modelCache); statusErr != nil {
				return ctrl.Result{}, statusErr
			}
			return ctrl.Result{}, nil
		}

		newJob.Name = jobName
		if newJob.Labels == nil {
			newJob.Labels = map[string]string{}
		}
		newJob.Labels["flexinfer.ai/publish-stage"] = string(stage)
		if newJob.Spec.Template.Labels == nil {
			newJob.Spec.Template.Labels = map[string]string{}
		}
		newJob.Spec.Template.Labels["flexinfer.ai/publish-stage"] = string(stage)

		if err := ctrl.SetControllerReference(modelCache, newJob, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		log.Info("Creating stage publish job", "cache", modelCache.Name, "stage", stage, "job", newJob.Name, "ref", desiredRef)
		if err := r.Create(ctx, newJob); err != nil {
			return ctrl.Result{}, err
		}

		modelCache.Status.Phase = aiv1alpha1.ModelCachePhasePublishing
		modelCache.Status.CurrentPhase = stagePublishCurrentPhase(stage)
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}
		r.Recorder.Event(modelCache, corev1.EventTypeNormal, "StagePublishStarted",
			fmt.Sprintf("%s artifact publish job created: %s", stage, desiredRef))
		return ctrl.Result{RequeueAfter: requeueLong}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	}

	if job.Status.Succeeded > 0 {
		if modelCache.Status.RetryCount > 0 {
			r.resetRetryCount(modelCache)
		}
		meta := r.readPublishMetadataFromPods(ctx, modelCache.Namespace, job.Name)
		digest := ""
		if meta != nil {
			digest = meta.OCIDigest
		}
		keys := stagePublishKeys(stage)
		if err := r.updateModelCacheAnnotations(ctx, types.NamespacedName{Name: modelCache.Name, Namespace: modelCache.Namespace}, func(annotations map[string]string) {
			annotations[keys.refKey] = desiredRef
			annotations[keys.versionKey] = desiredVersion
			annotations[keys.digestKey] = digest
		}); err != nil {
			return ctrl.Result{}, err
		}
		r.Recorder.Event(modelCache, corev1.EventTypeNormal, "StagePublishComplete",
			fmt.Sprintf("%s artifact published: %s", stage, desiredRef))
		return ctrl.Result{Requeue: true}, nil
	}

	if job.Status.Failed > 0 {
		failureMsg := capturePublishFailureLogs(ctx, r.Client, modelCache.Namespace, job.Name)
		phaseKey := stagePublishCurrentPhase(stage)
		if shouldRetry, backoff := r.shouldRetryFailedPhase(modelCache, phaseKey); shouldRetry {
			r.recordFailure(modelCache, phaseKey)
			if err := r.deleteFailedJob(ctx, modelCache.Namespace, jobName); err != nil {
				return ctrl.Result{}, err
			}
			modelCache.Status.Phase = aiv1alpha1.ModelCachePhasePublishing
			modelCache.Status.CurrentPhase = phaseKey
			if err := r.Status().Update(ctx, modelCache); err != nil {
				return ctrl.Result{}, err
			}
			r.Recorder.Event(modelCache, corev1.EventTypeWarning, "StagePublishRetry",
				fmt.Sprintf("%s artifact publish failed, retry %d/%d in %s: %s",
					stage, modelCache.Status.RetryCount, modelCache.Spec.GetMaxRetries(), backoff, truncateString(failureMsg, 200)))
			return ctrl.Result{RequeueAfter: backoff}, nil
		}

		modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseFailed
		modelCache.Status.CurrentPhase = phaseKey
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}
		r.Recorder.Event(modelCache, corev1.EventTypeWarning, "StagePublishFailed",
			fmt.Sprintf("%s artifact publish failed after %d retries: %s",
				stage, modelCache.Status.RetryCount, truncateString(failureMsg, 200)))
		return ctrl.Result{}, nil
	}

	modelCache.Status.Phase = aiv1alpha1.ModelCachePhasePublishing
	modelCache.Status.CurrentPhase = stagePublishCurrentPhase(stage)
	if err := r.Status().Update(ctx, modelCache); err != nil {
		log.Error(err, "Failed to update stage publish progress", "stage", stage)
	}
	return ctrl.Result{RequeueAfter: requeueLong}, nil
}
