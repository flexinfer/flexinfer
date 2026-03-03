package agentcontext

import (
	"context"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

func (s *Service) HandleTemplateCreate(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.templates.HandleTemplateCreate(ctx, args)
}

func (s *Service) HandleTemplateList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.templates.HandleTemplateList(ctx, args)
}
