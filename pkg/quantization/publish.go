// Package quantization — publish job builder.
// Publishing pushes model artifacts to an OCI registry (via oras) or
// HuggingFace Hub after the pipeline completes. No GPU required.
package quantization

import (
	"fmt"
	"os"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

const (
	// DefaultPublishMemoryGB is the default memory limit for publish jobs.
	// Publish is CPU+network only, needs memory for oras/hf_hub buffering.
	DefaultPublishMemoryGB = 8

	// DefaultPublishDeadlineSeconds is the default 2-hour deadline.
	DefaultPublishDeadlineSeconds = 7200

	// DefaultPublishCPU is the default CPU request for publish jobs.
	DefaultPublishCPU = 2
)

// BuildPublishJob creates a Kubernetes Job that publishes model artifacts.
func BuildPublishJob(params JobParams, spec *aiv1alpha1.PublishSpec) (*batchv1.Job, error) {
	if spec == nil {
		return nil, fmt.Errorf("publish spec is nil")
	}
	if len(spec.Targets) == 0 {
		return nil, fmt.Errorf("publish spec has no targets")
	}

	memoryGB := int32(DefaultPublishMemoryGB)
	if spec.MaxMemoryGB != nil && *spec.MaxMemoryGB > 0 {
		memoryGB = *spec.MaxMemoryGB
	}

	deadline := int64(DefaultPublishDeadlineSeconds)
	if spec.TimeoutSeconds != nil && *spec.TimeoutSeconds >= 300 {
		deadline = *spec.TimeoutSeconds
	}

	image := publishImage()
	env := publishEnv(params.ModelPath, spec)
	script := publishWrapperScript(spec)

	backoffLimit := int32(2)
	pvcVol, pvcMount := modelPVCVolume(params.PVCName)

	volumes := []corev1.Volume{pvcVol}
	mounts := []corev1.VolumeMount{pvcMount}

	// Mount credentials secret if specified.
	if spec.SecretRef != nil && *spec.SecretRef != "" {
		secretVol := corev1.Volume{
			Name: "publish-creds",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: *spec.SecretRef,
					Optional:   func() *bool { b := true; return &b }(),
				},
			},
		}
		secretMount := corev1.VolumeMount{
			Name:      "publish-creds",
			MountPath: "/etc/publish-creds",
			ReadOnly:  true,
		}
		volumes = append(volumes, secretVol)
		mounts = append(mounts, secretMount)
	}

	podSpec := corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyNever,
		Containers: []corev1.Container{
			{
				Name:            "publisher",
				Image:           image,
				ImagePullPolicy: corev1.PullAlways,
				Command:         []string{"/bin/bash", "-c"},
				Args:            []string{script},
				Env:             env,
				VolumeMounts:    mounts,
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse(fmt.Sprintf("%d", DefaultPublishCPU)),
						corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dGi", memoryGB)),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dGi", memoryGB)),
					},
				},
				TerminationMessagePath:   "/dev/termination-log",
				TerminationMessagePolicy: corev1.TerminationMessageReadFile,
			},
		},
		Volumes:      volumes,
		NodeSelector: params.NodeSelector,
		Tolerations:  params.Tolerations,
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-publish", params.Name),
			Namespace: params.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "flexinfer",
				"flexinfer.ai/component":       "publisher",
				"flexinfer.ai/cache":           params.Name,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoffLimit,
			ActiveDeadlineSeconds:   &deadline,
			TTLSecondsAfterFinished: func() *int32 { i := int32(300); return &i }(),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"flexinfer.ai/component": "publisher",
						"flexinfer.ai/cache":     params.Name,
					},
				},
				Spec: podSpec,
			},
		},
	}

	return job, nil
}

// publishEnv returns environment variables for the publish scripts.
func publishEnv(modelPath string, spec *aiv1alpha1.PublishSpec) []corev1.EnvVar {
	env := []corev1.EnvVar{
		{Name: "MODEL_DIR", Value: fmt.Sprintf("/cache/%s", modelPath)},
	}

	if spec.OCIRef != nil && *spec.OCIRef != "" {
		env = append(env, corev1.EnvVar{Name: "OCI_REF", Value: *spec.OCIRef})
	}
	if spec.HuggingFaceRepo != nil && *spec.HuggingFaceRepo != "" {
		env = append(env, corev1.EnvVar{Name: "HF_REPO", Value: *spec.HuggingFaceRepo})
	}

	// Inject credentials from secret via env vars.
	if spec.SecretRef != nil && *spec.SecretRef != "" {
		secretRef := &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: *spec.SecretRef},
		}
		for _, key := range []string{"OCI_USERNAME", "OCI_PASSWORD", "HF_TOKEN"} {
			ref := secretRef.DeepCopy()
			ref.Key = key
			ref.Optional = func() *bool { b := true; return &b }()
			env = append(env, corev1.EnvVar{
				Name: key,
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: ref,
				},
			})
		}
	}

	return env
}

// publishWrapperScript returns a bash script that runs the appropriate
// publish scripts for each target sequentially.
func publishWrapperScript(spec *aiv1alpha1.PublishSpec) string {
	var parts []string
	parts = append(parts, "set -euo pipefail")

	for _, target := range spec.Targets {
		switch target {
		case aiv1alpha1.PublishTargetOCI:
			parts = append(parts, "python3 /opt/flexinfer/scripts/publish_oci.py")
		case aiv1alpha1.PublishTargetHuggingFace:
			parts = append(parts, "python3 /opt/flexinfer/scripts/publish_hf.py")
		}
	}

	return strings.Join(parts, "\n")
}

// publishImage returns the image to use for publish jobs.
// Prefers the unified runtime image when available, falls back to GPTQ image.
func publishImage() string {
	if os.Getenv("FLEXINFER_USE_RUNTIME_FOR_QUANTIZE") == "true" {
		if img := os.Getenv("FLEXINFER_RUNTIME_IMAGE"); img != "" {
			return img
		}
	}
	// Fallback: GPTQ image has Python + huggingface_hub.
	if img := os.Getenv("FLEXINFER_QUANTIZER_GPTQ_IMAGE"); img != "" {
		return img
	}
	return "registry.harbor.lan/flexinfer/quantizer-gptq:latest"
}
