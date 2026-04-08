package quantization

import (
	"fmt"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

const (
	defaultImageWarmupCPURequest    = "10m"
	defaultImageWarmupMemoryRequest = "32Mi"
	defaultImageWarmupMemoryLimit   = "128Mi"
)

// ImagePullPolicyForImage returns the appropriate pull policy based on image reference format.
// - Digest-based images (@sha256:...): IfNotPresent (immutable, safe to cache)
// - Tag-based with named tag (e.g., :v1.2.3, :rocm-gfx1100): IfNotPresent
// - :latest or no tag: Always
func ImagePullPolicyForImage(image string) corev1.PullPolicy {
	// Digest-based images are immutable.
	if strings.Contains(image, "@sha256:") {
		return corev1.PullIfNotPresent
	}
	// Extract tag.
	tag := extractImageTag(image)
	// :latest or empty tag defaults to Always.
	if tag == "" || tag == "latest" {
		return corev1.PullAlways
	}
	// Named tags: IfNotPresent (production images are versioned).
	return corev1.PullIfNotPresent
}

// extractImageTag returns the tag portion of an image reference, or empty string.
func extractImageTag(image string) string {
	// Handle digest references (no tag extraction needed).
	if strings.Contains(image, "@") {
		return ""
	}
	// Find the last colon that's not part of a port.
	parts := strings.Split(image, "/")
	lastPart := parts[len(parts)-1]
	if idx := strings.LastIndex(lastPart, ":"); idx != -1 {
		return lastPart[idx+1:]
	}
	return ""
}

// ResolveImagePullPolicy returns the pull policy for an image, considering GPUProfile override.
// Priority: GPUProfile.ImagePullPolicy > ImagePullPolicyForImage(image)
func ResolveImagePullPolicy(image string, profileOverride *corev1.PullPolicy) corev1.PullPolicy {
	if profileOverride != nil {
		return *profileOverride
	}
	return ImagePullPolicyForImage(image)
}

// BuildImageWarmupJob creates a tiny per-node job whose only purpose is to
// pull the target image before the real GPU workload starts.
func BuildImageWarmupJob(name, namespace, cacheName, phase, image string, nodeSelector map[string]string, tolerations []corev1.Toleration) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "flexinfer",
				"flexinfer.ai/component":       "image-warmer",
				"flexinfer.ai/cache":           cacheName,
				"flexinfer.ai/phase":           phase,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            ptr.To(int32(1)),
			TTLSecondsAfterFinished: ptr.To(int32(300)),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:                corev1.RestartPolicyNever,
					AutomountServiceAccountToken: ptr.To(false),
					PriorityClassName:            PriorityClassWarmup,
					NodeSelector:                 nodeSelector,
					Tolerations:                  tolerations,
					Containers: []corev1.Container{
						{
							Name:            "image-warmer",
							Image:           image,
							ImagePullPolicy: ImagePullPolicyForImage(image),
							Command:         []string{"/bin/sh", "-c"},
							Args:            []string{fmt.Sprintf("echo warmed %s image %s", phase, image)},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse(defaultImageWarmupCPURequest),
									corev1.ResourceMemory: resource.MustParse(defaultImageWarmupMemoryRequest),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse(defaultImageWarmupMemoryLimit),
								},
							},
						},
					},
				},
			},
		},
	}
}
