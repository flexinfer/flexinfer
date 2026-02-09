package main

import (
	"flag"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestAgentFlags_Defaults(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	interval := fs.Duration("interval", 30*time.Second, "")
	metricsPort := fs.Int("metrics-port", 9100, "")
	labelPrefix := fs.String("label-prefix", "flexinfer.ai/", "")

	err := fs.Parse([]string{})
	assert.NoError(t, err)
	assert.Equal(t, 30*time.Second, *interval)
	assert.Equal(t, 9100, *metricsPort)
	assert.Equal(t, "flexinfer.ai/", *labelPrefix)
}

func TestAgentFlags_CustomInterval(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	interval := fs.Duration("interval", 30*time.Second, "")

	err := fs.Parse([]string{"-interval", "1m"})
	assert.NoError(t, err)
	assert.Equal(t, 1*time.Minute, *interval)
}

func TestAgentFlags_CustomLabelPrefix(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	labelPrefix := fs.String("label-prefix", "flexinfer.ai/", "")

	err := fs.Parse([]string{"-label-prefix", "custom.io/"})
	assert.NoError(t, err)
	assert.Equal(t, "custom.io/", *labelPrefix)
}
