package quantization

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
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
