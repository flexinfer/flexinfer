// ops_helpers.go — Shared utility functions for sync operations: file I/O, validation, and backup helpers.
package sync

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/pelletier/go-toml/v2"
)

// trimmedBytes is a convenience wrapper for bytes.TrimSpace.
func trimmedBytes(data []byte) []byte {
	return bytes.TrimSpace(data)
}

func readNonEmptyTextSnapshot(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	return append([]byte(nil), data...)
}

func readJSONSnapshot(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if !isValidGeminiJSONObject(data) {
		return nil
	}
	return append([]byte(nil), data...)
}

func readTOMLSnapshot(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if !isValidTOML(data) {
		return nil
	}
	return append([]byte(nil), data...)
}

func profileBackupRoots(homePath, profileName string) []string {
	homeParent := filepath.Dir(homePath)
	roots := []string{
		filepath.Join(homePath, "backups"),
		filepath.Join(homeParent, ".config", "loom", "backups", profileName),
	}

	seen := make(map[string]struct{}, len(roots))
	unique := make([]string, 0, len(roots))
	for _, root := range roots {
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}
		unique = append(unique, root)
	}
	return unique
}

func readProfileBackupFile(homePath, profileName, relPath string, validate func([]byte) bool) []byte {
	for _, root := range profileBackupRoots(homePath, profileName) {
		if data := readLatestBackupFile(root, relPath, validate); len(data) > 0 {
			return data
		}
	}
	return nil
}

func readLatestBackupFile(backupRoot, relPath string, validate func([]byte) bool) []byte {
	entries, err := os.ReadDir(backupRoot)
	if err != nil {
		return nil
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() > entries[j].Name()
	})

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(backupRoot, entry.Name(), relPath)
		data, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		if validate(data) {
			return append([]byte(nil), data...)
		}
	}

	return nil
}

func ensureProfileJSONFile(homePath, profileName, relPath string, fallback, defaultData []byte) error {
	path := filepath.Join(homePath, relPath)
	if current, err := os.ReadFile(path); err == nil && isValidGeminiJSONObject(current) {
		return nil
	}
	if len(fallback) > 0 && isValidGeminiJSONObject(fallback) {
		return writeFileAtomic(path, fallback, 0o600)
	}
	if backup := readProfileBackupFile(homePath, profileName, relPath, isValidGeminiJSONObject); len(backup) > 0 {
		return writeFileAtomic(path, backup, 0o600)
	}
	return writeFileAtomic(path, defaultData, 0o600)
}

func ensureProfileTOMLFile(homePath, profileName, relPath string, fallback, defaultData []byte) error {
	path := filepath.Join(homePath, relPath)
	if current, err := os.ReadFile(path); err == nil && isValidTOML(current) {
		return nil
	}
	if len(fallback) > 0 && isValidTOML(fallback) {
		return writeFileAtomic(path, fallback, 0o600)
	}
	if backup := readProfileBackupFile(homePath, profileName, relPath, isValidTOML); len(backup) > 0 {
		return writeFileAtomic(path, backup, 0o600)
	}
	return writeFileAtomic(path, defaultData, 0o600)
}

func isValidJSON(data []byte) bool {
	if len(bytes.TrimSpace(data)) == 0 {
		return false
	}
	return json.Valid(data)
}

func isValidNonEmptyText(data []byte) bool {
	return len(bytes.TrimSpace(data)) > 0
}

func isValidTOML(data []byte) bool {
	if len(bytes.TrimSpace(data)) == 0 {
		return false
	}
	var obj map[string]any
	return toml.Unmarshal(data, &obj) == nil
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) (retErr error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if retErr != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	return nil
}
