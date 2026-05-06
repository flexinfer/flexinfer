package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRuntimeProfileLabel(t *testing.T) {
	assert.Equal(t, "gfx1100", RuntimeProfileLabel("gfx1100"))
	assert.Equal(t, RuntimeProfileUnknown, RuntimeProfileLabel(""))
	assert.Equal(t, RuntimeProfileUnknown, RuntimeProfileLabel("  "))
}

func TestRuntimeDigestLabel(t *testing.T) {
	const digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	assert.Equal(t, digest, RuntimeDigestLabel("registry.harbor.lan/flexinfer/runtime@"+digest))
	assert.Equal(t, RuntimeDigestUnknown, RuntimeDigestLabel("registry.harbor.lan/flexinfer/runtime:rocm-gfx1100"))
	assert.Equal(t, RuntimeDigestUnknown, RuntimeDigestLabel(""))
	assert.Equal(t, RuntimeDigestUnknown, RuntimeDigestLabel("registry.harbor.lan/flexinfer/runtime@sha256:"))
	assert.Equal(t, RuntimeDigestUnknown, RuntimeDigestLabel("registry.harbor.lan/flexinfer/runtime@not-a-digest"))
}
