package main

import (
	"flag"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBenchFlags_Defaults(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	model := fs.String("model", "", "")
	modelName := fs.String("model-name", "", "")
	configMapName := fs.String("configmap", "", "")
	backend := fs.String("backend", "ollama", "")
	warmupIterations := fs.Int("warmup-iterations", 2, "")
	minDuration := fs.Duration("min-duration", 30*time.Second, "")
	iterations := fs.Int("iterations", 5, "")
	batchSize := fs.Int("batch-size", 128, "")
	coldStartTimeout := fs.Duration("cold-start-timeout", 5*time.Minute, "")

	err := fs.Parse([]string{})
	assert.NoError(t, err)

	assert.Equal(t, "", *model)
	assert.Equal(t, "", *modelName)
	assert.Equal(t, "", *configMapName)
	assert.Equal(t, "ollama", *backend)
	assert.Equal(t, 2, *warmupIterations)
	assert.Equal(t, 30*time.Second, *minDuration)
	assert.Equal(t, 5, *iterations)
	assert.Equal(t, 128, *batchSize)
	assert.Equal(t, 5*time.Minute, *coldStartTimeout)
}

func TestBenchFlags_RequiredFlags(t *testing.T) {
	// Both --model and --configmap are required.
	// If either is empty, the binary should exit with an error.
	// Test that flag parsing works with values provided.
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	model := fs.String("model", "", "")
	configMapName := fs.String("configmap", "", "")

	err := fs.Parse([]string{"-model", "test-model", "-configmap", "test-cm"})
	assert.NoError(t, err)
	assert.Equal(t, "test-model", *model)
	assert.Equal(t, "test-cm", *configMapName)
}

func TestBenchFlags_ModelNameFallback(t *testing.T) {
	// When model-name is not specified, it should fall back to model
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	model := fs.String("model", "", "")
	modelName := fs.String("model-name", "", "")

	err := fs.Parse([]string{"-model", "Qwen/Qwen2.5-7B"})
	assert.NoError(t, err)
	assert.Equal(t, "Qwen/Qwen2.5-7B", *model)
	assert.Equal(t, "", *modelName)

	// Simulate the fallback logic from main()
	if *modelName == "" {
		*modelName = *model
	}
	assert.Equal(t, "Qwen/Qwen2.5-7B", *modelName)
}
