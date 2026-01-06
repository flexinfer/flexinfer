// Package process manages local MCP server processes.
//
// NOTE: This package delegates to fi-mcp-kit so process lifecycle behavior stays
// consistent across loom-core, fi-mcp-kit, and other MCP tooling.
package process

import (
	"github.com/crb2nu/loom/pkg/registry"
	kitprocess "gitlab.flexinfer.ai/libs/fi-mcp-kit/pkg/process"
)

type Process = kitprocess.Process
type ExpandFunc = kitprocess.ExpandFunc
type Manager = kitprocess.Manager
type IdleInfo = kitprocess.IdleInfo

func NewManager(reg *registry.Registry, target string) *Manager {
	return kitprocess.NewManager(reg, target)
}
