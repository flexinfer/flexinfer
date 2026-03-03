package agentcontext

import "context"

func (s *Service) HandleCheckStale(ctx context.Context, args map[string]any) (map[string]any, error) {
	return s.sourceVersion.HandleCheckStale(ctx, args)
}

func (s *Service) HandleRefreshStale(ctx context.Context, args map[string]any) (map[string]any, error) {
	return s.sourceVersion.HandleRefreshStale(ctx, args)
}

func (s *Service) HandleAskSource(ctx context.Context, args map[string]any) (map[string]any, error) {
	return s.sourceVersion.HandleAskSource(ctx, args)
}
