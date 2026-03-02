package agentcontext

import (
	"context"
	"fmt"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

// HandleCompactionStatus returns the compaction scheduler status
func (s *Service) HandleCompactionStatus(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if s.compactionScheduler == nil {
		return mcp.JSONResult(map[string]any{
			"ok":      true,
			"enabled": false,
			"message": "compaction scheduler not initialized",
		})
	}

	status := s.compactionScheduler.Status()

	result := map[string]any{
		"ok":          true,
		"running":     status.Running,
		"run_count":   status.RunCount,
		"error_count": status.ErrorCount,
		"config": map[string]any{
			"enabled":        status.Config.Enabled,
			"check_interval": status.Config.CheckInterval.String(),
			"max_items":      status.Config.MaxItemsPerRun,
		},
	}

	if !status.LastRun.IsZero() {
		result["last_run"] = status.LastRun.Format(time.RFC3339)
	}
	if status.NextCheckIn > 0 {
		result["next_check_in"] = status.NextCheckIn.String()
	}
	if status.LastRunStats != nil {
		result["last_run_stats"] = map[string]any{
			"items_processed":  status.LastRunStats.ItemsProcessed,
			"items_compressed": status.LastRunStats.ItemsCompressed,
			"items_demoted":    status.LastRunStats.ItemsDemoted,
			"items_promoted":   status.LastRunStats.ItemsPromoted,
			"items_expired":    status.LastRunStats.ItemsExpired,
			"tokens_saved":     status.LastRunStats.TokensSaved,
			"duration":         status.LastRunStats.Duration.String(),
		}
	}

	// Include task reconciler status if available.
	if s.tasks.reconciler != nil {
		rs := s.tasks.reconciler.LastStats()
		reconciler := map[string]any{
			"enabled":        s.cfg.TaskReconcilerEnabled,
			"check_interval": s.tasks.reconciler.config.CheckInterval.String(),
		}
		if !rs.StartTime.IsZero() {
			reconciler["last_run"] = rs.StartTime.Format(time.RFC3339)
			reconciler["last_stats"] = map[string]any{
				"completed_gcd": rs.CompletedGCd,
				"orphans":       rs.OrphansCleanedUp,
				"unblocked":     rs.Unblocked,
				"stale":         rs.MarkedStale,
				"errors":        rs.Errors,
				"duration":      rs.Duration.String(),
			}
		}
		result["task_reconciler"] = reconciler
	}

	return mcp.JSONResult(result)
}

// HandleReconcileTrigger manually triggers a task reconciliation cycle
func (s *Service) HandleReconcileTrigger(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if s.tasks.reconciler == nil {
		return mcp.ErrorResult(fmt.Errorf("task reconciler not initialized")), nil
	}

	stats, err := s.tasks.reconciler.TriggerReconcile(ctx)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("trigger reconcile: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":        true,
		"gc":        stats.CompletedGCd,
		"orphans":   stats.OrphansCleanedUp,
		"unblocked": stats.Unblocked,
		"stale":     stats.MarkedStale,
		"errors":    stats.Errors,
		"duration":  stats.Duration.String(),
	})
}

// HandleCompactionTrigger manually triggers a compaction cycle
func (s *Service) HandleCompactionTrigger(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if s.compactionScheduler == nil {
		return mcp.ErrorResult(fmt.Errorf("compaction scheduler not initialized")), nil
	}

	stats, err := s.compactionScheduler.TriggerCompaction(ctx)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("trigger compaction: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":               true,
		"items_processed":  stats.ItemsProcessed,
		"items_compressed": stats.ItemsCompressed,
		"items_demoted":    stats.ItemsDemoted,
		"items_promoted":   stats.ItemsPromoted,
		"items_expired":    stats.ItemsExpired,
		"tokens_saved":     stats.TokensSaved,
		"duration":         stats.Duration.String(),
	})
}
