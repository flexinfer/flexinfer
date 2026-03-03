package agentcontext

import (
	"context"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

func (s *Service) SetToolExecutor(executor ToolExecutor) {
	s.workflow.SetToolExecutor(executor)
}

func (s *Service) GetWorkflowEngine() *WorkflowEngine {
	return s.workflow.GetWorkflowEngine()
}

func (s *Service) HandleWorkflowDefine(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.workflow.HandleWorkflowDefine(ctx, args)
}

func (s *Service) HandleWorkflowStart(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.workflow.HandleWorkflowStart(ctx, args)
}

func (s *Service) HandleWorkflowStatus(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.workflow.HandleWorkflowStatus(ctx, args)
}

func (s *Service) HandleWorkflowList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.workflow.HandleWorkflowList(ctx, args)
}

func (s *Service) HandleWorkflowApprove(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.workflow.HandleWorkflowApprove(ctx, args)
}

func (s *Service) HandleWorkflowReject(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.workflow.HandleWorkflowReject(ctx, args)
}

func (s *Service) HandleWorkflowCancel(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.workflow.HandleWorkflowCancel(ctx, args)
}

func (s *Service) HandleWorkflowEvents(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.workflow.HandleWorkflowEvents(ctx, args)
}

func (s *Service) HandleWorkflowDefinitionList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.workflow.HandleWorkflowDefinitionList(ctx, args)
}
