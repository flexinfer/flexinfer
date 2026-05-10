package pipeline

import (
	"strings"
	"unicode"

	"github.com/crb2nu/loom/pkg/mills/store"
)

const (
	defaultSourceBranchPrefix      = "feat/"
	defaultSliceBranchPrefix       = "feat/"
	defaultIntegrationBranchPrefix = "integrate/"
)

// RunBranchContract names the git branches tied to one Mills run.
type RunBranchContract struct {
	SourceBranch      string
	SliceBranch       string
	IntegrationBranch string
}

// BranchContractFor returns the canonical branch names for a run/item/slice.
func BranchContractFor(run *store.PipelineRun, item *store.BacklogItem, stage Stage, sliceName string) RunBranchContract {
	_ = stage
	if item == nil || item.ID == "" {
		return RunBranchContract{}
	}
	backlog := sanitizeBranchComponent(item.ID)
	if backlog == "" {
		return RunBranchContract{}
	}

	contract := RunBranchContract{
		SourceBranch:      SourceBranchName(item.ID),
		IntegrationBranch: IntegrationBranchName(item.ID),
	}
	if sliceName == "" && run != nil && run.ParentSessionID != "" && len(item.Slices) == 1 {
		sliceName = item.Slices[0].Name
	}
	if sliceName != "" {
		contract.SliceBranch = SliceBranchName(item.ID, sliceName)
		contract.SourceBranch = contract.SliceBranch
	}
	if ShouldFanOut(item) {
		contract.SourceBranch = contract.IntegrationBranch
	}
	return contract
}

// SourceBranchName is the straight-through branch for a backlog item.
func SourceBranchName(backlogID string) string {
	backlog := sanitizeBranchComponent(backlogID)
	if backlog == "" {
		return ""
	}
	return defaultSourceBranchPrefix + backlog
}

// SliceBranchName is the deterministic branch for one fan-out slice.
func SliceBranchName(backlogID, sliceName string) string {
	backlog := sanitizeBranchComponent(backlogID)
	slice := sanitizeBranchComponent(sliceName)
	if backlog == "" || slice == "" {
		return ""
	}
	return defaultSliceBranchPrefix + backlog + "/" + slice
}

// IntegrationBranchName is the fan-out parent branch.
func IntegrationBranchName(backlogID string) string {
	backlog := sanitizeBranchComponent(backlogID)
	if backlog == "" {
		return ""
	}
	return defaultIntegrationBranchPrefix + backlog
}

func sanitizeBranchComponent(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r), r == '.', r == '_':
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '/':
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), ".-_/")
	if out == "" {
		return "x"
	}
	return out
}
