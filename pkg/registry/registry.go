// Package registry loads and parses the MCP server registry.
//
// NOTE: This package is a thin compatibility layer over fi-mcp-kit so loom-core,
// fi-mcp-kit, and other services share a single registry schema + implementation.
package registry

import kitreg "gitlab.flexinfer.ai/libs/fi-mcp-kit/pkg/registry"

type Config = kitreg.Config

type Registry = kitreg.Registry
type EnvVar = kitreg.EnvVar
type Server = kitreg.Server
type ToolSchema = kitreg.ToolSchema
type InputSchema = kitreg.InputSchema
type TargetSpec = kitreg.TargetSpec
type SSHSpec = kitreg.SSHSpec
type PlatformPermission = kitreg.PlatformPermission

func LoadConfig() (*Config, error)                { return kitreg.LoadConfig() }
func GetRepoRoot(registryPath string) string      { return kitreg.GetRepoRoot(registryPath) }
func Load(path string) (*Registry, error)         { return kitreg.Load(path) }
func FindDefaultPath(workspaceRoot string) string { return kitreg.FindDefaultPath(workspaceRoot) }
func FindRegistry() (string, bool)                { return kitreg.FindRegistry() }
func FindRegistryOrDefault(defaultPath string) string {
	return kitreg.FindRegistryOrDefault(defaultPath)
}
func DefaultEnvAliases() map[string]EnvVar { return kitreg.DefaultEnvAliases() }
