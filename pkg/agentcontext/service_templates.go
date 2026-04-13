package agentcontext

import (
	"context"
	"fmt"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

// TemplateSvc is deprecated. Templates are CLI-only after SIMP-7.
// Qdrant persistence for templates has been removed.
type TemplateSvc struct{ *Service }

func (s *TemplateSvc) HandleTemplateCreate(_ context.Context, _ map[string]any) (*mcp.CallToolResult, error) {
	return mcp.ErrorResult(fmt.Errorf("template_create is deprecated; use CLI `loom template` instead")), nil
}

func (s *TemplateSvc) HandleTemplateList(_ context.Context, _ map[string]any) (*mcp.CallToolResult, error) {
	return mcp.ErrorResult(fmt.Errorf("template_list is deprecated; use CLI `loom template list` instead")), nil
}
