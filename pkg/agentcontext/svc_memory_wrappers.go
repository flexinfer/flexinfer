package agentcontext

import (
	"context"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

func (s *Service) GetMemoryHierarchy() *MemoryHierarchy {
	return s.memory.GetMemoryHierarchy()
}

func (s *Service) HandleMemoryAdd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.memory.HandleMemoryAdd(ctx, args)
}

func (s *Service) HandleMemoryGet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.memory.HandleMemoryGet(ctx, args)
}

func (s *Service) HandleMemoryRecall(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.memory.HandleMemoryRecall(ctx, args)
}

func (s *Service) HandleMemoryDelete(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.memory.HandleMemoryDelete(ctx, args)
}

func (s *Service) HandleMemoryPromote(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.memory.HandleMemoryPromote(ctx, args)
}

func (s *Service) HandleMemoryDemote(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.memory.HandleMemoryDemote(ctx, args)
}

func (s *Service) HandleMemoryCompress(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.memory.HandleMemoryCompress(ctx, args)
}

func (s *Service) HandleMemoryMerge(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.memory.HandleMemoryMerge(ctx, args)
}

func (s *Service) HandleMemoryStats(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.memory.HandleMemoryStats(ctx, args)
}

func (s *Service) HandleMemoryPolicyGet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.memory.HandleMemoryPolicyGet(ctx, args)
}

func (s *Service) HandleMemoryPolicySet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.memory.HandleMemoryPolicySet(ctx, args)
}

func (s *Service) HandleMemoryExport(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.memory.HandleMemoryExport(ctx, args)
}

func (s *Service) HandleMemoryImport(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.memory.HandleMemoryImport(ctx, args)
}
