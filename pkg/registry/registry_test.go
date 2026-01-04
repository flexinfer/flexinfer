package registry

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad(t *testing.T) {
	// Create temporary registry file
	content := `
version: 1
servers:
  - name: test-server
    common:
      command: ./test
      args: ["--flag"]
      env:
        TEST_VAR: value
  - name: multi-target
    targets:
      dev:
        command: ./dev
      prod:
        command: ./prod
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "registry.yaml")
	err := os.WriteFile(path, []byte(content), 0644)
	require.NoError(t, err)

	// Load registry
	reg, err := Load(path)
	require.NoError(t, err)

	assert.Equal(t, 1, reg.Version)
	assert.Len(t, reg.Servers, 2)

	// Test server 1
	s1 := reg.Servers[0]
	assert.Equal(t, "test-server", s1.Name)
	assert.NotNil(t, s1.Common)
	assert.Equal(t, "./test", s1.Common.Command)

	// Test server 2
	s2 := reg.Servers[1]
	assert.Equal(t, "multi-target", s2.Name)
	assert.Nil(t, s2.Common)
	assert.Len(t, s2.Targets, 2)
}

func TestGetServerSpec(t *testing.T) {
	reg := &Registry{
		Servers: []*Server{
			{
				Name: "mixed",
				Common: &TargetSpec{
					Command: "common",
					Env:     map[string]string{"COMMON": "1", "OVERRIDE": "1"},
				},
				Targets: map[string]*TargetSpec{
					"dev": {
						Command: "dev",
						Env:     map[string]string{"OVERRIDE": "2", "DEV": "1"},
					},
				},
			},
		},
	}

	// Test dev target (merge)
	// Note: This modifies the underlying map of Common because GetServerSpec does a shallow copy
	spec, err := reg.GetServerSpec("mixed", "dev")
	require.NoError(t, err)
	assert.Equal(t, "dev", spec.Command)
	assert.Equal(t, "2", spec.Env["OVERRIDE"])

	// Reset for next test to avoid side effects of shallow copy modification
	reg.Servers[0].Common.Env = map[string]string{"COMMON": "1", "OVERRIDE": "1"}
	// Overridden
	assert.Equal(t, "1", spec.Env["COMMON"]) // Inherited
	assert.Equal(t, "1", spec.Env["DEV"])    // Added

	// Test other target (fallback to common with target override if present but empty in target)
	// Actually, GetServerSpec logic:
	// 1. spec = *server.Common (copy)
	// 2. mergeSpec(spec, targetSpec)

	// If target "prod" does not exist in server.Targets, we just get common.
	spec, err = reg.GetServerSpec("mixed", "prod")
	require.NoError(t, err)
	assert.Equal(t, "common", spec.Command)
	assert.Equal(t, "1", spec.Env["OVERRIDE"])
}

func TestResolveEnv(t *testing.T) {
	reg := &Registry{
		EnvAliases: map[string]EnvVar{
			"MY_TOKEN": {Fallbacks: []string{"FALLBACK_TOKEN", "LEGACY_TOKEN"}},
		},
	}

	// Case 1: Primary set
	os.Setenv("MY_TOKEN", "primary")
	val, found := reg.ResolveEnv("MY_TOKEN")
	assert.True(t, found)
	assert.Equal(t, "primary", val)
	os.Unsetenv("MY_TOKEN")

	// Case 2: Fallback set
	os.Setenv("FALLBACK_TOKEN", "fallback")
	val, found = reg.ResolveEnv("MY_TOKEN")
	assert.True(t, found)
	assert.Equal(t, "fallback", val)
	os.Unsetenv("FALLBACK_TOKEN")

	// Case 3: None set
	val, found = reg.ResolveEnv("MY_TOKEN")
	assert.False(t, found)
	assert.Empty(t, val)
}

func TestGetStaticTools(t *testing.T) {
	reg := &Registry{
		Servers: []*Server{
			{
				Name: "static-server",
				Common: &TargetSpec{
					Tools: []ToolSchema{
						{
							Name:        "static_tool",
							Description: "A static tool",
							InputSchema: InputSchema{
								Type: "object",
								Properties: map[string]any{
									"arg": map[string]any{"type": "string"},
								},
							},
						},
					},
				},
			},
		},
	}

	tools := reg.GetStaticTools("default")
	require.Len(t, tools, 1)
	assert.Equal(t, "static-server__static_tool", tools[0].Name)
	assert.Equal(t, "A static tool", tools[0].Description)
}
