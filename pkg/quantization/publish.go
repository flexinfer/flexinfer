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
	// DefaultModelToolsImage is the lightweight CPU-only utility image used for
	// publishing and artifact validation. It bakes in oras, safetensors,
	// huggingface_hub, and the FlexInfer helper scripts so publish jobs do not
	// need to install tools at runtime or pull GPU runtime images.
	DefaultModelToolsImage = "ghcr.io/flexinfer/model-tools:latest"

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
		RestartPolicy:     corev1.RestartPolicyNever,
		PriorityClassName: PriorityClassBulk,
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

	// Pass tag policy and additional tags to the publish script.
	tagPolicy := "overwrite"
	if spec.TagPolicy != nil && *spec.TagPolicy != "" {
		tagPolicy = *spec.TagPolicy
	}
	env = append(env, corev1.EnvVar{Name: "OCI_TAG_POLICY", Value: tagPolicy})
	if len(spec.AdditionalTags) > 0 {
		env = append(env, corev1.EnvVar{Name: "OCI_ADDITIONAL_TAGS", Value: strings.Join(spec.AdditionalTags, ",")})
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

json_escape() {
  printf '%s' "${1:-}" | sed 's/\\/\\\\/g; s/"/\\"/g' | tr '\n' ' '
}

PUBLISH_STATUS="failed"
PUBLISH_PHASE="init"
PUBLISH_ERROR=""
PUBLISH_DIGEST=""
PUSH_REF="${OCI_REF:-}"
PUSHED_TAGS=""
TOTAL_BYTES=0
FILE_COUNT=0
START_TIME=0

write_publish_metadata() {
  rc="${1:-0}"
  end_time=$(date +%s 2>/dev/null || echo 0)
  duration=0
  if [ "${START_TIME:-0}" -gt 0 ] 2>/dev/null; then
    duration=$((end_time - START_TIME))
  fi
  if [ "$rc" -eq 0 ]; then
    PUBLISH_STATUS="success"
  fi
  cat > /dev/termination-log <<TERMEOF
{"target":"oci","status":"$(json_escape "$PUBLISH_STATUS")","phase":"$(json_escape "$PUBLISH_PHASE")","ociRef":"$(json_escape "${PUSH_REF:-${OCI_REF:-}}")","ociDigest":"$(json_escape "$PUBLISH_DIGEST")","pushedTags":"$(json_escape "$PUSHED_TAGS")","durationSeconds":$duration,"totalBytes":${TOTAL_BYTES:-0},"fileCount":${FILE_COUNT:-0},"error":"$(json_escape "$PUBLISH_ERROR")"}
TERMEOF
}

fail_publish() {
  PUBLISH_ERROR="${1:-publish failed}"
  write_publish_metadata 1
  echo "ERROR: $PUBLISH_ERROR" >&2
  exit 1
}

if ! command -v oras >/dev/null 2>&1; then
  PUBLISH_PHASE="install_oras"
  echo "oras not found, installing v1.2.0..."
  if ! curl -fsSL https://github.com/oras-project/oras/releases/download/v1.2.0/oras_1.2.0_linux_amd64.tar.gz | tar -xz -C /usr/local/bin oras; then
    fail_publish "failed to install oras v1.2.0"
  fi
  echo "oras installed successfully"
fi

if [ -z "${MODEL_DIR:-}" ] || [ -z "${OCI_REF:-}" ]; then
  fail_publish "MODEL_DIR and OCI_REF are required"
fi
if [ ! -d "$MODEL_DIR" ]; then
  fail_publish "MODEL_DIR does not exist: $MODEL_DIR"
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
  PUBLISH_PHASE="authenticating"
  if ! oras login $INSECURE_FLAG "$REGISTRY" -u "$OCI_USERNAME" -p "$OCI_PASSWORD"; then
    fail_publish "oras login failed for registry $REGISTRY"
  fi
  echo "{\"event\":\"progress\",\"phase\":\"authenticated\",\"percent\":5}"
fi

# Apply tag policy to modify OCI_REF before push.
TAG_POLICY="${OCI_TAG_POLICY:-overwrite}"
PUSH_REF="$OCI_REF"
case "$TAG_POLICY" in
  timestamp)
    BASE_REF=$(echo "$OCI_REF" | sed 's/:.*$//')
    BASE_TAG=$(echo "$OCI_REF" | grep -o ':[^:]*$' | tr -d ':')
    [ -z "$BASE_TAG" ] && BASE_TAG="latest"
    TS=$(date -u +%Y%m%d-%H%M%S)
    PUSH_REF="${BASE_REF}:${BASE_TAG}-${TS}"
    echo "Tag policy: timestamp → pushing as $PUSH_REF"
    ;;
  digest-suffix)
    # Will be modified after push once we have the digest
    echo "Tag policy: digest-suffix → will re-tag after push"
    ;;
  *)
    echo "Tag policy: overwrite → pushing as $PUSH_REF"
    ;;
esac

PUSHED_TAGS="$PUSH_REF"
PUBLISH_PHASE="pushing"
echo "{\"event\":\"progress\",\"phase\":\"pushing\",\"percent\":10,\"detail\":\"$FILE_COUNT files\"}"

# Push from MODEL_DIR so ORAS uses relative paths as artifact titles.
# ORAS v1.x sets title annotation from the argument path; absolute paths
# cause "path traversal disallowed" on pull.
START_TIME=$(date +%s)
if ! (cd "$MODEL_DIR" && oras push $INSECURE_FLAG --disable-path-validation "$PUSH_REF" $(find . -type f | sed 's|^\./||') --artifact-type "application/vnd.flexinfer.model.v1") > /tmp/oras-output.log 2>&1; then
  cat /tmp/oras-output.log
  PUBLISH_ERROR=$(tail -n 20 /tmp/oras-output.log | tr '\n' ' ')
  fail_publish "${PUBLISH_ERROR:-oras push failed}"
fi
cat /tmp/oras-output.log
END_TIME=$(date +%s)
DURATION=$((END_TIME - START_TIME))

DIGEST=$(grep 'Digest: sha256:' /tmp/oras-output.log | head -1 | sed 's/.*Digest: //' || echo "")
PUBLISH_DIGEST="$DIGEST"

if [ -z "$DIGEST" ]; then
  fail_publish "oras push completed without reporting a digest"
fi

# For digest-suffix policy, create an additional tag with the digest prefix.
if [ "$TAG_POLICY" = "digest-suffix" ] && [ -n "$DIGEST" ]; then
  PUBLISH_PHASE="tagging"
  BASE_REF=$(echo "$OCI_REF" | sed 's/:.*$//')
  BASE_TAG=$(echo "$OCI_REF" | grep -o ':[^:]*$' | tr -d ':')
  [ -z "$BASE_TAG" ] && BASE_TAG="latest"
  SHORT_DIGEST=$(echo "$DIGEST" | sed 's/sha256://' | cut -c1-12)
  DIGEST_TAG="${BASE_REF}:${BASE_TAG}-sha256-${SHORT_DIGEST}"
  echo "Creating digest-suffix tag: $DIGEST_TAG"
  if ! oras tag $INSECURE_FLAG "$PUSH_REF" "${BASE_TAG}-sha256-${SHORT_DIGEST}" 2>&1; then
    fail_publish "oras tag failed for digest-suffix tag $DIGEST_TAG"
  fi
  PUSHED_TAGS="${PUSHED_TAGS},${DIGEST_TAG}"
fi

# Apply additional tags (server-side, no re-upload).
if [ -n "${OCI_ADDITIONAL_TAGS:-}" ]; then
  PUBLISH_PHASE="tagging"
  echo "{\"event\":\"progress\",\"phase\":\"tagging\",\"percent\":90}"
  IFS=',' read -r TAGS_STR <<EOF
${OCI_ADDITIONAL_TAGS}
EOF
  for tag in $(echo "$TAGS_STR" | tr ',' ' '); do
    [ -z "$tag" ] && continue
    BASE_REF=$(echo "$OCI_REF" | sed 's/:.*$//')
    EXTRA_REF="${BASE_REF}:${tag}"
    echo "Applying additional tag: $EXTRA_REF"
    if ! oras tag $INSECURE_FLAG "$PUSH_REF" "$tag" 2>&1; then
      fail_publish "oras tag failed for additional tag $EXTRA_REF"
    fi
    PUSHED_TAGS="${PUSHED_TAGS},${EXTRA_REF}"
  done
fi

PUBLISH_PHASE="complete"
PUBLISH_STATUS="success"
echo "{\"event\":\"complete\",\"phase\":\"publishing\",\"target\":\"oci\",\"duration_seconds\":$DURATION,\"digest\":\"$DIGEST\"}"

write_publish_metadata 0

echo "Published to $PUSH_REF (digest: $DIGEST) in ${DURATION}s"
`
}

// publishImage returns the image to use for publish jobs.
// Prefers FLEXINFER_PUBLISH_IMAGE, then the shared FLEXINFER_MODEL_TOOLS_IMAGE.
func publishImage() string {
	if img := os.Getenv("FLEXINFER_PUBLISH_IMAGE"); img != "" {
		return img
	}
	if img := os.Getenv("FLEXINFER_MODEL_TOOLS_IMAGE"); img != "" {
		return img
	}
	return DefaultModelToolsImage
}
