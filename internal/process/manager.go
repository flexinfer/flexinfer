// Package process manages local MCP server processes.
//
// NOTE: This package delegates to fi-mcp-kit so process lifecycle behavior stays
// consistent across loom-core, fi-mcp-kit, and other MCP tooling.
package process

import (
	kitprocess "gitlab.flexinfer.ai/libs/fi-mcp-kit/pkg/process"

	"github.com/crb2nu/loom/pkg/registry"
)

type Process = kitprocess.Process
type ExpandFunc = kitprocess.ExpandFunc
type Manager = kitprocess.Manager
type IdleInfo = kitprocess.IdleInfo

func NewManager(reg *registry.Registry, target string) *Manager {
	return kitprocess.NewManager(reg, target)
}
