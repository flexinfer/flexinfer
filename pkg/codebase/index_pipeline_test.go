package codebase

import (
	"testing"

	"github.com/crb2nu/loom/pkg/codebase/schema"
)

// TestRecordFlushWarning_DoesNotMarkJobFailed asserts that a job-final
// Flush failure is surfaced as a soft warning on job stats but the job
// still reports status=done. This is the structural invariant the
// indexer must preserve: an EOF on the trailing durability ping cannot
// mark a fully-processed run as failed, because all data has already
// landed in Qdrant via wait=false upserts and is durable through WAL
// fsync regardless.
func TestRecordFlushWarning_DoesNotMarkJobFailed(t *testing.T) {
	t.Parallel()

	svc := &Service{
		jobs: map[string]*indexJob{},
	}
	jobID := "test-job"
	svc.jobs[jobID] = &indexJob{
		id:     jobID,
		status: "running",
		stats:  schema.IndexStats{RepoID: "r", FilesTotal: 10, FilesDone: 10},
	}

	svc.recordFlushWarning(jobID, "job-final flush: EOF")
	svc.setJobDone(jobID)

	job := svc.jobs[jobID]
	if job.status != "done" {
		t.Fatalf("status=%q want \"done\" after recordFlushWarning + setJobDone", job.status)
	}
	if job.stats.FlushWarnings != 1 {
		t.Fatalf("FlushWarnings=%d want 1", job.stats.FlushWarnings)
	}
	if job.stats.LastFlushWarning == "" {
		t.Fatalf("LastFlushWarning should be set to the warning message")
	}
	if job.stats.Errors != 0 {
		t.Fatalf("Errors=%d want 0 (flush warning must NOT bump hard error count)", job.stats.Errors)
	}
	if job.err != "" {
		t.Fatalf("job.err=%q want empty (flush warning is soft)", job.err)
	}
}

// TestRecordFlushWarning_IsAdditive asserts the counter increments on
// repeated calls (e.g. if a future code path retries Flush).
func TestRecordFlushWarning_IsAdditive(t *testing.T) {
	t.Parallel()

	svc := &Service{jobs: map[string]*indexJob{}}
	jobID := "j"
	svc.jobs[jobID] = &indexJob{id: jobID, status: "running"}

	svc.recordFlushWarning(jobID, "first warn")
	svc.recordFlushWarning(jobID, "second warn")

	got := svc.jobs[jobID].stats.FlushWarnings
	if got != 2 {
		t.Fatalf("FlushWarnings=%d want 2", got)
	}
	if last := svc.jobs[jobID].stats.LastFlushWarning; last != "second warn" {
		t.Fatalf("LastFlushWarning=%q want %q", last, "second warn")
	}
}
