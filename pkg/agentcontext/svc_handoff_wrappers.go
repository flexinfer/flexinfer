package agentcontext

import (
	"context"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

func (s *Service) HandleHandoffCreate(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.handoffs.HandleHandoffCreate(ctx, args)
}

func (s *Service) HandleHandoffAccept(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.handoffs.HandleHandoffAccept(ctx, args)
}

func (s *Service) HandleHandoffInbox(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.handoffs.HandleHandoffInbox(ctx, args)
}

func (s *Service) HandleHandoffReject(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.handoffs.HandleHandoffReject(ctx, args)
}
