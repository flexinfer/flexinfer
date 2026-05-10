package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/mills/pipeline"
)

type capabilityStatus string

const (
	capabilityGreen  capabilityStatus = "green"
	capabilityYellow capabilityStatus = "yellow"
	capabilityRed    capabilityStatus = "red"
)

type capabilityMode string

const (
	capabilityModeReal          capabilityMode = "real"
	capabilityModeStub          capabilityMode = "stub"
	capabilityModeDisabled      capabilityMode = "disabled"
	capabilityModeNotConfigured capabilityMode = "not_configured"
)

type capabilityID string

const (
	capabilitySQLiteStore           capabilityID = "sqlite_store"
	capabilityPolicyLoaded          capabilityID = "policy_loaded"
	capabilityAdminAuth             capabilityID = "admin_auth"
	capabilityRepoRoot              capabilityID = "repo_root"
	capabilityFlexInfer             capabilityID = "flexinfer"
	capabilityGitLab                capabilityID = "gitlab"
	capabilityHUDSpawn              capabilityID = "hud_spawn"
	capabilityMCPHubSession         capabilityID = "mcp_hub_session"
	capabilityDispatcherWriteStages capabilityID = "dispatcher_write_stages"
	capabilityCouncilParticipants   capabilityID = "council_participants"
	capabilityBranchContract        capabilityID = "branch_contract"
	capabilityKPIWriter             capabilityID = "kpi_writer"
)

type capabilityDefinition struct {
	ID                  capabilityID
	RequiredForAutonomy bool
	ConfigKey           string
}

var capabilityDefinitions = []capabilityDefinition{
	{ID: capabilitySQLiteStore, RequiredForAutonomy: true, ConfigKey: "LOOM_MILLS_DB_PATH"},
	{ID: capabilityPolicyLoaded, RequiredForAutonomy: true, ConfigKey: "LOOM_MILLS_POLICY_PATH"},
	{ID: capabilityAdminAuth, RequiredForAutonomy: true, ConfigKey: adminTokenEnv},
	{ID: capabilityRepoRoot, RequiredForAutonomy: true, ConfigKey: "LOOM_MILLS_REPO_ROOT"},
	{ID: capabilityFlexInfer, RequiredForAutonomy: true, ConfigKey: "FLEXINFER_PROXY_URL"},
	{ID: capabilityGitLab, RequiredForAutonomy: true, ConfigKey: "GITLAB_API_URL/GITLAB_TOKEN/GITLAB_PROJECT"},
	{ID: capabilityHUDSpawn, RequiredForAutonomy: true, ConfigKey: "LOOM_HUD_URL/LOOM_HUD_TOKEN"},
	{ID: capabilityMCPHubSession, RequiredForAutonomy: true, ConfigKey: "LOOM_MCP_HUB_URL"},
	{ID: capabilityDispatcherWriteStages, RequiredForAutonomy: true},
	{ID: capabilityCouncilParticipants, RequiredForAutonomy: true, ConfigKey: "council.ensemble"},
	{ID: capabilityBranchContract, RequiredForAutonomy: true},
	{ID: capabilityKPIWriter, RequiredForAutonomy: false},
}

type capabilityRow struct {
	ID                  string `json:"id"`
	Status              string `json:"status"`
	Mode                string `json:"mode"`
	RequiredForAutonomy bool   `json:"required_for_autonomy"`
	LastCheckedAt       string `json:"last_checked_at"`
	Message             string `json:"message"`
	Source              string `json:"source,omitempty"`
	ConfigKey           string `json:"config_key,omitempty"`
}

type capabilityReport struct {
	AutonomyReady    bool            `json:"autonomy_ready"`
	AutonomyBlockers []string        `json:"autonomy_blockers"`
	Capabilities     []capabilityRow `json:"capabilities"`
	CheckedAt        string          `json:"checked_at"`
}

type capabilityWiring struct {
	Config                Config
	FlexInferConfigured   bool
	FlexInferReady        bool
	GitLabConfigured      bool
	GitLabReady           bool
	HUDSpawnConfigured    bool
	HUDSpawnReady         bool
	MCPHubConfigured      bool
	MCPHubSessionReady    bool
	DispatcherRealStages  map[string]bool
	CouncilConfigured     bool
	CouncilUsesFakeAgents bool
	BranchContractReady   bool
	BranchContractSource  string
	KPIWriterReady        bool
	KPIWriterSource       string
}

func newCapabilityWiring(cfg Config) capabilityWiring {
	return capabilityWiring{
		Config: cfg,
		DispatcherRealStages: map[string]bool{
			"plan_slice":     false,
			"research":       false,
			"implement":      false,
			"tests":          false,
			"pr_self_review": false,
			"mr":             false,
			"ci_watch":       false,
			"merge":          false,
			"cleanup":        false,
		},
	}
}

func (w capabilityWiring) realStageCount() (int, int, []string) {
	total := 0
	var stubbed []string
	for _, stage := range pipeline.DefaultStages {
		if stage.Type == "auto_gate" {
			continue
		}
		total++
		if !w.DispatcherRealStages[stage.ID] {
			stubbed = append(stubbed, stage.ID)
		}
	}
	sort.Strings(stubbed)
	return total - len(stubbed), total, stubbed
}

func (o *operator) setCapabilities(w capabilityWiring) {
	o.capabilities = w
}

func (o *operator) capabilityReport(ctx context.Context) capabilityReport {
	now := time.Now().UTC().Format(time.RFC3339)
	w := o.capabilities
	policyEnabled := false
	policyVersion := 0
	if o.policy != nil {
		p := o.policy.Current()
		if p != nil {
			policyEnabled = p.IsEnabled()
			policyVersion = p.Version
		}
	}

	rows := make([]capabilityRow, 0, len(capabilityDefinitions))
	for _, def := range capabilityDefinitions {
		row := capabilityRow{
			ID:                  string(def.ID),
			RequiredForAutonomy: def.RequiredForAutonomy,
			LastCheckedAt:       now,
			ConfigKey:           def.ConfigKey,
		}
		switch def.ID {
		case capabilitySQLiteStore:
			if o.dbOK(ctx) {
				row.Status = string(capabilityGreen)
				row.Mode = string(capabilityModeReal)
				row.Message = "canonical SQLite store is reachable"
				row.Source = w.Config.DBPath
			} else {
				row.Status = string(capabilityRed)
				row.Mode = string(capabilityModeNotConfigured)
				row.Message = "canonical SQLite store is unreachable"
				row.Source = w.Config.DBPath
			}
		case capabilityPolicyLoaded:
			if o.policy == nil || o.policy.Current() == nil {
				row.Status = string(capabilityRed)
				row.Mode = string(capabilityModeNotConfigured)
				row.Message = "policy manager is not loaded"
			} else {
				row.Status = string(capabilityGreen)
				row.Mode = string(capabilityModeReal)
				row.Message = fmt.Sprintf("policy loaded (version %d, enabled=%t)", policyVersion, policyEnabled)
				row.Source = w.Config.PolicyPath
			}
		case capabilityAdminAuth:
			if currentAdminToken() != "" {
				row.Status = string(capabilityGreen)
				row.Mode = string(capabilityModeReal)
				row.Message = "admin token configured; mutating endpoints require bearer auth"
			} else {
				row.Status = string(capabilityRed)
				row.Mode = string(capabilityModeNotConfigured)
				row.Message = "admin token is not configured; mutating endpoints fail closed"
			}
		case capabilityRepoRoot:
			row = checkRepoRootCapability(row, w.Config.RepoRoot)
		case capabilityFlexInfer:
			row = configuredReadyRow(row, w.FlexInferConfigured, w.FlexInferReady, "FlexInfer client is configured", "FlexInfer proxy is not configured", w.Config.FlexInferProxyURL)
		case capabilityGitLab:
			row = configuredReadyRow(row, w.GitLabConfigured, w.GitLabReady, "GitLab client is configured", "GitLab client config is incomplete", w.Config.GitLabAPIURL)
		case capabilityHUDSpawn:
			row = configuredReadyRow(row, w.HUDSpawnConfigured, w.HUDSpawnReady, "HUD spawn client is configured", "HUD spawn config is incomplete", w.Config.HUDBaseURL)
		case capabilityMCPHubSession:
			if w.MCPHubSessionReady {
				row.Status = string(capabilityGreen)
				row.Mode = string(capabilityModeReal)
				row.Message = "MCP hub and operator agent-context session are configured"
			} else if w.MCPHubConfigured {
				row.Status = string(capabilityRed)
				row.Mode = string(capabilityModeNotConfigured)
				row.Message = "MCP hub is configured but the operator session is unavailable"
			} else {
				row.Status = string(capabilityRed)
				row.Mode = string(capabilityModeNotConfigured)
				row.Message = "MCP hub is not configured"
			}
		case capabilityDispatcherWriteStages:
			real, total, stubbed := w.realStageCount()
			if len(stubbed) == 0 {
				row.Status = string(capabilityGreen)
				row.Mode = string(capabilityModeReal)
				row.Message = fmt.Sprintf("all %d write stages use real workers", total)
			} else {
				row.Status = string(capabilityRed)
				row.Mode = string(capabilityModeStub)
				row.Message = fmt.Sprintf("%d/%d write stages use real workers; stubbed stages: %s", real, total, strings.Join(stubbed, ", "))
			}
		case capabilityCouncilParticipants:
			switch {
			case !w.CouncilConfigured:
				row.Status = string(capabilityRed)
				row.Mode = string(capabilityModeNotConfigured)
				row.Message = "council ensemble has no configured reviewers"
			case w.CouncilUsesFakeAgents:
				row.Status = string(capabilityRed)
				row.Mode = string(capabilityModeStub)
				row.Message = "council uses FakeReviewer/FakeEditor/FakeLLMJudge participants"
			default:
				row.Status = string(capabilityGreen)
				row.Mode = string(capabilityModeReal)
				row.Message = "council participants use real agent backends"
			}
		case capabilityBranchContract:
			if w.BranchContractReady {
				row.Status = string(capabilityGreen)
				row.Mode = string(capabilityModeReal)
				row.Message = "branch contract enforcement is configured"
				row.Source = w.BranchContractSource
			} else {
				row.Status = string(capabilityRed)
				row.Mode = string(capabilityModeNotConfigured)
				row.Message = "branch contract enforcement is not configured"
			}
		case capabilityKPIWriter:
			if w.KPIWriterReady {
				row.Status = string(capabilityGreen)
				row.Mode = string(capabilityModeReal)
				row.Message = "KPI writer is configured"
				row.Source = w.KPIWriterSource
			} else {
				row.Status = string(capabilityYellow)
				row.Mode = string(capabilityModeDisabled)
				row.Message = "KPI writer is not wired yet; autonomy readiness does not depend on this row"
			}
		}
		rows = append(rows, row)
	}

	blockers := autonomyBlockers(policyEnabled, rows)
	if !policyEnabled {
		blockers = append([]string{"policy.enabled=false"}, blockers...)
	}
	return capabilityReport{
		AutonomyReady:    policyEnabled && len(blockers) == 0,
		AutonomyBlockers: blockers,
		Capabilities:     rows,
		CheckedAt:        now,
	}
}

func configuredReadyRow(row capabilityRow, configured, ready bool, readyMessage, missingMessage, source string) capabilityRow {
	switch {
	case ready:
		row.Status = string(capabilityGreen)
		row.Mode = string(capabilityModeReal)
		row.Message = readyMessage
		row.Source = source
	case configured:
		row.Status = string(capabilityRed)
		row.Mode = string(capabilityModeNotConfigured)
		row.Message = "configured but client initialization failed"
		row.Source = source
	default:
		row.Status = string(capabilityRed)
		row.Mode = string(capabilityModeNotConfigured)
		row.Message = missingMessage
	}
	return row
}

func checkRepoRootCapability(row capabilityRow, repoRoot string) capabilityRow {
	row.Source = repoRoot
	if strings.TrimSpace(repoRoot) == "" {
		row.Status = string(capabilityRed)
		row.Mode = string(capabilityModeNotConfigured)
		row.Message = "repo root is not configured"
		return row
	}
	if _, err := os.Stat(filepath.Join(repoRoot, ".git")); err != nil {
		row.Status = string(capabilityRed)
		row.Mode = string(capabilityModeNotConfigured)
		row.Message = "git checkout metadata is missing under repo root"
		return row
	}
	loomDir := filepath.Join(repoRoot, ".loom")
	info, err := os.Stat(loomDir)
	if err != nil {
		row.Status = string(capabilityRed)
		row.Mode = string(capabilityModeNotConfigured)
		row.Message = ".loom directory is missing under repo root"
		return row
	}
	if !info.IsDir() {
		row.Status = string(capabilityRed)
		row.Mode = string(capabilityModeNotConfigured)
		row.Message = ".loom exists but is not a directory"
		return row
	}
	f, err := os.CreateTemp(loomDir, ".capability-check-*")
	if err != nil {
		row.Status = string(capabilityRed)
		row.Mode = string(capabilityModeNotConfigured)
		row.Message = ".loom directory is not writable"
		return row
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	row.Status = string(capabilityGreen)
	row.Mode = string(capabilityModeReal)
	row.Message = "repo root .loom directory exists and is writable"
	return row
}

func autonomyBlockers(policyEnabled bool, rows []capabilityRow) []string {
	if !policyEnabled {
		return nil
	}
	var blockers []string
	for _, row := range rows {
		if !row.RequiredForAutonomy {
			continue
		}
		if row.Status == string(capabilityGreen) && row.Mode == string(capabilityModeReal) {
			continue
		}
		blockers = append(blockers, fmt.Sprintf("%s: %s", row.ID, row.Message))
	}
	sort.Strings(blockers)
	return blockers
}
