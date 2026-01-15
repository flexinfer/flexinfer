package codebase

import "time"

func (s *Service) setWatchFailed(watchID, msg string) {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	if job := s.watchJobs[watchID]; job != nil {
		job.status = "failed"
		job.err = msg
		job.stats.StoppedAt = time.Now()
	}
}

func (s *Service) setWatchStopped(watchID string) {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	if job := s.watchJobs[watchID]; job != nil {
		if job.status == "failed" {
			return
		}
		job.status = "stopped"
		job.stats.StoppedAt = time.Now()
	}
}

func (s *Service) incrementWatchEvent(watchID string) {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	if job := s.watchJobs[watchID]; job != nil {
		job.stats.EventsTotal++
		job.stats.LastEventAt = time.Now()
	}
}

func (s *Service) incrementWatchQueued(watchID string, n int) {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	if job := s.watchJobs[watchID]; job != nil {
		job.stats.FilesQueued += n
	}
}

func (s *Service) incrementWatchIndexed(watchID string, chunks int) {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	if job := s.watchJobs[watchID]; job != nil {
		job.stats.FilesIndexed++
		job.stats.ChunksUpserted += chunks
	}
}

func (s *Service) incrementWatchSkipped(watchID string) {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	if job := s.watchJobs[watchID]; job != nil {
		job.stats.FilesSkipped++
	}
}

func (s *Service) incrementWatchDeleted(watchID string) {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	if job := s.watchJobs[watchID]; job != nil {
		job.stats.FilesDeleted++
	}
}

func (s *Service) incrementWatchError(watchID, msg string) {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	if job := s.watchJobs[watchID]; job != nil {
		job.stats.Errors++
		job.err = msg
	}
}

