package agentcontext

import (
	"context"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

// Context handlers — thin delegation to ContextSvc.

func (s *Service) HandleContextAdd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.ctxSvc.Add(ctx, args)
}

func (s *Service) HandleContextGet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.ctxSvc.Get(ctx, args)
}

func (s *Service) HandleContextDelete(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.ctxSvc.Delete(ctx, args)
}

func (s *Service) HandleContextSearch(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.ctxSvc.Search(ctx, args)
}

func (s *Service) HandleContextRecall(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.HandleDeprecatedContextRecall(ctx, args)
}

func (s *Service) HandleContextShare(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.ctxSvc.Share(ctx, args)
}

func (s *Service) HandleContextQueryShared(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.ctxSvc.QueryShared(ctx, args)
}

func (s *Service) HandleContextSummarize(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.ctxSvc.Summarize(ctx, args)
}

func (s *Service) HandleContextStats(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.ctxSvc.Stats(ctx, args)
}

func (s *Service) HandleContextLinkCodebase(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.ctxSvc.LinkCodebase(ctx, args)
}

func (s *Service) HandleEnhancedRecall(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.HandleDeprecatedEnhancedRecall(ctx, args)
}

func (s *Service) HandleAnnotationAdd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.ctxSvc.AnnotationAdd(ctx, args)
}

func (s *Service) HandleAnnotationsGet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.ctxSvc.AnnotationsGet(ctx, args)
}
