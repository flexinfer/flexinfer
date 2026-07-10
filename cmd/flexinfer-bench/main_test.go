package main

import (
	"flag"
	"testing"
	"time"

	"github.com/flexinfer/flexinfer/pkg/gauntlet"
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
	gauntletAPI := fs.String("gauntlet-api", string(gauntlet.ProbeAPIChat), "")

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
	assert.Equal(t, "chat", *gauntletAPI)
}

func TestGauntletProbeURL(t *testing.T) {
	tests := []struct {
		name string
		api  gauntlet.ProbeAPI
		want string
	}{
		{name: "chat default", api: gauntlet.ProbeAPIChat, want: "http://proxy/model/gemma/v1/chat/completions"},
		{name: "raw completions", api: gauntlet.ProbeAPICompletions, want: "http://proxy/model/gemma/v1/completions"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := gauntletProbeURL("http://proxy/", "gemma", tt.api)
			if err != nil {
				t.Fatalf("gauntletProbeURL: %v", err)
			}
			if got != tt.want {
				t.Fatalf("url = %q, want %q", got, tt.want)
			}
		})
	}

	if _, err := gauntletProbeURL("http://proxy", "gemma", gauntlet.ProbeAPI("responses")); err == nil {
		t.Fatal("gauntletProbeURL accepted unsupported API")
	}
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
