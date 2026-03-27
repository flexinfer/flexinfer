// Package quantization — publish job builder.
// Publishing pushes model artifacts to an OCI registry (via oras) or
// HuggingFace Hub after the pipeline completes. No GPU required.
package quantization

import (
	"fmt"
	"net/url"
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
				ImagePullPolicy: ImagePullPolicyForImage(image),
				Command:         []string{"/bin/sh", "-c"},
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
		if shouldUseInsecure(*spec.OCIRef) {
			env = append(env, corev1.EnvVar{Name: "OCI_INSECURE", Value: "true"})
		}
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

// shouldUseInsecure returns true for private registries (e.g. .lan) that use
// self-signed TLS certificates. This triggers --insecure (HTTPS without cert
// verify) rather than --plain-http (HTTP), since Harbor's HTTP endpoint has
// limited OCI distribution support (manifest PUT returns 404).
func shouldUseInsecure(ociRef string) bool {
	host := ociRef
	if strings.Contains(ociRef, "://") {
		if parsed, err := url.Parse(ociRef); err == nil && parsed.Host != "" {
			host = parsed.Host
		}
	} else if idx := strings.IndexRune(ociRef, '/'); idx >= 0 {
		host = ociRef[:idx]
	}
	host = strings.TrimSpace(strings.ToLower(host))
	return strings.HasSuffix(host, ".lan")
}

// publishWrapperScript returns a shell script that runs the appropriate
// publish logic for each target sequentially. The OCI target is inlined
// as a POSIX sh script so any image with sh works (including alpine).
func publishWrapperScript(spec *aiv1alpha1.PublishSpec) string {
	var parts []string
	parts = append(parts, "set -eu")

	for _, target := range spec.Targets {
		switch target {
		case aiv1alpha1.PublishTargetOCI:
			parts = append(parts, ociPublishScript())
		case aiv1alpha1.PublishTargetHuggingFace:
			parts = append(parts, "python3 /opt/flexinfer/scripts/publish_hf.py")
		}
	}

	return strings.Join(parts, "\n")
}

// ociPublishScript returns an inlined POSIX sh script for OCI publish via oras.
func ociPublishScript() string {
	return `
apk add --no-cache curl >/dev/null 2>&1 || true

if ! command -v oras >/dev/null 2>&1; then
  echo "oras not found, installing v1.2.0..."
  curl -fsSL https://github.com/oras-project/oras/releases/download/v1.2.0/oras_1.2.0_linux_amd64.tar.gz | tar -xz -C /usr/local/bin oras
  echo "oras installed successfully"
fi

if [ -z "${MODEL_DIR:-}" ] || [ -z "${OCI_REF:-}" ]; then
  echo "ERROR: MODEL_DIR and OCI_REF are required" >&2; exit 1
fi
if [ ! -d "$MODEL_DIR" ]; then
  echo "ERROR: MODEL_DIR does not exist: $MODEL_DIR" >&2; exit 1
fi

REGISTRY=$(echo "$OCI_REF" | cut -d'/' -f1)
INSECURE_FLAG=""
if [ "${OCI_INSECURE:-}" = "true" ] || echo "$REGISTRY" | grep -q '\.lan$'; then
  INSECURE_FLAG="--insecure"
fi

TOTAL_BYTES=0
FILE_COUNT=0
for f in $(find "$MODEL_DIR" -type f); do
  sz=$(stat -c '%s' "$f" 2>/dev/null || stat -f '%z' "$f" 2>/dev/null || echo 0)
  TOTAL_BYTES=$((TOTAL_BYTES + sz))
  FILE_COUNT=$((FILE_COUNT + 1))
done
echo "{\"event\":\"start\",\"phase\":\"publishing\",\"target\":\"oci\",\"total_bytes\":$TOTAL_BYTES,\"file_count\":$FILE_COUNT}"

if [ -n "${OCI_USERNAME:-}" ] && [ -n "${OCI_PASSWORD:-}" ]; then
  oras login $INSECURE_FLAG "$REGISTRY" -u "$OCI_USERNAME" -p "$OCI_PASSWORD"
  echo "{\"event\":\"progress\",\"phase\":\"authenticated\",\"percent\":5}"
fi

ARTIFACTS=""
for fpath in $(find "$MODEL_DIR" -type f); do
  rel=$(echo "$fpath" | sed "s|^$MODEL_DIR/||")
  ARTIFACTS="$ARTIFACTS $fpath:$rel"
done

echo "{\"event\":\"progress\",\"phase\":\"pushing\",\"percent\":10,\"detail\":\"$FILE_COUNT files\"}"

START_TIME=$(date +%s)
oras push $INSECURE_FLAG --disable-path-validation "$OCI_REF" $ARTIFACTS --artifact-type "application/vnd.flexinfer.model.v1" 2>&1 | tee /tmp/oras-output.log
END_TIME=$(date +%s)
DURATION=$((END_TIME - START_TIME))

DIGEST=$(grep 'Digest: sha256:' /tmp/oras-output.log | head -1 | sed 's/.*Digest: //' || echo "")

echo "{\"event\":\"complete\",\"phase\":\"publishing\",\"target\":\"oci\",\"duration_seconds\":$DURATION,\"digest\":\"$DIGEST\"}"

cat > /dev/termination-log <<TERMEOF
{"target":"oci","ociRef":"$OCI_REF","ociDigest":"$DIGEST","durationSeconds":$DURATION,"totalBytes":$TOTAL_BYTES,"fileCount":$FILE_COUNT}
TERMEOF

echo "Published to $OCI_REF (digest: $DIGEST) in ${DURATION}s"
`
}

// publishImage returns the image to use for publish jobs.
// Prefers FLEXINFER_PUBLISH_IMAGE. Defaults to alpine (publish only
// needs sh + curl for oras install, no Python/GPU required).
func publishImage() string {
	if img := os.Getenv("FLEXINFER_PUBLISH_IMAGE"); img != "" {
		return img
	}
	return "alpine:3.23"
}
