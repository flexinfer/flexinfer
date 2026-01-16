package generator

import (
	"github.com/crb2nu/loom/pkg/registry"
	kitgen "gitlab.flexinfer.ai/libs/fi-mcp-kit/pkg/generator"
)

type ManifestsOptions = kitgen.ManifestsOptions
type GatewayManifests = kitgen.GatewayManifests

// GenerateManifests generates Kubernetes manifests for the MCP Hub.
func GenerateManifests(reg *registry.Registry, outputDir string, opts ManifestsOptions) error {
	return kitgen.GenerateManifests(reg, outputDir, opts)
}
