package main

import (
	"flag"
	"testing"

	"github.com/stretchr/testify/assert"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
)

func TestSchemeRegistration(t *testing.T) {
	// Verify that all required API types are registered in the scheme.
	// This catches issues where a new CRD version is added but not registered.
	assert.True(t, scheme.IsGroupRegistered("ai.flexinfer"))

	// v1alpha1 types
	gvk := aiv1alpha1.GroupVersion.WithKind("ModelDeployment")
	_, err := scheme.New(gvk)
	assert.NoError(t, err, "ModelDeployment should be registered in scheme")

	gvk = aiv1alpha1.GroupVersion.WithKind("GPUGroup")
	_, err = scheme.New(gvk)
	assert.NoError(t, err, "GPUGroup should be registered in scheme")

	gvk = aiv1alpha1.GroupVersion.WithKind("ModelCache")
	_, err = scheme.New(gvk)
	assert.NoError(t, err, "ModelCache should be registered in scheme")

	// v1alpha2 types
	gvk = aiv1alpha2.GroupVersion.WithKind("Model")
	_, err = scheme.New(gvk)
	assert.NoError(t, err, "Model (v1alpha2) should be registered in scheme")

	// Core k8s types
	_ = clientgoscheme.AddToScheme(scheme)
}

func TestManagerFlags_Defaults(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	metricsAddr := fs.String("metrics-bind-address", ":8080", "")
	probeAddr := fs.String("health-probe-bind-address", ":8081", "")
	leaderElect := fs.Bool("leader-elect", false, "")

	err := fs.Parse([]string{})
	assert.NoError(t, err)
	assert.Equal(t, ":8080", *metricsAddr)
	assert.Equal(t, ":8081", *probeAddr)
	assert.False(t, *leaderElect)
}

func TestManagerFlags_LeaderElection(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	leaderElect := fs.Bool("leader-elect", false, "")

	err := fs.Parse([]string{"-leader-elect"})
	assert.NoError(t, err)
	assert.True(t, *leaderElect)
}
