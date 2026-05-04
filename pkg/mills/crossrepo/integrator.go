package crossrepo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/crb2nu/loom/pkg/mills"
	"github.com/crb2nu/loom/pkg/mills/store"
)

// CIStatus is the per-MR pipeline status the integrator polls for.
type CIStatus string

const (
	CIPending  CIStatus = "pending"
	CIRunning  CIStatus = "running"
	CISuccess  CIStatus = "success"
	CIFailed   CIStatus = "failed"
	CICanceled CIStatus = "canceled"
)

// IsTerminal reports whether the status is one the integrator stops
// polling on. Pending + running keep polling; everything else exits.
func (s CIStatus) IsTerminal() bool {
	switch s {
	case CISuccess, CIFailed, CICanceled:
		return true
	default:
		return false
	}
}

// MergeOptions captures the per-repo merge knobs the integrator forwards
// to the GitLab client.
type MergeOptions struct {
	SquashCommitMessage      string
	ShouldRemoveSourceBranch bool
}

// RevertOptions captures the per-repo revert MR knobs. Reason is included
// in the revert MR's title/body so the audit trail names the failure.
type RevertOptions struct {
	Reason string
}

// GitLabClient is the GitLab subset the integrator depends on. The
// production wiring wraps mcp-gitlab; tests supply a fake.
type GitLabClient interface {
	GetMRPipelineStatus(ctx context.Context, projectID int64, mrIID int64) (CIStatus, error)
	MergeMR(ctx context.Context, projectID int64, mrIID int64, opts MergeOptions) error
	OpenRevertMR(ctx context.Context, projectID int64, mergedMRIID int64, opts RevertOptions) (newMRIID int64, err error)
}

// PolicyProvider returns the active mills policy snapshot. Wraps
// mills.PolicyManager.Current in production; tests supply a static fake.
type PolicyProvider interface {
	Snapshot() *mills.Policy
}

// Integrator drives a *store.CrossRepoRun from `state=open` to a terminal
// state (merged | reverted | failed). It does NOT mutate the
// cross_repo_runs.state itself — the caller transitions the row based on
// the returned state to keep the state-machine truth in one place.
type Integrator struct {
	Store        *store.Store
	GitLabClient GitLabClient
	Policy       PolicyProvider
	// Now is injected for tests; default time.Now.
	Now func() time.Time
	// PollInterval is the delay between successive CI status polls.
	// Zero means defaultPollInterval.
	PollInterval time.Duration
	Logger       *slog.Logger
}

// defaultPollInterval is the cadence at which the integrator polls each
// per-repo MR's CI status. 15s is a balance between operator perception
// (cross-repo runs feel "live") and GitLab API quota.
const defaultPollInterval = 15 * time.Second

// defaultPerRepoTimeoutMinutes is the per-repo CI timeout when the policy
// snapshot omits the field. Mirrors the spec default.
const defaultPerRepoTimeoutMinutes = 60

// WaitForGreen polls every repo's CI until all reach CISuccess (returns
// CrossRepoGatesGreen) or any per-repo timeout fires (returns
// CrossRepoFailed). Errors from the underlying GitLabClient are logged
// per-repo and treated as continue-and-keep-polling: a transient API
// blip should not fail an otherwise-green run.
//
// State transition responsibility lives with the caller.
func (i *Integrator) WaitForGreen(ctx context.Context, run *store.CrossRepoRun) (store.CrossRepoState, error) {
	if err := i.guard(); err != nil {
		return store.CrossRepoFailed, err
	}
	if run == nil || len(run.Repos) == 0 {
		return store.CrossRepoFailed, errors.New("crossrepo: integrator: run has no repos")
	}

	timeout := i.perRepoTimeout()
	interval := i.pollInterval()
	deadline := i.now().Add(timeout)

	// statuses tracks the most recent terminal status per repo index.
	statuses := make([]CIStatus, len(run.Repos))

	for {
		allGreen := true
		for idx, repo := range run.Repos {
			if statuses[idx] == CISuccess {
				continue
			}
			if repo.MRIID == nil {
				return store.CrossRepoFailed, fmt.Errorf(
					"crossrepo: integrator: repo %s (project_id=%d) has no MR IID",
					repo.RepoName, repo.ProjectID)
			}
			status, err := i.GitLabClient.GetMRPipelineStatus(ctx, repo.ProjectID, *repo.MRIID)
			if err != nil {
				i.logger().Warn("crossrepo wait_for_green poll error",
					"repo", repo.RepoName, "project_id", repo.ProjectID,
					"mr_iid", *repo.MRIID, "err", err.Error())
				allGreen = false
				continue
			}
			statuses[idx] = status
			switch status {
			case CISuccess:
				i.logger().Info("crossrepo wait_for_green repo green",
					"repo", repo.RepoName, "project_id", repo.ProjectID,
					"mr_iid", *repo.MRIID)
			case CIFailed, CICanceled:
				return store.CrossRepoFailed, fmt.Errorf(
					"crossrepo: integrator: repo %s (project_id=%d) ci=%s",
					repo.RepoName, repo.ProjectID, status)
			default:
				allGreen = false
			}
		}
		if allGreen {
			return store.CrossRepoGatesGreen, nil
		}
		if !i.now().Before(deadline) {
			pending := i.firstPendingRepo(run, statuses)
			return store.CrossRepoFailed, fmt.Errorf(
				"crossrepo: integrator: timeout after %s waiting on repo %s (project_id=%d)",
				timeout, pending.RepoName, pending.ProjectID)
		}
		if err := waitOrCancel(ctx, interval); err != nil {
			return store.CrossRepoFailed, err
		}
	}
}

// AtomicMerge merges every repo in declaration order. On per-repo merge
// failure, opens revert MRs for already-merged repos in REVERSE order
// (the trunk reflects the most-recent intent first) and returns
// CrossRepoReverted. Returns CrossRepoFailed if no merges had succeeded
// yet — there is nothing to revert and nothing was merged. Returns
// CrossRepoMerged on full success.
//
// Resilience: a revert call that itself fails is logged but does not
// abort the rollback for remaining merged repos. The integrator's job is
// to drive the trunk back to a coherent state; partial revert progress
// beats no progress.
func (i *Integrator) AtomicMerge(ctx context.Context, run *store.CrossRepoRun) (store.CrossRepoState, error) {
	if err := i.guard(); err != nil {
		return store.CrossRepoFailed, err
	}
	if run == nil || len(run.Repos) == 0 {
		return store.CrossRepoFailed, errors.New("crossrepo: integrator: run has no repos")
	}

	mergedIdx := make([]int, 0, len(run.Repos))
	for idx, repo := range run.Repos {
		if repo.MRIID == nil {
			i.revertAll(ctx, run, mergedIdx, fmt.Sprintf(
				"prior repo %s (project_id=%d) has no MR IID; rolling back",
				repo.RepoName, repo.ProjectID))
			return i.terminalAfterMergeError(mergedIdx),
				fmt.Errorf("crossrepo: integrator: repo %s missing MR IID",
					repo.RepoName)
		}
		err := i.GitLabClient.MergeMR(ctx, repo.ProjectID, *repo.MRIID, MergeOptions{
			ShouldRemoveSourceBranch: true,
		})
		if err != nil {
			reason := fmt.Sprintf("merge failed on %s (project_id=%d): %v",
				repo.RepoName, repo.ProjectID, err)
			i.revertAll(ctx, run, mergedIdx, reason)
			return i.terminalAfterMergeError(mergedIdx),
				fmt.Errorf("crossrepo: integrator: %s", reason)
		}
		mergedIdx = append(mergedIdx, idx)
		i.logger().Info("crossrepo atomic_merge merged",
			"repo", repo.RepoName, "project_id", repo.ProjectID,
			"mr_iid", *repo.MRIID)
	}
	return store.CrossRepoMerged, nil
}

// revertAll opens revert MRs for the given merged-index list in REVERSE
// order. Errors per repo are logged + counted; rollback continues so the
// operator sees as much revert progress as possible.
func (i *Integrator) revertAll(ctx context.Context, run *store.CrossRepoRun, mergedIdx []int, reason string) {
	for k := len(mergedIdx) - 1; k >= 0; k-- {
		repo := run.Repos[mergedIdx[k]]
		mrIID := *repo.MRIID
		newIID, err := i.GitLabClient.OpenRevertMR(ctx, repo.ProjectID, mrIID, RevertOptions{
			Reason: reason,
		})
		if err != nil {
			i.logger().Error("crossrepo atomic_merge revert failed; continuing rollback",
				"repo", repo.RepoName, "project_id", repo.ProjectID,
				"merged_mr_iid", mrIID, "err", err.Error())
			continue
		}
		i.logger().Info("crossrepo atomic_merge revert opened",
			"repo", repo.RepoName, "project_id", repo.ProjectID,
			"merged_mr_iid", mrIID, "revert_mr_iid", newIID)
	}
}

// terminalAfterMergeError reports the terminal state when a merge call in
// AtomicMerge fails: reverted if any repo had already merged (rollback
// happened), failed if nothing had merged yet.
func (i *Integrator) terminalAfterMergeError(mergedIdx []int) store.CrossRepoState {
	if len(mergedIdx) == 0 {
		return store.CrossRepoFailed
	}
	return store.CrossRepoReverted
}

// firstPendingRepo returns the first repo whose status hasn't reached
// CISuccess yet — used for diagnostic output on timeout.
func (i *Integrator) firstPendingRepo(run *store.CrossRepoRun, statuses []CIStatus) store.CrossRepoRepoEntry {
	for idx, s := range statuses {
		if s != CISuccess {
			return run.Repos[idx]
		}
	}
	return run.Repos[0]
}

func (i *Integrator) guard() error {
	if i == nil {
		return errors.New("crossrepo: integrator: receiver is nil")
	}
	if i.GitLabClient == nil {
		return errors.New("crossrepo: integrator: gitlab client is required")
	}
	return nil
}

func (i *Integrator) perRepoTimeout() time.Duration {
	if i.Policy != nil {
		if pol := i.Policy.Snapshot(); pol != nil && pol.CrossRepo.PerRepoTimeoutMinutes > 0 {
			return time.Duration(pol.CrossRepo.PerRepoTimeoutMinutes) * time.Minute
		}
	}
	return defaultPerRepoTimeoutMinutes * time.Minute
}

func (i *Integrator) pollInterval() time.Duration {
	if i.PollInterval > 0 {
		return i.PollInterval
	}
	return defaultPollInterval
}

func (i *Integrator) now() time.Time {
	if i.Now != nil {
		return i.Now()
	}
	return time.Now()
}

func (i *Integrator) logger() *slog.Logger {
	if i.Logger != nil {
		return i.Logger
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// waitOrCancel sleeps for d, returning ctx.Err() if the context cancels
// first. Uses time.NewTimer + select instead of time.Sleep so polling
// halts immediately on ctx cancel rather than on the next tick boundary.
func waitOrCancel(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
