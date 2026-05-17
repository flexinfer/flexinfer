package codebase

import (
	"fmt"
	"os"
	"time"
)

func (s *Service) setJobFailed(jobID, msg string) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	if job := s.jobs[jobID]; job != nil {
		job.status = "failed"
		job.err = msg
		job.stats.FinishedAt = time.Now()
		job.stats.Stages.Total = stageSample(job.stats.FinishedAt.Sub(job.stats.StartedAt), job.stats.FilesDone)
	}
}

func (s *Service) setJobDone(jobID string) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	if job := s.jobs[jobID]; job != nil {
		job.status = "done"
		job.stats.FinishedAt = time.Now()
		job.stats.Stages.Total = stageSample(job.stats.FinishedAt.Sub(job.stats.StartedAt), job.stats.FilesDone)
	}
}

func (s *Service) setJobCanceled(jobID string) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	if job := s.jobs[jobID]; job != nil {
		job.status = "canceled"
		job.stats.FinishedAt = time.Now()
		job.stats.Stages.Total = stageSample(job.stats.FinishedAt.Sub(job.stats.StartedAt), job.stats.FilesDone)
	}
}

func (s *Service) incrementFilesDone(jobID string, chunks int) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	if job := s.jobs[jobID]; job != nil {
		job.stats.FilesDone++
		job.stats.ChunksTotal += chunks
	}
}

func (s *Service) incrementFilesSkipped(jobID string) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	if job := s.jobs[jobID]; job != nil {
		job.stats.FilesDone++
		job.stats.FilesSkipped++
	}
}

func (s *Service) incrementJobError(jobID, msg string) {
	_, _ = fmt.Fprintln(os.Stderr, msg)
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	if job := s.jobs[jobID]; job != nil {
		job.stats.Errors++
		job.err = msg
	}
}

// recordFlushWarning logs a soft warning for a job-final Flush failure and
// bumps the flush_warnings counter on job stats. This is deliberately
// distinct from incrementJobError: a flush failure does not mark the job
// failed (data is durable via WAL fsync regardless), but operators still
// need a way to observe transport flakiness against Qdrant.
func (s *Service) recordFlushWarning(jobID, msg string) {
	_, _ = fmt.Fprintln(os.Stderr, "WARN "+msg)
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	if job := s.jobs[jobID]; job != nil {
		job.stats.FlushWarnings++
		job.stats.LastFlushWarning = msg
	}
}
