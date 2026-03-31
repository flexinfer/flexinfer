package quantization

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

func TestShouldUseInsecure(t *testing.T) {
	tests := []struct {
		name   string
		ociRef string
		want   bool
	}{
		{
			name:   ".lan suffix triggers insecure",
			ociRef: "registry.harbor.lan/models/qwen3:gptq-int4",
			want:   true,
		},
		{
			name:   ".lan suffix with port — host includes port, suffix check does not strip it",
			ociRef: "registry.harbor.lan:5000/models/qwen3:gptq-int4",
			want:   false,
		},
		{
			name:   ".local suffix does not trigger insecure",
			ociRef: "registry.local/models/qwen3:v1",
			want:   false,
		},
		{
			name:   "public registry does not trigger insecure",
			ociRef: "ghcr.io/flexinfer/models/qwen3:v1",
			want:   false,
		},
		{
			name:   "docker.io does not trigger insecure",
			ociRef: "docker.io/library/alpine:3.23",
			want:   false,
		},
		{
			name:   "bare hostname with .lan",
			ociRef: "myregistry.lan/repo:tag",
			want:   true,
		},
		{
			name:   "URL with oci:// scheme and .lan",
			ociRef: "oci://registry.harbor.lan/models/test:v1",
			want:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldUseInsecure(tt.ociRef)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPublishImage(t *testing.T) {
	origVal, origSet := os.LookupEnv("FLEXINFER_PUBLISH_IMAGE")
	defer func() {
		if origSet {
			_ = os.Setenv("FLEXINFER_PUBLISH_IMAGE", origVal)
		} else {
			_ = os.Unsetenv("FLEXINFER_PUBLISH_IMAGE")
		}
	}()

	t.Run("default returns alpine:3.23", func(t *testing.T) {
		_ = os.Unsetenv("FLEXINFER_PUBLISH_IMAGE")
		got := publishImage()
		assert.Equal(t, "alpine:3.23", got)
	})

	t.Run("env override takes precedence", func(t *testing.T) {
		_ = os.Setenv("FLEXINFER_PUBLISH_IMAGE", "custom-publisher:v2")
		got := publishImage()
		assert.Equal(t, "custom-publisher:v2", got)
	})
}

func TestOCIPublishScript(t *testing.T) {
	script := ociPublishScript()
	assert.NotEmpty(t, script)
	assert.Contains(t, script, "oras push")
	assert.Contains(t, script, "--insecure")
	assert.Contains(t, script, "MODEL_DIR")
	assert.Contains(t, script, "OCI_REF")
	assert.Contains(t, script, "OCI_INSECURE")
	assert.Contains(t, script, "termination-log")
}

func TestOCIPublishScriptTagPolicies(t *testing.T) {
	script := ociPublishScript()

	t.Run("supports timestamp tag policy", func(t *testing.T) {
		assert.Contains(t, script, "OCI_TAG_POLICY")
		assert.Contains(t, script, "timestamp")
	})

	t.Run("supports digest-suffix tag policy", func(t *testing.T) {
		assert.Contains(t, script, "digest-suffix")
	})

	t.Run("supports additional tags", func(t *testing.T) {
		assert.Contains(t, script, "OCI_ADDITIONAL_TAGS")
		assert.Contains(t, script, "oras tag")
	})

	t.Run("includes pushedTags in termination log", func(t *testing.T) {
		assert.Contains(t, script, "pushedTags")
	})
}

func TestPublishEnvTagPolicy(t *testing.T) {
	ociRef := "registry.harbor.lan/models/test:v1"

	t.Run("default tag policy is overwrite", func(t *testing.T) {
		spec := &aiv1alpha1.PublishSpec{
			Targets: []aiv1alpha1.PublishTarget{aiv1alpha1.PublishTargetOCI},
			OCIRef:  &ociRef,
		}
		env := publishEnv("/models/test", spec)
		envMap := make(map[string]string)
		for _, e := range env {
			envMap[e.Name] = e.Value
		}
		assert.Equal(t, "overwrite", envMap["OCI_TAG_POLICY"])
		assert.Empty(t, envMap["OCI_ADDITIONAL_TAGS"])
	})

	t.Run("timestamp tag policy propagated", func(t *testing.T) {
		policy := "timestamp"
		spec := &aiv1alpha1.PublishSpec{
			Targets:   []aiv1alpha1.PublishTarget{aiv1alpha1.PublishTargetOCI},
			OCIRef:    &ociRef,
			TagPolicy: &policy,
		}
		env := publishEnv("/models/test", spec)
		envMap := make(map[string]string)
		for _, e := range env {
			envMap[e.Name] = e.Value
		}
		assert.Equal(t, "timestamp", envMap["OCI_TAG_POLICY"])
	})

	t.Run("additional tags joined with comma", func(t *testing.T) {
		spec := &aiv1alpha1.PublishSpec{
			Targets:        []aiv1alpha1.PublishTarget{aiv1alpha1.PublishTargetOCI},
			OCIRef:         &ociRef,
			AdditionalTags: []string{"latest", "stable"},
		}
		env := publishEnv("/models/test", spec)
		envMap := make(map[string]string)
		for _, e := range env {
			envMap[e.Name] = e.Value
		}
		assert.Equal(t, "latest,stable", envMap["OCI_ADDITIONAL_TAGS"])
	})
}
