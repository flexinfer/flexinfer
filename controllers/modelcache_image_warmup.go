package controllers

import (
	"context"
	"fmt"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	"github.com/flexinfer/flexinfer/pkg/quantization"
)

type imageWarmupState string

const (
	imageWarmupReady   imageWarmupState = "ready"
	imageWarmupPending imageWarmupState = "pending"
	imageWarmupFailed  imageWarmupState = "failed"
)

type imageWarmupRequest struct {
	JobName      string
	Phase        string
	Image        string
	NodeSelector map[string]string
	Tolerations  []corev1.Toleration
}

func shouldWarmImage(image string) bool {
	return strings.Contains(image, "/flexinfer/runtime")
}

func summarizeImageRef(image string) string {
	ref := image
	if slash := strings.LastIndex(ref, "/"); slash >= 0 {
		ref = ref[slash+1:]
	}
	if digest := strings.Index(ref, "@sha256:"); digest >= 0 {
		return ref[:digest+19]
	}
	return ref
}

func (r *ModelCacheReconciler) ensureImageWarmup(ctx context.Context, modelCache *aiv1alpha1.ModelCache, req imageWarmupRequest) (imageWarmupState, string, error) {
	if !shouldWarmImage(req.Image) {
		return imageWarmupReady, "", nil
	}

	logger := log.FromContext(ctx)
	existing := &batchv1.Job{}
	key := types.NamespacedName{Name: req.JobName, Namespace: modelCache.Namespace}
	if err := r.Get(ctx, key, existing); err != nil {
		if !apierrors.IsNotFound(err) {
			return imageWarmupFailed, "", err
		}
		job := quantization.BuildImageWarmupJob(req.JobName, modelCache.Namespace, modelCache.Name, req.Phase, req.Image, req.NodeSelector, req.Tolerations)
		if err := ctrl.SetControllerReference(modelCache, job, r.Scheme); err != nil {
			return imageWarmupFailed, "", err
		}
		logger.Info("Creating image warmup job", "job", job.Name, "image", req.Image, "phase", req.Phase)
		if _, err := createJobIdempotent(ctx, r.Client, job, "image_warmup", modelCache.Generation); err != nil {
			return imageWarmupFailed, "", err
		}
		r.Recorder.Event(modelCache, corev1.EventTypeNormal, "ImageWarmupStarted",
			fmt.Sprintf("%s image warmup job created for %s", req.Phase, summarizeImageRef(req.Image)))
		return imageWarmupPending, fmt.Sprintf("warming %s image %s", req.Phase, summarizeImageRef(req.Image)), nil
	}

	if existing.Status.Succeeded > 0 {
		return imageWarmupReady, "", nil
	}
	if existing.Status.Failed > 0 {
		detail := r.imageWarmupFailureDetail(ctx, modelCache.Namespace, req.JobName)
		if detail == "" {
			detail = fmt.Sprintf("%s image warmup failed for %s", req.Phase, summarizeImageRef(req.Image))
		}
		return imageWarmupFailed, detail, nil
	}

	return imageWarmupPending, fmt.Sprintf("warming %s image %s", req.Phase, summarizeImageRef(req.Image)), nil
}

func (r *ModelCacheReconciler) imageWarmupFailureDetail(ctx context.Context, namespace, jobName string) string {
	podList := &corev1.PodList{}
	if err := r.List(ctx, podList, client.InNamespace(namespace), client.MatchingLabels{"job-name": jobName}); err != nil {
		return ""
	}

	var parts []string
	seen := map[string]struct{}{}
	appendMsg := func(msg string) {
		msg = strings.TrimSpace(msg)
		if msg == "" {
			return
		}
		if _, ok := seen[msg]; ok {
			return
		}
		seen[msg] = struct{}{}
		parts = append(parts, msg)
	}

	for _, pod := range podList.Items {
		for _, status := range pod.Status.ContainerStatuses {
			if status.State.Waiting != nil {
				appendMsg(strings.TrimSpace(strings.Join([]string{status.State.Waiting.Reason, status.State.Waiting.Message}, ": ")))
			}
			if status.LastTerminationState.Terminated != nil {
				appendMsg(strings.TrimSpace(strings.Join([]string{status.LastTerminationState.Terminated.Reason, status.LastTerminationState.Terminated.Message}, ": ")))
			}
		}
	}

	return strings.Join(parts, "; ")
}
