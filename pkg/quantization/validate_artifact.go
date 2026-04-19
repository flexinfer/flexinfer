// Package quantization — pre-publish artifact validator job builder.
//
// BuildValidateArtifactJob produces a one-shot Job that runs the offline
// validator (build/scripts/validate_quantized_artifact.py, baked into the
// runtime image at /opt/flexinfer/scripts/) against the on-PVC artifact
// before publish. The controller gates publish on the validator's JSON
// result.
//
// Validator is CPU-only — no GPU resources requested. Output JSON is
// written to /dev/termination-log so the controller can read it from the
// Job's pod without log scraping.
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
	// ValidatorScriptPath is the in-image path of the validator. It is
	// shipped under /opt/flexinfer/scripts/ via build/Dockerfile.runtime
	// (`COPY build/scripts/ /opt/flexinfer/scripts/`).
	ValidatorScriptPath = "/opt/flexinfer/scripts/validate_quantized_artifact.py"

	// DefaultValidatorMemoryGB is enough for safetensors metadata reads on
	// large MoE artifacts.
	DefaultValidatorMemoryGB = 4

	// DefaultValidatorCPU is the validator job CPU request.
	DefaultValidatorCPU = 1

	// DefaultValidatorDeadlineSeconds is the default 10-minute deadline.
	DefaultValidatorDeadlineSeconds = 600

	// ValidatorJobSuffix is the publish-validate Job name suffix.
	ValidatorJobSuffix = "-validate-publish"
)

// BuildValidateArtifactJob constructs the pre-publish validator Job.
//
// The script is invoked with --json so the JSON document is the only
// thing on stdout and (via tee) on /dev/termination-log. The controller
// parses that document to drive the publish gate.
func BuildValidateArtifactJob(params JobParams, spec *aiv1alpha1.PublishValidateSpec) (*batchv1.Job, error) {
	if spec == nil {
		return nil, fmt.Errorf("publish validate spec is nil")
	}
	if !spec.Enabled {
		return nil, fmt.Errorf("publish validate spec is disabled")
	}

	memoryGB := int32(DefaultValidatorMemoryGB)
	if spec.MaxMemoryGB != nil && *spec.MaxMemoryGB > 0 {
		memoryGB = *spec.MaxMemoryGB
	}

	deadline := int64(DefaultValidatorDeadlineSeconds)
	if spec.TimeoutSeconds != nil && *spec.TimeoutSeconds >= 60 {
		deadline = *spec.TimeoutSeconds
	}

	image := validateArtifactImage(spec)
	script := validatorWrapperScript(params.ModelPath, spec)

	pvcVol, pvcMount := modelPVCVolume(params.PVCName)

	backoffLimit := int32(1)
	ttl := int32(300)

	podSpec := corev1.PodSpec{
		RestartPolicy:     corev1.RestartPolicyNever,
		PriorityClassName: PriorityClassBulk,
		NodeSelector:      params.NodeSelector,
		Tolerations:       params.Tolerations,
		Volumes:           []corev1.Volume{pvcVol},
		Containers: []corev1.Container{
			{
				Name:            "validator",
				Image:           image,
				ImagePullPolicy: ImagePullPolicyForImage(image),
				Command:         []string{"/bin/sh", "-c"},
				Args:            []string{script},
				VolumeMounts:    []corev1.VolumeMount{pvcMount},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse(fmt.Sprintf("%d", DefaultValidatorCPU)),
						corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dGi", memoryGB)),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dGi", memoryGB)),
					},
				},
				TerminationMessagePath:   "/dev/termination-log",
				TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
			},
		},
	}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      params.Name + ValidatorJobSuffix,
			Namespace: params.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "flexinfer",
				"flexinfer.ai/component":       "publish-validator",
				"flexinfer.ai/cache":           params.Name,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoffLimit,
			ActiveDeadlineSeconds:   &deadline,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"flexinfer.ai/component": "publish-validator",
						"flexinfer.ai/cache":     params.Name,
					},
				},
				Spec: podSpec,
			},
		},
	}, nil
}

// validateArtifactImage resolves the image for the validator container.
// Precedence (highest first):
//  1. spec.Image (per-cache override)
//  2. FLEXINFER_VALIDATOR_IMAGE env var
//  3. FLEXINFER_RUNTIME_IMAGE env var (validator script ships with runtime)
//  4. Hardcoded fallback (python3-slim — script will fail "no safetensors"
//     but the failure is loud and points operators at the real fix).
func validateArtifactImage(spec *aiv1alpha1.PublishValidateSpec) string {
	if spec != nil && spec.Image != nil && *spec.Image != "" {
		return *spec.Image
	}
	if img := os.Getenv("FLEXINFER_VALIDATOR_IMAGE"); img != "" {
		return img
	}
	if img := os.Getenv("FLEXINFER_RUNTIME_IMAGE"); img != "" {
		return img
	}
	return "python:3.11-slim"
}

// validatorWrapperScript returns the shell wrapper that invokes the
// validator and captures its JSON to /dev/termination-log so the
// controller can parse it without scraping pod logs.
//
// The wrapper:
//  1. Picks the artifact directory (MODEL_DIR by default).
//  2. Calls the validator with --json.
//  3. Tees output to both stdout (Loki) and /dev/termination-log
//     (kube termination message).
//  4. Echoes a "validator missing" sentinel JSON if the script is
//     absent so the controller can pattern-match the failure cleanly.
func validatorWrapperScript(modelPath string, spec *aiv1alpha1.PublishValidateSpec) string {
	layout := "auto"
	if spec.Layout != nil && *spec.Layout != "" {
		layout = *spec.Layout
	}
	family := "auto"
	if spec.Family != nil && *spec.Family != "" {
		family = *spec.Family
	}

	// Quote arguments via single quotes after stripping any single quote
	// characters. layout/family come from the CRD enum + a regex, so
	// injection risk is bounded, but keep the belt-and-suspenders.
	layout = strings.ReplaceAll(layout, "'", "")
	family = strings.ReplaceAll(family, "'", "")

	return fmt.Sprintf(`set -eu
ARTIFACT_PATH="/cache/%s"

if [ ! -f "%s" ]; then
  echo '{"ok":false,"errors":["validator script not present in image at %s"],"warnings":[],"layout":"unknown","family":"unknown"}' | tee /dev/termination-log
  exit 1
fi

# Validator is CPU-only; emit JSON to both stdout (Loki) and the
# termination log (controller).
python3 "%s" \
  --artifact-path "${ARTIFACT_PATH}" \
  --layout '%s' \
  --family '%s' \
  --json | tee /dev/termination-log
`,
		modelPath,
		ValidatorScriptPath,
		ValidatorScriptPath,
		ValidatorScriptPath,
		layout,
		family,
	)
}
