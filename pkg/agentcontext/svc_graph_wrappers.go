package agentcontext

import (
	"context"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

func (s *Service) GetKnowledgeGraph() *KnowledgeGraph {
	return s.graph.GetKnowledgeGraph()
}

func (s *Service) HandleEntityAdd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.graph.HandleEntityAdd(ctx, args)
}

func (s *Service) HandleEntityGet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.graph.HandleEntityGet(ctx, args)
}

func (s *Service) HandleEntityFind(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.graph.HandleEntityFind(ctx, args)
}

func (s *Service) HandleEntityDelete(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.graph.HandleEntityDelete(ctx, args)
}

func (s *Service) HandleRelationAdd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.graph.HandleRelationAdd(ctx, args)
}

func (s *Service) HandleRelationGet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.graph.HandleRelationGet(ctx, args)
}

func (s *Service) HandleRelationDelete(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.graph.HandleRelationDelete(ctx, args)
}

func (s *Service) HandleGraphQuery(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.graph.HandleGraphQuery(ctx, args)
}

func (s *Service) HandleFindPath(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.graph.HandleFindPath(ctx, args)
}

func (s *Service) HandleReasoningChainAdd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.graph.HandleReasoningChainAdd(ctx, args)
}

func (s *Service) HandleReasoningChainGet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.graph.HandleReasoningChainGet(ctx, args)
}

func (s *Service) HandleReasoningChainList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.graph.HandleReasoningChainList(ctx, args)
}

func (s *Service) HandleGraphStats(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.graph.HandleGraphStats(ctx, args)
}
