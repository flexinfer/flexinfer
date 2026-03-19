// Package sync provides sync status and drift detection.
package sync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/skills"
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
	File     string
	RepoHash string
	HomeHash string
	Status   DriftStatus
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

	repoPath := m.ResolveRepoPath(profile)
	homePath := m.ResolveHomePath(profile)

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
	requiresRepo := !profile.GeneratedDirectToHome
	if profile.SkillsManifest != "" && !profile.SkillsDirectToHome {
		requiresRepo = true
	}
	if (requiresRepo && !status.RepoExists) || !status.HomeExists {
		status.InSync = false
		return status, nil
	}

	// Compare files
	if profile.GeneratedDirectToHome {
		status.DriftDetails = compareHomeGeneratedFiles(homePath, profile)
	} else if profile.SyncGeneratedOnly {
		status.DriftDetails = compareGeneratedFile(repoPath, homePath, profile)
	} else {
		status.DriftDetails = m.compareDirectories(repoPath, homePath, profile)
	}
	for _, item := range status.DriftDetails {
		if item.Status != DriftInSync {
			status.InSync = false
			break
		}
	}

	// Check skill files for drift via manifest
	if profile.SkillsManifest != "" {
		var skillDrift []DriftItem
		if profile.SkillsDirectToHome {
			// Skills live only in home; verify they exist there.
			skillDrift = compareHomeSkillFiles(homePath)
		} else {
			skillDrift = compareSkillFiles(repoPath, homePath, profile)
		}
		status.DriftDetails = append(status.DriftDetails, skillDrift...)
		for _, item := range skillDrift {
			if item.Status != DriftInSync {
				status.InSync = false
			}
		}
	}

	return status, nil
}

// DriftSummary returns aggregate drift counts across all profiles. Useful for
// HUD status display and quick health checks.
func (m *Manager) DriftSummary() (inSync int, outOfSync int, missing int, err error) {
	for _, name := range m.List() {
		status, e := m.GetSyncStatus(name)
		if e != nil {
			err = e
			return
		}
		if status == nil {
			continue
		}
		if status.InSync {
			inSync++
		} else {
			// Classify why it's out of sync.
			hasMissing := false
			for _, item := range status.DriftDetails {
				if item.Status == DriftMissing {
					hasMissing = true
					break
				}
			}
			if hasMissing || !status.RepoExists || !status.HomeExists {
				missing++
			} else {
				outOfSync++
			}
		}
	}
	return
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
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(repoPath, path)
		if info.IsDir() {
			if m.shouldExclude(rel, profile) {
				return filepath.SkipDir
			}
			return nil
		}
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
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(homePath, path)
		if info.IsDir() {
			if m.shouldExclude(rel, profile) {
				return filepath.SkipDir
			}
			return nil
		}
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
	if shouldExclude(path, profile.Excludes) {
		return true
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

// compareSkillFiles checks drift for all files listed in the skills manifest.
func compareSkillFiles(repoPath, homePath string, profile *Profile) []DriftItem {
	manifest, _ := skills.ReadManifest(repoPath)
	if manifest == nil || len(manifest.Generated) == 0 {
		return nil
	}

	var items []DriftItem
	for _, relPath := range manifest.Generated {
		repoFile := filepath.Join(repoPath, relPath)
		homeFile := filepath.Join(homePath, relPath)

		repoExists := Exists(repoFile)
		homeExists := Exists(homeFile)

		switch {
		case repoExists && !homeExists:
			repoHash, _ := hashFile(repoFile)
			items = append(items, DriftItem{File: relPath, RepoHash: repoHash, Status: DriftMissing})
		case !repoExists && homeExists:
			homeHash, _ := hashFile(homeFile)
			items = append(items, DriftItem{File: relPath, HomeHash: homeHash, Status: DriftExtra})
		case repoExists && homeExists:
			repoHash, _ := hashFile(repoFile)
			homeHash, _ := hashFile(homeFile)
			if repoHash != homeHash {
				items = append(items, DriftItem{File: relPath, RepoHash: repoHash, HomeHash: homeHash, Status: DriftOutOfSync})
			} else {
				items = append(items, DriftItem{File: relPath, RepoHash: repoHash, HomeHash: homeHash, Status: DriftInSync})
			}
		}
	}

	return items
}

// compareHomeSkillFiles checks that skill files listed in the home manifest exist
// in the home directory. Used when SkillsDirectToHome is true (no repo copy).
func compareHomeSkillFiles(homePath string) []DriftItem {
	manifest, _ := skills.ReadManifest(homePath)
	if manifest == nil || len(manifest.Generated) == 0 {
		return nil
	}

	var items []DriftItem
	for _, relPath := range manifest.Generated {
		homeFile := filepath.Join(homePath, relPath)
		if Exists(homeFile) {
			homeHash, _ := hashFile(homeFile)
			items = append(items, DriftItem{File: relPath, HomeHash: homeHash, Status: DriftInSync})
		} else {
			items = append(items, DriftItem{File: relPath, Status: DriftMissing})
		}
	}
	return items
}

func compareHomeGeneratedFiles(homePath string, profile *Profile) []DriftItem {
	if profile.GeneratedFile == "" {
		return []DriftItem{{File: "", Status: DriftOutOfSync}}
	}

	files := []string{primaryHomeGeneratedFile(profile)}
	files = append(files, profile.ExtraGeneratedFiles...)

	var items []DriftItem
	for _, rel := range files {
		path := filepath.Join(homePath, rel)
		if Exists(path) {
			homeHash, _ := hashFile(path)
			items = append(items, DriftItem{File: rel, HomeHash: homeHash, Status: DriftInSync})
		} else if rel == primaryHomeGeneratedFile(profile) {
			items = append(items, DriftItem{File: rel, Status: DriftMissing})
		}
	}

	if len(items) == 0 {
		return []DriftItem{{File: primaryHomeGeneratedFile(profile), Status: DriftMissing}}
	}
	return items
}

func compareGeneratedFile(repoPath, homePath string, profile *Profile) []DriftItem {
	if profile.GeneratedFile == "" {
		return []DriftItem{{
			File:   "",
			Status: DriftOutOfSync,
		}}
	}

	// Compare all generated files (primary + extras).
	files := []string{profile.GeneratedFile}
	files = append(files, profile.ExtraGeneratedFiles...)

	var items []DriftItem
	for _, rel := range files {
		repoFile := filepath.Join(repoPath, rel)
		homeRel := mapRepoGeneratedToHome(profile, rel)
		homeFile := filepath.Join(homePath, homeRel)

		repoExists := Exists(repoFile)
		homeExists := Exists(homeFile)

		switch {
		case !repoExists && !homeExists:
			// Extra files are optional — skip if neither side has them.
			if rel != profile.GeneratedFile {
				continue
			}
			items = append(items, DriftItem{File: rel, Status: DriftMissing})
		case repoExists && !homeExists:
			repoHash, _ := hashFile(repoFile)
			items = append(items, DriftItem{File: rel, RepoHash: repoHash, Status: DriftMissing})
		case !repoExists && homeExists:
			homeHash, _ := hashFile(homeFile)
			items = append(items, DriftItem{File: rel, HomeHash: homeHash, Status: DriftExtra})
		default:
			repoHash, _ := hashFile(repoFile)
			homeHash, _ := hashFile(homeFile)
			if repoHash != homeHash {
				// For settings.json, hooks are stripped from repo but preserved in
				// home via merge. Compare non-hooks keys only to avoid false drift.
				if rel == "settings.json" && settingsInSyncIgnoringHooks(repoFile, homeFile) {
					items = append(items, DriftItem{File: rel, RepoHash: repoHash, HomeHash: homeHash, Status: DriftInSync})
				} else if strings.HasSuffix(rel, ".toml") && tomlInSyncIgnoringKeys(repoFile, homeFile, []string{"notify"}) {
					// For TOML configs, the [notify] section may exist in home
					// (added by lifecycle hooks) but not in repo. Treat as in-sync.
					items = append(items, DriftItem{File: rel, RepoHash: repoHash, HomeHash: homeHash, Status: DriftInSync})
				} else {
					items = append(items, DriftItem{File: rel, RepoHash: repoHash, HomeHash: homeHash, Status: DriftOutOfSync})
				}
			} else {
				items = append(items, DriftItem{File: rel, RepoHash: repoHash, HomeHash: homeHash, Status: DriftInSync})
			}
		}
	}

	if len(items) == 0 {
		return []DriftItem{{File: profile.GeneratedFile, Status: DriftMissing}}
	}
	return items
}

// configInSyncIgnoringKeys compares two JSON config files, treating them as
// in-sync if they differ only in the specified keys. This accounts for
// intentional design where certain keys are stripped from the repo copy but
// preserved in home via merge operations.
func configInSyncIgnoringKeys(repoFile, homeFile string, ignoreKeys []string) bool {
	repoData, err := os.ReadFile(repoFile)
	if err != nil {
		return false
	}
	homeData, err := os.ReadFile(homeFile)
	if err != nil {
		return false
	}

	var repoMap, homeMap map[string]json.RawMessage
	if json.Unmarshal(repoData, &repoMap) != nil || json.Unmarshal(homeData, &homeMap) != nil {
		return false
	}

	for _, key := range ignoreKeys {
		delete(repoMap, key)
		delete(homeMap, key)
	}

	repoNorm, _ := json.Marshal(repoMap)
	homeNorm, _ := json.Marshal(homeMap)
	return string(repoNorm) == string(homeNorm)
}

// settingsInSyncIgnoringHooks compares two settings.json files, treating them
// as in-sync if they differ only in the "hooks" key.
func settingsInSyncIgnoringHooks(repoFile, homeFile string) bool {
	return configInSyncIgnoringKeys(repoFile, homeFile, []string{"hooks"})
}

// tomlInSyncIgnoringKeys compares two TOML config files, treating them as
// in-sync if they differ only in the specified top-level sections. Uses a
// simple line-based approach to strip [key] sections rather than importing
// a TOML library, since the config files are simple.
func tomlInSyncIgnoringKeys(repoFile, homeFile string, ignoreKeys []string) bool {
	repoData, err := os.ReadFile(repoFile)
	if err != nil {
		return false
	}
	homeData, err := os.ReadFile(homeFile)
	if err != nil {
		return false
	}

	repoFiltered := filterTOMLSections(string(repoData), ignoreKeys)
	homeFiltered := filterTOMLSections(string(homeData), ignoreKeys)
	return repoFiltered == homeFiltered
}

// filterTOMLSections removes top-level sections matching the given keys from
// TOML content. A section starts with [key] and extends until the next
// top-level section header or end of file.
func filterTOMLSections(content string, ignoreKeys []string) string {
	ignore := make(map[string]bool, len(ignoreKeys))
	for _, k := range ignoreKeys {
		ignore[k] = true
	}

	lines := strings.Split(content, "\n")
	var result []string
	skipping := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Detect top-level section headers like [notify] or [notify.hooks]
		if strings.HasPrefix(trimmed, "[") && !strings.HasPrefix(trimmed, "[[") {
			section := strings.TrimPrefix(trimmed, "[")
			section = strings.TrimSuffix(section, "]")
			section = strings.TrimSpace(section)
			// Extract the top-level key (before any dot)
			topKey := section
			if idx := strings.Index(section, "."); idx >= 0 {
				topKey = section[:idx]
			}
			skipping = ignore[topKey]
		}
		if !skipping {
			result = append(result, line)
		}
	}

	return strings.Join(result, "\n")
}
