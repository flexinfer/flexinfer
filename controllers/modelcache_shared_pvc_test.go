package controllers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

func TestDownloadGCdShouldProceed(t *testing.T) {
	tests := []struct {
		name   string
		status aiv1alpha1.ModelCacheStatus
		want   bool
	}{
		{
			name:   "empty status — download never completed",
			status: aiv1alpha1.ModelCacheStatus{},
			want:   false,
		},
		{
			name: "phase Ready but path empty — rollout race, should re-download",
			status: aiv1alpha1.ModelCacheStatus{
				Phase: aiv1alpha1.ModelCachePhaseReady,
			},
			want: false,
		},
		{
			name: "phase Quantizing but path empty — rollout race, should re-download",
			status: aiv1alpha1.ModelCacheStatus{
				Phase: aiv1alpha1.ModelCachePhaseQuantizing,
			},
			want: false,
		},
		{
			name: "phase Provisioning with path empty — normal case, re-download",
			status: aiv1alpha1.ModelCacheStatus{
				Phase: aiv1alpha1.ModelCachePhaseProvisioning,
			},
			want: false,
		},
		{
			name: "phase Ready with path set — download completed, proceed",
			status: aiv1alpha1.ModelCacheStatus{
				Phase: aiv1alpha1.ModelCachePhaseReady,
				Path:  "my-pvc:my-model",
			},
			want: true,
		},
		{
			name: "phase Quantizing with path set — download completed, proceed",
			status: aiv1alpha1.ModelCacheStatus{
				Phase: aiv1alpha1.ModelCachePhaseQuantizing,
				Path:  "my-pvc:my-model",
			},
			want: true,
		},
		{
			name: "phase Abliterating with path set — download completed, proceed",
			status: aiv1alpha1.ModelCacheStatus{
				Phase: aiv1alpha1.ModelCachePhaseAbliterating,
				Path:  "my-pvc:my-model",
			},
			want: true,
		},
		{
			name: "phase Failed with path set — pipeline failed after download, proceed",
			status: aiv1alpha1.ModelCacheStatus{
				Phase: aiv1alpha1.ModelCachePhaseFailed,
				Path:  "my-pvc:my-model",
			},
			want: true,
		},
		{
			name: "phase Finetuning with path set — download completed, proceed",
			status: aiv1alpha1.ModelCacheStatus{
				Phase: aiv1alpha1.ModelCachePhaseFinetuning,
				Path:  "my-pvc:my-model",
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := downloadGCdShouldProceed(&tt.status)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDownloadJobPredatesPVC(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name string
		job  *batchv1.Job
		pvc  *corev1.PersistentVolumeClaim
		want bool
	}{
		{
			name: "older job is stale for recreated pvc",
			job: &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(now.Add(-10 * time.Minute))},
			},
			pvc: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(now)},
			},
			want: true,
		},
		{
			name: "newer job belongs to current pvc",
			job: &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(now)},
			},
			pvc: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(now.Add(-10 * time.Minute))},
			},
			want: false,
		},
		{
			name: "missing pvc does not trigger reset",
			job: &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(now)},
			},
			pvc:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, downloadJobPredatesPVC(tt.job, tt.pvc))
		})
	}
}

func TestManagedPVCNeedsRecreate(t *testing.T) {
	tests := []struct {
		name     string
		existing *corev1.PersistentVolumeClaim
		desired  *corev1.PersistentVolumeClaim
		want     bool
		wantWhy  string
	}{
		{
			name: "storage class drift requires recreation",
			existing: &corev1.PersistentVolumeClaim{
				Spec: corev1.PersistentVolumeClaimSpec{
					StorageClassName: strPtr("bulk-1r-stable"),
					AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				},
			},
			desired: &corev1.PersistentVolumeClaim{
				Spec: corev1.PersistentVolumeClaimSpec{
					StorageClassName: strPtr("nvme-1r-gpu"),
					AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				},
			},
			want:    true,
			wantWhy: `storageClassName "bulk-1r-stable" -> "nvme-1r-gpu"`,
		},
		{
			name: "access mode drift requires recreation",
			existing: &corev1.PersistentVolumeClaim{
				Spec: corev1.PersistentVolumeClaimSpec{
					StorageClassName: strPtr("longhorn"),
					AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
				},
			},
			desired: &corev1.PersistentVolumeClaim{
				Spec: corev1.PersistentVolumeClaimSpec{
					StorageClassName: strPtr("longhorn"),
					AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				},
			},
			want:    true,
			wantWhy: `accessModes "ReadWriteMany" -> "ReadWriteOnce"`,
		},
		{
			name: "matching immutable spec does not recreate",
			existing: &corev1.PersistentVolumeClaim{
				Spec: corev1.PersistentVolumeClaimSpec{
					StorageClassName: strPtr("nvme-1r-gpu"),
					AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				},
			},
			desired: &corev1.PersistentVolumeClaim{
				Spec: corev1.PersistentVolumeClaimSpec{
					StorageClassName: strPtr("nvme-1r-gpu"),
					AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, why := managedPVCNeedsRecreate(tt.existing, tt.desired)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantWhy, why)
		})
	}
}

func TestSourceHash(t *testing.T) {
	tests := []struct {
		name    string
		sourceA string
		sourceB string
		same    bool
	}{
		{
			name:    "identical sources produce same hash",
			sourceA: "oci://registry.harbor.lan/models/qwen3:v1",
			sourceB: "oci://registry.harbor.lan/models/qwen3:v1",
			same:    true,
		},
		{
			name:    "different tags produce different hashes",
			sourceA: "oci://registry.harbor.lan/models/qwen3:v1",
			sourceB: "oci://registry.harbor.lan/models/qwen3:v2",
			same:    false,
		},
		{
			name:    "different registries produce different hashes",
			sourceA: "oci://registry.harbor.lan/models/qwen3:v1",
			sourceB: "oci://ghcr.io/models/qwen3:v1",
			same:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hashA := sourceHash(tt.sourceA)
			hashB := sourceHash(tt.sourceB)
			assert.Len(t, hashA, 16, "hash should be 16 hex chars")
			if tt.same {
				assert.Equal(t, hashA, hashB)
			} else {
				assert.NotEqual(t, hashA, hashB)
			}
		})
	}
}

func strPtr(v string) *string {
	return &v
}

func TestTruncateDigest(t *testing.T) {
	tests := []struct {
		name   string
		digest string
		want   string
	}{
		{
			name:   "full sha256 digest truncated to 12 chars",
			digest: "sha256:abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
			want:   "sha256:abcdef123456",
		},
		{
			name:   "short digest returned with prefix re-added",
			digest: "sha256:abc",
			want:   "abc",
		},
		{
			name:   "empty digest",
			digest: "",
			want:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateDigest(tt.digest)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestOCIDownloadJobHasTerminationLog(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := aiv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}
	r := &ModelCacheReconciler{Scheme: scheme}
	cache := &aiv1alpha1.ModelCache{}
	cache.Name = "test-oci-model"
	cache.Namespace = "default"
	cache.Spec.Source = "oci://registry.harbor.lan/models/test:v1"

	job, err := r.jobForOCIDownload(cache, "test-pvc", "test-model")
	assert.NoError(t, err)

	c := job.Spec.Template.Spec.Containers[0]
	assert.Equal(t, "/dev/termination-log", c.TerminationMessagePath)
	assert.Equal(t, corev1.TerminationMessageReadFile, c.TerminationMessagePolicy)

	script := c.Args[0]
	assert.Contains(t, script, "termination-log", "script should write termination log")
	assert.Contains(t, script, "ociDigest", "script should capture digest in termination log")
}

func TestOCIDownloadJobCacheHitWritesTerminationLog(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := aiv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}
	r := &ModelCacheReconciler{Scheme: scheme}
	cache := &aiv1alpha1.ModelCache{}
	cache.Name = "test-oci-cached"
	cache.Namespace = "default"
	cache.Spec.Source = "oci://registry.harbor.lan/models/test:v1"

	job, err := r.jobForOCIDownload(cache, "test-pvc", "test-model")
	assert.NoError(t, err)

	script := job.Spec.Template.Spec.Containers[0].Args[0]
	assert.Contains(t, script, `"cached":true`, "cache-hit should write cached:true termination log")
}

func TestJobForDownloadCleansStaleDerivedArtifactsBeforeReuse(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := aiv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}

	r := &ModelCacheReconciler{Scheme: scheme}
	cache := &aiv1alpha1.ModelCache{}
	cache.Name = "qwen35-27b-opus-distill-gptq"
	cache.Namespace = "flexinfer-system"
	cache.Spec.Source = "HF://Jackrong/Qwen3.5-27B-Claude-4.6-Opus-Reasoning-Distilled"

	job, err := r.jobForDownload(cache, "qwen35-27b-opus-distill-gptq", "qwen35-27b-opus-distill-gptq")
	if err != nil {
		t.Fatalf("jobForDownload() error = %v", err)
	}

	script := job.Spec.Template.Spec.Containers[0].Args[0]
	assert.Contains(t, script, `"$DEST_DIR/.abliteration-checkpoint.json"`)
	assert.Contains(t, script, `"$DEST_DIR/.quantization-status.json"`)
	assert.Contains(t, script, `find "$DEST_DIR" -maxdepth 1 -type d -name 'gptq-*'`)
	assert.Contains(t, script, `Detected stale abliteration/quantization artifacts in $DEST_DIR`)
	assert.Contains(t, script, `find "$DEST_DIR" -mindepth 1 -maxdepth 1 ! -name '.cache' -exec rm -rf {} +`)
}
