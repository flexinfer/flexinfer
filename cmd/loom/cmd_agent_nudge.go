package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

func nudgeQueueStatusWithHUD(port, agentID string) (json.RawMessage, error) {
	path, err := bridge.NudgeQueueStatusPath(agentID)
	if err != nil {
		return nil, err
	}
	return hudGet(port, path)
}

func nudgeQueuePolicyWithHUD(port string) (json.RawMessage, error) {
	return hudGet(port, bridge.AgentNudgeQueuePolicyPath)
}

func nudgeQueuePolicyUpdateWithHUD(port string, body bridge.NudgeQueuePolicyMutation, adminToken string) (json.RawMessage, error) {
	headers := map[string]string{
		"Authorization": "Bearer " + adminToken,
	}
	return hudPostWithHeaders(port, bridge.AgentNudgeQueuePolicyPath, body, headers)
}

// newAgentNudgeQueueStatusCmd creates the `loom agent nudge-queue` command.
func newAgentNudgeQueueStatusCmd() *cobra.Command {
	var (
		agentID string
		quiet   bool
	)

	cmd := &cobra.Command{
		Use:   "nudge-queue",
		Short: "Show queued nudge status for an agent",
		Long:  `Return lane counts, dropped counters, and runtime queue settings for a specific agent.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(agentID) == "" {
				return fmt.Errorf("agent-id is required")
			}

			port := resolvePort(cmd)
			result, err := nudgeQueueStatusWithHUD(port, agentID)
			if err != nil {
				if quiet {
					return nil
				}
				return err
			}

			if !quiet {
				fmt.Println(string(result))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&agentID, "agent-id", "", "Agent identifier")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Suppress output (for hooks)")

	return cmd
}

// newAgentNudgeQueuePolicyCmd creates the `loom agent nudge-queue-policy` command.
func newAgentNudgeQueuePolicyCmd() *cobra.Command {
	var (
		capValue     int
		debounceMs   int
		dropPolicy   string
		lanePriority string
		updatedBy    string
		adminToken   string
		quiet        bool
	)

	cmd := &cobra.Command{
		Use:   "nudge-queue-policy",
		Short: "Get or update runtime nudge queue policy",
		Long: `Get current policy when no mutation flags are set.

Update policy at runtime by passing one or more of:
--cap, --debounce-ms, --drop-policy, --lane-priority.

Mutations require --admin-token or $LOOM_HUD_ADMIN_TOKEN (fallback: $HUD_ADMIN_TOKEN).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			port := resolvePort(cmd)

			mutation := bridge.NudgeQueuePolicyMutation{}
			if capValue >= 0 {
				v := capValue
				mutation.Cap = &v
			}
			if debounceMs >= 0 {
				v := debounceMs
				mutation.DebounceMs = &v
			}
			if strings.TrimSpace(dropPolicy) != "" {
				v := strings.TrimSpace(dropPolicy)
				mutation.DropPolicy = &v
			}
			if strings.TrimSpace(lanePriority) != "" {
				lanes, err := bridge.ParseLanePriorityCSV(lanePriority)
				if err != nil {
					return err
				}
				mutation.LanePriority = lanes
			}
			if strings.TrimSpace(updatedBy) != "" {
				mutation.UpdatedBy = strings.TrimSpace(updatedBy)
			}
			mutation = mutation.Normalize()

			hasMutation := mutation.HasMutation()

			if !hasMutation {
				result, err := nudgeQueuePolicyWithHUD(port)
				if err != nil {
					if quiet {
						return nil
					}
					return err
				}
				if !quiet {
					fmt.Println(string(result))
				}
				return nil
			}

			token := strings.TrimSpace(adminToken)
			if token == "" {
				token = strings.TrimSpace(os.Getenv("LOOM_HUD_ADMIN_TOKEN"))
			}
			if token == "" {
				token = strings.TrimSpace(os.Getenv("HUD_ADMIN_TOKEN"))
			}
			if token == "" {
				return fmt.Errorf("admin token is required for policy updates (--admin-token, LOOM_HUD_ADMIN_TOKEN, or HUD_ADMIN_TOKEN)")
			}
			if err := mutation.Validate(); err != nil {
				return err
			}

			result, err := nudgeQueuePolicyUpdateWithHUD(port, mutation, token)
			if err != nil {
				if quiet {
					return nil
				}
				return err
			}
			if !quiet {
				fmt.Println(string(result))
			}
			return nil
		},
	}

	cmd.Flags().IntVar(&capValue, "cap", -1, "Queue cap (set to update)")
	cmd.Flags().IntVar(&debounceMs, "debounce-ms", -1, "Debounce duration in milliseconds (set to update)")
	cmd.Flags().StringVar(&dropPolicy, "drop-policy", "", "Drop policy: drop_old, drop_new, summarize")
	cmd.Flags().StringVar(&lanePriority, "lane-priority", "", "Comma-separated lane order (for example: control,handoff,advice,default)")
	cmd.Flags().StringVar(&updatedBy, "updated-by", "", "Actor label for audit trail")
	cmd.Flags().StringVar(&adminToken, "admin-token", "", "Admin token for protected mutations (falls back to LOOM_HUD_ADMIN_TOKEN, then HUD_ADMIN_TOKEN)")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Suppress output (for hooks)")

	return cmd
}
