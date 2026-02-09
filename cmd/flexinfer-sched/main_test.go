package main

import (
	"flag"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultPort(t *testing.T) {
	// Verify the default port flag value is 8082
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	var port int
	fs.IntVar(&port, "port", 8082, "")
	err := fs.Parse([]string{})
	assert.NoError(t, err)
	assert.Equal(t, 8082, port)
}

func TestCustomPort(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	var port int
	fs.IntVar(&port, "port", 8082, "")
	err := fs.Parse([]string{"-port", "9090"})
	assert.NoError(t, err)
	assert.Equal(t, 9090, port)
}

func TestInvalidPort(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	var port int
	fs.IntVar(&port, "port", 8082, "")
	err := fs.Parse([]string{"-port", "not-a-number"})
	assert.Error(t, err)
}
