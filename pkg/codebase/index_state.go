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
	}
}

func (s *Service) setJobDone(jobID string) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	if job := s.jobs[jobID]; job != nil {
		job.status = "done"
		job.stats.FinishedAt = time.Now()
	}
}

func (s *Service) setJobCanceled(jobID string) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	if job := s.jobs[jobID]; job != nil {
		job.status = "canceled"
		job.stats.FinishedAt = time.Now()
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
