package pipeline

import (
	"testing"

	"github.com/crb2nu/loom/pkg/mills/store"
)

func TestBranchContractFor_StraightThrough(t *testing.T) {
	run := &store.PipelineRun{ID: "PIPE-1", BacklogID: "BL X"}
	item := &store.BacklogItem{ID: "BL X"}
	got := BranchContractFor(run, item, Stage{ID: "implement"}, "")
	if got.SourceBranch != "feat/BL-X" {
		t.Errorf("source branch = %q", got.SourceBranch)
	}
	if got.IntegrationBranch != "integrate/BL-X" {
		t.Errorf("integration branch = %q", got.IntegrationBranch)
	}
}

func TestBranchContractFor_FanOutParentUsesIntegrationBranch(t *testing.T) {
	run := &store.PipelineRun{ID: "PIPE-1", BacklogID: "BL-PAR"}
	item := &store.BacklogItem{
		ID: "BL-PAR",
		Slices: []store.Slice{
			{Name: "api changes", ParallelWith: []string{"ui"}},
			{Name: "ui", ParallelWith: []string{"api changes"}},
		},
	}
	got := BranchContractFor(run, item, Stage{ID: "mr"}, "")
	if got.SourceBranch != "integrate/BL-PAR" {
		t.Errorf("source branch = %q", got.SourceBranch)
	}
}

func TestBranchContractFor_FanOutSliceUsesSliceBranch(t *testing.T) {
	parentID := "PIPE-1"
	run := &store.PipelineRun{ID: "PIPE-1-api", BacklogID: "BL-PAR", ParentSessionID: parentID}
	item := &store.BacklogItem{
		ID:     "BL-PAR",
		Slices: []store.Slice{{Name: "api changes"}},
	}
	got := BranchContractFor(run, item, Stage{ID: "implement"}, "")
	if got.SourceBranch != "feat/BL-PAR/api-changes" {
		t.Errorf("source branch = %q", got.SourceBranch)
	}
	if got.SliceBranch != "feat/BL-PAR/api-changes" {
		t.Errorf("slice branch = %q", got.SliceBranch)
	}
}
