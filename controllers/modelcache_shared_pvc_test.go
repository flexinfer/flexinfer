package controllers

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
