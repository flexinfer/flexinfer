// Package sync provides sync status and drift detection.
package sync

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"time"
)

// DriftStatus indicates the sync state of a file.
type DriftStatus int

const (
	DriftInSync DriftStatus = iota
	DriftOutOfSync
	DriftMissing
	DriftExtra
)

func (s DriftStatus) String() string {
	switch s {
	case DriftInSync:
		return "in-sync"
	case DriftOutOfSync:
		return "out-of-sync"
	case DriftMissing:
		return "missing"
	case DriftExtra:
		return "extra"
	default:
		return "unknown"
	}
}

// DriftItem represents a specific file that differs.
type DriftItem struct {
	File       string
	RepoHash   string
	HomeHash   string
	Status     DriftStatus
}

// SyncStatus represents the sync state of a profile.
type SyncStatus struct {
	Profile      string
	RepoExists   bool
	HomeExists   bool
	InSync       bool
	DriftDetails []DriftItem
	LastSyncTime time.Time
	RepoPath     string
	HomePath     string
}

// GetSyncStatus returns the sync status for a profile.
func (m *Manager) GetSyncStatus(profileName string) (*SyncStatus, error) {
	profile := m.Get(profileName)
	if profile == nil {
		return nil, nil
	}

	repoPath := filepath.Join(m.RepoRoot, profile.RepoDir)
	homePath := expandHome(profile.HomeDir)

	status := &SyncStatus{
		Profile:  profileName,
		RepoPath: repoPath,
		HomePath: homePath,
		InSync:   true,
	}

	// Check if directories exist
	if _, err := os.Stat(repoPath); err == nil {
		status.RepoExists = true
	}
	if _, err := os.Stat(homePath); err == nil {
		status.HomeExists = true
	}

	// If either doesn't exist, not in sync
	if !status.RepoExists || !status.HomeExists {
		status.InSync = false
		return status, nil
	}

	// Compare files
	status.DriftDetails = m.compareDirectories(repoPath, homePath, profile)
	for _, item := range status.DriftDetails {
		if item.Status != DriftInSync {
			status.InSync = false
			break
		}
	}

	return status, nil
}

// GetAllSyncStatus returns sync status for all profiles.
func (m *Manager) GetAllSyncStatus() (map[string]*SyncStatus, error) {
	result := make(map[string]*SyncStatus)

	for _, name := range m.List() {
		status, err := m.GetSyncStatus(name)
		if err != nil {
			return nil, err
		}
		if status != nil {
			result[name] = status
		}
	}

	return result, nil
}

// compareDirectories compares files between repo and home directories.
func (m *Manager) compareDirectories(repoPath, homePath string, profile *Profile) []DriftItem {
	var items []DriftItem

	// Get files in repo
	repoFiles := make(map[string]string)
	filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(repoPath, path)
		if m.shouldExclude(rel, profile) {
			return nil
		}
		hash, _ := hashFile(path)
		repoFiles[rel] = hash
		return nil
	})

	// Get files in home
	homeFiles := make(map[string]string)
	filepath.Walk(homePath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(homePath, path)
		if m.shouldExclude(rel, profile) {
			return nil
		}
		hash, _ := hashFile(path)
		homeFiles[rel] = hash
		return nil
	})

	// Compare
	for file, repoHash := range repoFiles {
		homeHash, exists := homeFiles[file]
		if !exists {
			items = append(items, DriftItem{
				File:     file,
				RepoHash: repoHash,
				Status:   DriftMissing,
			})
		} else if repoHash != homeHash {
			items = append(items, DriftItem{
				File:     file,
				RepoHash: repoHash,
				HomeHash: homeHash,
				Status:   DriftOutOfSync,
			})
		} else {
			items = append(items, DriftItem{
				File:     file,
				RepoHash: repoHash,
				HomeHash: homeHash,
				Status:   DriftInSync,
			})
		}
	}

	// Check for extra files in home
	for file, homeHash := range homeFiles {
		if _, exists := repoFiles[file]; !exists {
			items = append(items, DriftItem{
				File:     file,
				HomeHash: homeHash,
				Status:   DriftExtra,
			})
		}
	}

	return items
}

// shouldExclude checks if a file should be excluded from comparison.
func (m *Manager) shouldExclude(path string, profile *Profile) bool {
	for _, exc := range profile.Excludes {
		if path == exc || filepath.Base(path) == exc {
			return true
		}
	}
	for _, secret := range profile.SecretFiles {
		if path == secret || filepath.Base(path) == secret {
			return true
		}
	}
	return false
}

// hashFile computes SHA256 hash of a file.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)[:8]), nil
}

// expandHome expands ~ to home directory.
func expandHome(path string) string {
	if len(path) > 0 && path[0] == '~' {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[1:])
	}
	return path
}
