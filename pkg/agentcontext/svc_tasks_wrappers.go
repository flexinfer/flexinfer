package agentcontext

import (
	"context"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

// Task handlers — thin delegation to TaskSvc.

func (s *Service) HandleTaskAdd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.tasks.Add(ctx, args)
}

func (s *Service) HandleTaskUpdate(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.tasks.Update(ctx, args)
}

func (s *Service) HandleTaskList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.tasks.List(ctx, args)
}

func (s *Service) HandleTaskDelete(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.tasks.Delete(ctx, args)
}

// getActiveTasks delegates to TaskSvc.GetActive.
func (s *Service) getActiveTasks(ctx context.Context, agentID, sessionID string, limit int) ([]Task, error) {
	return s.tasks.GetActive(ctx, agentID, sessionID, limit)
}

// markSessionTasksStale delegates to TaskSvc.MarkSessionTasksStale.
func (s *Service) markSessionTasksStale(ctx context.Context, sessionID string) int {
	return s.tasks.MarkSessionTasksStale(ctx, sessionID)
}
