package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/crb2nu/loom/pkg/openairesponses"
)

type responsesRuntimeDependencies struct {
	Client    openairesponses.ResponsesClient
	Adapter   openairesponses.ToolAdapter
	Executor  openairesponses.ToolExecutor
	Telemetry openairesponses.TelemetrySink
}

type responsesRuntimeFactoryFunc func(ctx context.Context) (responsesRuntimeDependencies, error)

var responsesRuntimeFactory responsesRuntimeFactoryFunc = defaultResponsesRuntimeFactory

func defaultResponsesRuntimeFactory(_ context.Context) (responsesRuntimeDependencies, error) {
	return responsesRuntimeDependencies{}, fmt.Errorf("responses runtime integration is not configured yet")
}

func newResponsesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "responses",
		Short: "OpenAI Responses orchestration (experimental)",
		Long: `Inspect OpenAI Responses orchestration configuration.

This command surfaces feature-gate and loop safety settings for the
opt-in Responses orchestration path planned for Loom Core.`,
	}
	cmd.AddCommand(newResponsesStatusCmd(), newResponsesRunCmd())
	return cmd
}

func newResponsesStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show OpenAI Responses orchestration status",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := openairesponses.LoadConfigFromEnv()
			if err := cfg.Validate(); err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "enabled: %t\n", cfg.Enabled)
			fmt.Fprintf(out, "feature_gate_env: %s\n", openairesponses.FeatureGateEnvVar)
			fmt.Fprintf(out, "request_timeout: %s\n", cfg.RequestTimeout)
			fmt.Fprintf(out, "max_loop_iterations: %d\n", cfg.MaxLoopIterations)
			if !cfg.Enabled {
				fmt.Fprintf(out, "note: set %s=1 to enable the experimental orchestration path\n", openairesponses.FeatureGateEnvVar)
			}
			return nil
		},
	}
}

func newResponsesRunCmd() *cobra.Command {
	var (
		model              string
		input              string
		contextMode        string
		previousResponseID string
		conversationID     string
		agentID            string
		sessionID          string
		namespace          string
		timeoutOverride    time.Duration
		maxLoopOverride    int
	)

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run one non-stream Responses orchestration turn (experimental)",
		Long:  "Execute a gated OpenAI Responses orchestration turn via the pkg/openairesponses loop.",
		RunE: func(cmd *cobra.Command, args []string) error {
			model = strings.TrimSpace(model)
			if model == "" {
				return fmt.Errorf("model is required")
			}

			cfg := openairesponses.LoadConfigFromEnv()
			if timeoutOverride > 0 {
				cfg.RequestTimeout = timeoutOverride
			}
			if maxLoopOverride > 0 {
				cfg.MaxLoopIterations = maxLoopOverride
			}
			if err := cfg.RequireEnabled(); err != nil {
				return err
			}
			if err := cfg.Validate(); err != nil {
				return err
			}

			mode, err := openairesponses.ParseContextMode(contextMode)
			if err != nil {
				return err
			}

			req := openairesponses.TurnRequest{
				Model: model,
				Input: input,
				Context: openairesponses.ContextStrategy{
					Mode:               mode,
					PreviousResponseID: strings.TrimSpace(previousResponseID),
					ConversationID:     strings.TrimSpace(conversationID),
				},
			}

			deps, err := responsesRuntimeFactory(cmd.Context())
			if err != nil {
				return err
			}

			orch := openairesponses.Orchestrator{
				Config:    cfg,
				Client:    deps.Client,
				Adapter:   deps.Adapter,
				Executor:  deps.Executor,
				Telemetry: deps.Telemetry,
			}

			runCtx, cancel := context.WithTimeout(cmd.Context(), cfg.RequestTimeout)
			defer cancel()

			res, err := orch.Run(runCtx, req, openairesponses.ExecutionIdentity{
				AgentID:   strings.TrimSpace(agentID),
				SessionID: strings.TrimSpace(sessionID),
				Namespace: strings.TrimSpace(namespace),
			})
			if err != nil {
				return err
			}

			payload := map[string]any{
				"iterations":         res.Iterations,
				"response_id":        res.Final.ResponseID,
				"conversation_id":    res.Final.ConversationID,
				"terminal":           res.Final.Terminal,
				"output_text":        res.Final.OutputText,
				"tool_results_count": len(res.ToolResults),
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(payload)
		},
	}

	cmd.Flags().StringVar(&model, "model", "", "Responses model name (required)")
	cmd.Flags().StringVar(&input, "input", "", "Input prompt or payload")
	cmd.Flags().StringVar(&contextMode, "context-mode", string(openairesponses.ContextModeChain), "Context mode: chain|conversation|stateless")
	cmd.Flags().StringVar(&previousResponseID, "previous-response-id", "", "Previous response ID for chain mode")
	cmd.Flags().StringVar(&conversationID, "conversation-id", "", "Conversation ID for conversation mode")
	cmd.Flags().StringVar(&agentID, "agent-id", "", "Agent identity for policy/audit attribution")
	cmd.Flags().StringVar(&sessionID, "session-id", "", "Session identity for policy/audit attribution")
	cmd.Flags().StringVar(&namespace, "namespace", "", "Namespace identity for policy/audit attribution")
	cmd.Flags().DurationVar(&timeoutOverride, "timeout", 0, "Override request timeout (e.g., 30s)")
	cmd.Flags().IntVar(&maxLoopOverride, "max-loop-iterations", 0, "Override max loop iterations (>0)")
	_ = cmd.MarkFlagRequired("model")
	return cmd
}
