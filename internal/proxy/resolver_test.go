package proxy

import (
	"context"
	"testing"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ---------------------------------------------------------------------------
// extractModelFromSource (pure function, no Proxy receiver)
// ---------------------------------------------------------------------------

func TestExtractModelFromSource_HF(t *testing.T) {
	got := extractModelFromSource("HF://org/model")
	assert.Equal(t, "org/model", got)
}

func TestExtractModelFromSource_Ollama(t *testing.T) {
	got := extractModelFromSource("ollama://llama3")
	assert.Equal(t, "llama3", got)
}

func TestExtractModelFromSource_File(t *testing.T) {
	got := extractModelFromSource("file:///path/to/model")
	assert.Equal(t, "/path/to/model", got)
}

func TestExtractModelFromSource_PVC_WithSubpath(t *testing.T) {
	got := extractModelFromSource("pvc://my-pvc/subpath")
	assert.Equal(t, "/subpath", got)
}

func TestExtractModelFromSource_PVC_NoSubpath(t *testing.T) {
	got := extractModelFromSource("pvc://my-pvc")
	assert.Equal(t, "", got)
}

func TestExtractModelFromSource_Plain(t *testing.T) {
	got := extractModelFromSource("plain-string")
	assert.Equal(t, "plain-string", got)
}

func TestExtractModelFromSource_Empty(t *testing.T) {
	got := extractModelFromSource("")
	assert.Equal(t, "", got)
}

// ---------------------------------------------------------------------------
// resolveLoRAAdapter (requires fake K8s client via setupTestProxy)
// ---------------------------------------------------------------------------

func TestResolveLoRAAdapter_Found(t *testing.T) {
	p := setupTestProxy(t)
	ctx := context.Background()

	adapter := &aiv1alpha2.LoRAAdapter{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-lora",
			Namespace: "default",
		},
		Spec: aiv1alpha2.LoRAAdapterSpec{
			ModelRef:    "parent-model",
			AdapterName: "lora-finetune",
			Source: aiv1alpha2.LoRAAdapterSource{
				Type: aiv1alpha2.LoRASourceHuggingFace,
				URI:  "org/adapter",
			},
		},
	}
	require.NoError(t, p.client.Create(ctx, adapter))

	parentModel, isLoRA := p.resolveLoRAAdapter(ctx, "lora-finetune")
	assert.True(t, isLoRA)
	assert.Equal(t, "parent-model", parentModel)
}

func TestResolveLoRAAdapter_NotFound(t *testing.T) {
	p := setupTestProxy(t)
	ctx := context.Background()

	parentModel, isLoRA := p.resolveLoRAAdapter(ctx, "no-such-adapter")
	assert.False(t, isLoRA)
	assert.Equal(t, "no-such-adapter", parentModel)
}

func TestResolveLoRAAdapter_MultipleAdapters(t *testing.T) {
	p := setupTestProxy(t)
	ctx := context.Background()

	// Create two adapters that share the same adapterName.
	// The function iterates the list in order, so the first match wins.
	adapter1 := &aiv1alpha2.LoRAAdapter{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "adapter-alpha",
			Namespace: "default",
		},
		Spec: aiv1alpha2.LoRAAdapterSpec{
			ModelRef:    "model-alpha",
			AdapterName: "shared-name",
			Source: aiv1alpha2.LoRAAdapterSource{
				Type: aiv1alpha2.LoRASourceHuggingFace,
				URI:  "org/alpha",
			},
		},
	}
	adapter2 := &aiv1alpha2.LoRAAdapter{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "adapter-beta",
			Namespace: "default",
		},
		Spec: aiv1alpha2.LoRAAdapterSpec{
			ModelRef:    "model-beta",
			AdapterName: "unique-name",
			Source: aiv1alpha2.LoRAAdapterSource{
				Type: aiv1alpha2.LoRASourceLocalPath,
				URI:  "/models/adapters/beta",
			},
		},
	}
	require.NoError(t, p.client.Create(ctx, adapter1))
	require.NoError(t, p.client.Create(ctx, adapter2))

	// Resolve the shared name -- first match should win.
	parentModel, isLoRA := p.resolveLoRAAdapter(ctx, "shared-name")
	assert.True(t, isLoRA)
	assert.Equal(t, "model-alpha", parentModel)

	// Resolve the unique name for the second adapter.
	parentModel, isLoRA = p.resolveLoRAAdapter(ctx, "unique-name")
	assert.True(t, isLoRA)
	assert.Equal(t, "model-beta", parentModel)

	// Unmatched name returns original.
	parentModel, isLoRA = p.resolveLoRAAdapter(ctx, "nonexistent")
	assert.False(t, isLoRA)
	assert.Equal(t, "nonexistent", parentModel)
}
