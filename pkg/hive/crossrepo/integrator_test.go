package crossrepo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/hive"
	"github.com/crb2nu/loom/pkg/hive/store"
)

// staticPolicy returns a fixed *hive.Policy snapshot for the integrator.
type staticPolicy struct{ p *hive.Policy }

func (s *staticPolicy) Snapshot() *hive.Policy { return s.p }

// fakeGitLab is a programmable GitLabClient. Status sequences are
// per-(projectID, mrIID); each call returns the next entry in the list
// and clamps to the final entry once exhausted, matching how a real
// pipeline holds its terminal status across polls.
type fakeGitLab struct {
	mu sync.Mutex

	statusSeq map[string][]CIStatus
	statusIdx map[string]int
	statusErr map[string]error

	mergeErrFor  map[string]error
	revertErrFor map[string]error

	mergeCalls  []string // ordered key list "p:i"
	revertCalls []string // ordered key list "p:i" - the merged MR being reverted
	revertCount int
}

func newFakeGitLab() *fakeGitLab {
	return &fakeGitLab{
		statusSeq:    map[string][]CIStatus{},
		statusIdx:    map[string]int{},
		statusErr:    map[string]error{},
		mergeErrFor:  map[string]error{},
		revertErrFor: map[string]error{},
	}
}

func mrKey(projectID, mrIID int64) string {
	return fmt.Sprintf("%d:%d", projectID, mrIID)
}

func (f *fakeGitLab) setStatus(projectID, mrIID int64, seq ...CIStatus) {
	f.statusSeq[mrKey(projectID, mrIID)] = seq
}

func (f *fakeGitLab) setMergeError(projectID, mrIID int64, err error) {
	f.mergeErrFor[mrKey(projectID, mrIID)] = err
}

func (f *fakeGitLab) setRevertError(projectID, mrIID int64, err error) {
	f.revertErrFor[mrKey(projectID, mrIID)] = err
}

func (f *fakeGitLab) GetMRPipelineStatus(_ context.Context, projectID, mrIID int64) (CIStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := mrKey(projectID, mrIID)
	if err := f.statusErr[k]; err != nil {
		return CIPending, err
	}
	seq, ok := f.statusSeq[k]
	if !ok || len(seq) == 0 {
		return CIPending, nil
	}
	idx := f.statusIdx[k]
	if idx >= len(seq) {
		idx = len(seq) - 1
	}
	f.statusIdx[k] = idx + 1
	return seq[idx], nil
}

func (f *fakeGitLab) MergeMR(_ context.Context, projectID, mrIID int64, _ MergeOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := mrKey(projectID, mrIID)
	f.mergeCalls = append(f.mergeCalls, k)
	if err := f.mergeErrFor[k]; err != nil {
		return err
	}
	return nil
}

func (f *fakeGitLab) OpenRevertMR(_ context.Context, projectID, mergedMRIID int64, _ RevertOptions) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := mrKey(projectID, mergedMRIID)
	f.revertCalls = append(f.revertCalls, k)
	if err := f.revertErrFor[k]; err != nil {
		return 0, err
	}
	f.revertCount++
	// Synthesise a deterministic new IID so callers can assert on it.
	return mergedMRIID + 1000, nil
}

// virtualClock advances only when the test explicitly calls Advance, so
// timeout tests are deterministic and free of real-time sleeps.
type virtualClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *virtualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *virtualClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func mrPtr(v int64) *int64 { return &v }

func newCrossRun(repos ...store.CrossRepoRepoEntry) *store.CrossRepoRun {
	return &store.CrossRepoRun{
		ID:                "CRR-1",
		BacklogItemID:     "BL-CROSS-1",
		Repos:             repos,
		State:             store.CrossRepoOpen,
		AtomicityStrategy: "all_or_revert",
	}
}

// integratorWithFakes wires an integrator against a fake GitLab + virtual
// clock + tiny poll interval.
func integratorWithFakes(t *testing.T, perRepoMinutes int) (*Integrator, *fakeGitLab, *virtualClock) {
	t.Helper()
	clk := &virtualClock{now: time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)}
	gl := newFakeGitLab()
	pol := &hive.Policy{
		Version: 2,
		CrossRepo: hive.CrossRepoPolicy{
			Enabled:               true,
			PerRepoTimeoutMinutes: perRepoMinutes,
		},
	}
	return &Integrator{
		GitLabClient: gl,
		Policy:       &staticPolicy{p: pol},
		Now:          clk.Now,
		PollInterval: time.Millisecond,
	}, gl, clk
}

func TestIntegrator_WaitForGreen_AllPass(t *testing.T) {
	i, gl, _ := integratorWithFakes(t, 60)
	gl.setStatus(47, 42, CIPending, CIRunning, CISuccess)
	gl.setStatus(51, 99, CIRunning, CISuccess)
	run := newCrossRun(
		store.CrossRepoRepoEntry{ProjectID: 47, RepoName: "loom-core", Branch: "feat/x", MRIID: mrPtr(42)},
		store.CrossRepoRepoEntry{ProjectID: 51, RepoName: "loom", Branch: "feat/x", MRIID: mrPtr(99)},
	)
	state, err := i.WaitForGreen(context.Background(), run)
	if err != nil {
		t.Fatalf("WaitForGreen: %v", err)
	}
	if state != store.CrossRepoGatesGreen {
		t.Errorf("state = %s, want gates_green", state)
	}
}

func TestIntegrator_WaitForGreen_RepoTimesOut(t *testing.T) {
	i, gl, clk := integratorWithFakes(t, 1) // 1 minute timeout
	gl.setStatus(47, 42, CISuccess)
	// Repo 51 stays pending forever.
	gl.setStatus(51, 99, CIPending, CIPending, CIPending)

	run := newCrossRun(
		store.CrossRepoRepoEntry{ProjectID: 47, RepoName: "loom-core", Branch: "feat/x", MRIID: mrPtr(42)},
		store.CrossRepoRepoEntry{ProjectID: 51, RepoName: "loom", Branch: "feat/x", MRIID: mrPtr(99)},
	)

	// Advance the virtual clock past the timeout while WaitForGreen polls.
	// Because PollInterval is sub-millisecond, the loop will see the
	// advanced clock on its next tick.
	go func() {
		time.Sleep(20 * time.Millisecond)
		clk.Advance(2 * time.Minute)
	}()

	state, err := i.WaitForGreen(context.Background(), run)
	if err == nil {
		t.Fatalf("expected timeout error, got nil")
	}
	if state != store.CrossRepoFailed {
		t.Errorf("state = %s, want failed", state)
	}
	msg := err.Error()
	if !strings.Contains(msg, "loom") {
		t.Errorf("error = %v, want it to mention the timed-out repo name", err)
	}
	if !strings.Contains(msg, "51") {
		t.Errorf("error = %v, want it to mention the project_id", err)
	}
}

func TestIntegrator_WaitForGreen_FailedCIIsTerminal(t *testing.T) {
	i, gl, _ := integratorWithFakes(t, 60)
	gl.setStatus(47, 42, CIRunning, CIFailed)
	gl.setStatus(51, 99, CISuccess)
	run := newCrossRun(
		store.CrossRepoRepoEntry{ProjectID: 47, RepoName: "loom-core", Branch: "feat/x", MRIID: mrPtr(42)},
		store.CrossRepoRepoEntry{ProjectID: 51, RepoName: "loom", Branch: "feat/x", MRIID: mrPtr(99)},
	)
	state, err := i.WaitForGreen(context.Background(), run)
	if err == nil {
		t.Fatalf("expected error on CI failure, got nil")
	}
	if state != store.CrossRepoFailed {
		t.Errorf("state = %s, want failed", state)
	}
}

func TestIntegrator_WaitForGreen_ContextCancel(t *testing.T) {
	i, gl, _ := integratorWithFakes(t, 60)
	gl.setStatus(47, 42, CIPending, CIPending)
	run := newCrossRun(
		store.CrossRepoRepoEntry{ProjectID: 47, RepoName: "loom-core", Branch: "feat/x", MRIID: mrPtr(42)},
	)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()
	_, err := i.WaitForGreen(ctx, run)
	if err == nil {
		t.Fatalf("expected ctx cancel error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestIntegrator_AtomicMerge_AllSucceed(t *testing.T) {
	i, _, _ := integratorWithFakes(t, 60)
	run := newCrossRun(
		store.CrossRepoRepoEntry{ProjectID: 47, RepoName: "loom-core", Branch: "feat/x", MRIID: mrPtr(42)},
		store.CrossRepoRepoEntry{ProjectID: 51, RepoName: "loom", Branch: "feat/x", MRIID: mrPtr(99)},
	)
	state, err := i.AtomicMerge(context.Background(), run)
	if err != nil {
		t.Fatalf("AtomicMerge: %v", err)
	}
	if state != store.CrossRepoMerged {
		t.Errorf("state = %s, want merged", state)
	}
	gl := i.GitLabClient.(*fakeGitLab)
	if got, want := gl.mergeCalls, []string{"47:42", "51:99"}; !equalStrSlice(got, want) {
		t.Errorf("merge call order = %v, want %v", got, want)
	}
	if len(gl.revertCalls) != 0 {
		t.Errorf("revert calls = %v, want none", gl.revertCalls)
	}
}

func TestIntegrator_AtomicMerge_TwoReposSecondFails_RevertsFirst(t *testing.T) {
	i, gl, _ := integratorWithFakes(t, 60)
	gl.setMergeError(51, 99, errors.New("merge conflict"))
	run := newCrossRun(
		store.CrossRepoRepoEntry{ProjectID: 47, RepoName: "loom-core", Branch: "feat/x", MRIID: mrPtr(42)},
		store.CrossRepoRepoEntry{ProjectID: 51, RepoName: "loom", Branch: "feat/x", MRIID: mrPtr(99)},
	)
	state, err := i.AtomicMerge(context.Background(), run)
	if err == nil {
		t.Fatalf("expected merge failure, got nil")
	}
	if state != store.CrossRepoReverted {
		t.Errorf("state = %s, want reverted", state)
	}
	if !strings.Contains(err.Error(), "loom") {
		t.Errorf("error = %v, want mention of failing repo", err)
	}
	if !equalStrSlice(gl.mergeCalls, []string{"47:42", "51:99"}) {
		t.Errorf("merge calls = %v, want [47:42 51:99]", gl.mergeCalls)
	}
	if !equalStrSlice(gl.revertCalls, []string{"47:42"}) {
		t.Errorf("revert calls = %v, want [47:42] (reverse order over the one merged repo)",
			gl.revertCalls)
	}
}

func TestIntegrator_AtomicMerge_ThreeReposThirdFails_RevertsInReverseOrder(t *testing.T) {
	i, gl, _ := integratorWithFakes(t, 60)
	gl.setMergeError(53, 17, errors.New("conflict"))
	run := newCrossRun(
		store.CrossRepoRepoEntry{ProjectID: 47, RepoName: "loom-core", Branch: "feat/x", MRIID: mrPtr(42)},
		store.CrossRepoRepoEntry{ProjectID: 51, RepoName: "loom", Branch: "feat/x", MRIID: mrPtr(99)},
		store.CrossRepoRepoEntry{ProjectID: 53, RepoName: "flexdeck", Branch: "feat/x", MRIID: mrPtr(17)},
	)
	state, err := i.AtomicMerge(context.Background(), run)
	if err == nil {
		t.Fatalf("expected merge failure, got nil")
	}
	if state != store.CrossRepoReverted {
		t.Errorf("state = %s, want reverted", state)
	}
	wantMerge := []string{"47:42", "51:99", "53:17"}
	if !equalStrSlice(gl.mergeCalls, wantMerge) {
		t.Errorf("merge calls = %v, want %v", gl.mergeCalls, wantMerge)
	}
	// Revert order = REVERSE of merged: [51:99, 47:42] (the third repo
	// was never merged, so it's not in the revert set).
	wantRevert := []string{"51:99", "47:42"}
	if !equalStrSlice(gl.revertCalls, wantRevert) {
		t.Errorf("revert calls = %v, want %v", gl.revertCalls, wantRevert)
	}
}

func TestIntegrator_AtomicMerge_RevertCallFails_ContinuesRollback(t *testing.T) {
	i, gl, _ := integratorWithFakes(t, 60)
	gl.setMergeError(51, 99, errors.New("conflict"))
	gl.setRevertError(47, 42, errors.New("revert mr api blew up"))
	run := newCrossRun(
		store.CrossRepoRepoEntry{ProjectID: 47, RepoName: "loom-core", Branch: "feat/x", MRIID: mrPtr(42)},
		store.CrossRepoRepoEntry{ProjectID: 51, RepoName: "loom", Branch: "feat/x", MRIID: mrPtr(99)},
	)
	state, err := i.AtomicMerge(context.Background(), run)
	if err == nil {
		t.Fatalf("expected merge failure, got nil")
	}
	if state != store.CrossRepoReverted {
		t.Errorf("state = %s, want reverted (we still attempted rollback)", state)
	}
	if !equalStrSlice(gl.revertCalls, []string{"47:42"}) {
		t.Errorf("revert calls = %v, want [47:42] attempted even though it errored",
			gl.revertCalls)
	}
}

func TestIntegrator_AtomicMerge_FirstRepoFailsReturnsFailed(t *testing.T) {
	i, gl, _ := integratorWithFakes(t, 60)
	gl.setMergeError(47, 42, errors.New("upstream race"))
	run := newCrossRun(
		store.CrossRepoRepoEntry{ProjectID: 47, RepoName: "loom-core", Branch: "feat/x", MRIID: mrPtr(42)},
		store.CrossRepoRepoEntry{ProjectID: 51, RepoName: "loom", Branch: "feat/x", MRIID: mrPtr(99)},
	)
	state, err := i.AtomicMerge(context.Background(), run)
	if err == nil {
		t.Fatalf("expected merge failure, got nil")
	}
	// Nothing had merged yet so there is nothing to revert: failed, not reverted.
	if state != store.CrossRepoFailed {
		t.Errorf("state = %s, want failed (no rollback because nothing merged)", state)
	}
	if len(gl.revertCalls) != 0 {
		t.Errorf("revert calls = %v, want none", gl.revertCalls)
	}
}

func TestIntegrator_GuardsAgainstMissingClient(t *testing.T) {
	i := &Integrator{}
	if _, err := i.WaitForGreen(context.Background(), newCrossRun()); err == nil {
		t.Errorf("expected error when client is nil")
	}
	if _, err := i.AtomicMerge(context.Background(), newCrossRun()); err == nil {
		t.Errorf("expected error when client is nil")
	}
}

func TestIntegrator_WaitForGreen_RepoMissingMRIDIsTerminal(t *testing.T) {
	i, _, _ := integratorWithFakes(t, 60)
	run := newCrossRun(
		store.CrossRepoRepoEntry{ProjectID: 47, RepoName: "loom-core", Branch: "feat/x"},
	)
	state, err := i.WaitForGreen(context.Background(), run)
	if err == nil {
		t.Fatalf("expected error for missing MR IID")
	}
	if state != store.CrossRepoFailed {
		t.Errorf("state = %s, want failed", state)
	}
}

func equalStrSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
