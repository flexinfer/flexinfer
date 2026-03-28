// ops_gemini.go — Gemini platform sync: snapshot, ensure, prune, and repair operations.
package sync

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type geminiConfigSnapshot struct {
	trustedFolders      []byte
	extensionEnablement []byte
	extensionManifests  map[string][]byte
}

type geminiAuthSnapshot struct {
	googleAccounts []byte
	oauthCreds     []byte
	state          []byte
	installationID []byte
}

func readGeminiConfigSnapshot(homePath string) geminiConfigSnapshot {
	return geminiConfigSnapshot{
		trustedFolders:      readGeminiTrustedFoldersSnapshot(homePath),
		extensionEnablement: readGeminiJSONSnapshot(filepath.Join(homePath, "extensions", "extension-enablement.json")),
		extensionManifests:  readGeminiExtensionManifestSnapshots(homePath),
	}
}

func readGeminiAuthSnapshot(homePath string) geminiAuthSnapshot {
	return geminiAuthSnapshot{
		googleAccounts: readGeminiAuthJSONSnapshot(filepath.Join(homePath, "google_accounts.json")),
		oauthCreds:     readGeminiAuthJSONSnapshot(filepath.Join(homePath, "oauth_creds.json")),
		state:          readGeminiAuthJSONSnapshot(filepath.Join(homePath, "state.json")),
		installationID: readNonEmptyTextSnapshot(filepath.Join(homePath, "installation_id")),
	}
}

func readGeminiTrustedFoldersSnapshot(homePath string) []byte {
	path := filepath.Join(homePath, "trustedFolders.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if !isValidGeminiTrustedFolders(data) {
		return nil
	}
	return append([]byte(nil), data...)
}

func readGeminiJSONSnapshot(path string) []byte {
	return readJSONSnapshot(path)
}

func readGeminiAuthJSONSnapshot(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if !isValidJSON(data) {
		return nil
	}
	return append([]byte(nil), data...)
}

func readGeminiExtensionManifestSnapshots(homePath string) map[string][]byte {
	matches, err := filepath.Glob(filepath.Join(homePath, "extensions", "*", "gemini-extension.json"))
	if err != nil || len(matches) == 0 {
		return nil
	}

	snapshots := make(map[string][]byte)
	for _, path := range matches {
		data := readGeminiJSONSnapshot(path)
		if len(data) == 0 {
			continue
		}
		relPath, err := filepath.Rel(homePath, path)
		if err != nil {
			continue
		}
		snapshots[relPath] = data
	}
	if len(snapshots) == 0 {
		return nil
	}
	return snapshots
}

func ensureGeminiConfigFiles(homePath string, snapshot geminiConfigSnapshot) error {
	var errs []string

	if err := ensureGeminiTrustedFolders(homePath, snapshot.trustedFolders); err != nil {
		errs = append(errs, fmt.Sprintf("trustedFolders.json: %v", err))
	}
	if err := ensureGeminiExtensionEnablement(homePath, snapshot.extensionEnablement); err != nil {
		errs = append(errs, fmt.Sprintf("extensions/extension-enablement.json: %v", err))
	}
	if err := ensureGeminiExtensionManifests(homePath, snapshot.extensionManifests); err != nil {
		errs = append(errs, fmt.Sprintf("extensions/*/gemini-extension.json: %v", err))
	}
	if err := ensureGeminiExtensionCommands(homePath); err != nil {
		errs = append(errs, fmt.Sprintf("extensions/*/commands/*.toml: %v", err))
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func ensureGeminiAuthFiles(homePath string, snapshot geminiAuthSnapshot) error {
	var errs []string
	ensure := func(relPath string, fallback []byte, validate func([]byte) bool) {
		if len(fallback) == 0 {
			return
		}
		path := filepath.Join(homePath, relPath)
		if current, err := os.ReadFile(path); err == nil && validate(current) {
			return
		}
		if err := writeFileAtomic(path, fallback, 0o600); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", relPath, err))
		}
	}

	ensure("google_accounts.json", snapshot.googleAccounts, isValidJSON)
	ensure("oauth_creds.json", snapshot.oauthCreds, isValidJSON)
	ensure("state.json", snapshot.state, isValidJSON)
	ensure("installation_id", snapshot.installationID, isValidNonEmptyText)

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func ensureGeminiTrustedFolders(homePath string, fallback []byte) error {
	path := filepath.Join(homePath, "trustedFolders.json")
	if current, err := os.ReadFile(path); err == nil && isValidGeminiTrustedFolders(current) {
		return nil
	}
	if len(fallback) > 0 && isValidGeminiTrustedFolders(fallback) {
		return writeFileAtomic(path, fallback, 0o600)
	}
	return writeFileAtomic(path, []byte("{}\n"), 0o600)
}

func ensureGeminiExtensionEnablement(homePath string, fallback []byte) error {
	path := filepath.Join(homePath, "extensions", "extension-enablement.json")
	if current, err := os.ReadFile(path); err == nil && isValidGeminiJSONObject(current) {
		return nil
	}
	if len(fallback) > 0 && isValidGeminiJSONObject(fallback) {
		return writeFileAtomic(path, fallback, 0o600)
	}

	if backup := readGeminiBackupJSON(homePath, filepath.Join("extensions", "extension-enablement.json")); len(backup) > 0 {
		return writeFileAtomic(path, backup, 0o600)
	}

	return writeFileAtomic(path, []byte("{}\n"), 0o600)
}

func ensureGeminiExtensionManifests(homePath string, snapshots map[string][]byte) error {
	processed := make(map[string]struct{})
	var errs []string

	matches, err := filepath.Glob(filepath.Join(homePath, "extensions", "*", "gemini-extension.json"))
	if err != nil {
		return err
	}
	for _, path := range matches {
		relPath, err := filepath.Rel(homePath, path)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		processed[relPath] = struct{}{}
		if err := repairGeminiManifestFile(homePath, relPath, snapshots[relPath]); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", relPath, err))
		}
	}

	for relPath, fallback := range snapshots {
		if _, ok := processed[relPath]; ok {
			continue
		}
		if err := repairGeminiManifestFile(homePath, relPath, fallback); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", relPath, err))
		}
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func repairGeminiManifestFile(homePath, relPath string, fallback []byte) error {
	path := filepath.Join(homePath, relPath)
	if current, err := os.ReadFile(path); err == nil && isValidGeminiJSONObject(current) {
		return nil
	}
	if len(fallback) > 0 && isValidGeminiJSONObject(fallback) {
		return writeFileAtomic(path, fallback, 0o600)
	}
	if backup := readGeminiBackupJSON(homePath, relPath); len(backup) > 0 {
		return writeFileAtomic(path, backup, 0o600)
	}
	return writeFileAtomic(path, []byte("{}\n"), 0o600)
}

func ensureGeminiExtensionCommands(homePath string) error {
	extensionsDir := filepath.Join(homePath, "extensions")
	if info, err := os.Stat(extensionsDir); err != nil || !info.IsDir() {
		return nil
	}

	var errs []string
	err := filepath.WalkDir(extensionsDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			errs = append(errs, walkErr.Error())
			return nil
		}
		if d.IsDir() || filepath.Ext(path) != ".toml" {
			return nil
		}

		slashPath := filepath.ToSlash(path)
		if !strings.Contains(slashPath, "/commands/") {
			return nil
		}

		current, err := os.ReadFile(path)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", path, err))
			return nil
		}
		if isValidTOML(current) {
			return nil
		}

		relPath, err := filepath.Rel(homePath, path)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", path, err))
			return nil
		}

		backup := readGeminiBackupTOML(homePath, relPath)
		if len(backup) == 0 {
			quarantinePath := path + ".loom-quarantined"
			_ = os.Remove(quarantinePath)
			if err := os.Rename(path, quarantinePath); err != nil {
				errs = append(errs, fmt.Sprintf("%s invalid and no backup available: %v", relPath, err))
			}
			return nil
		}

		mode := os.FileMode(0o644)
		if info, err := os.Stat(path); err == nil {
			mode = info.Mode().Perm()
		}
		if err := writeFileAtomic(path, backup, mode); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", relPath, err))
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func readGeminiBackupJSON(homePath, relPath string) []byte {
	return readProfileBackupFile(homePath, "gemini", relPath, isValidGeminiJSONObject)
}

func readGeminiBackupTOML(homePath, relPath string) []byte {
	return readProfileBackupFile(homePath, "gemini", relPath, isValidTOML)
}

func isValidGeminiTrustedFolders(data []byte) bool {
	if len(trimmedBytes(data)) == 0 {
		return false
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return false
	}
	for k, v := range obj {
		if strings.TrimSpace(k) == "" {
			return false
		}
		if _, ok := v.(string); !ok {
			return false
		}
	}
	return true
}

func isValidGeminiJSONObject(data []byte) bool {
	if len(trimmedBytes(data)) == 0 {
		return false
	}
	var obj map[string]any
	return json.Unmarshal(data, &obj) == nil
}

// pruneGeminiMCPExtensions scans ~/.gemini/extensions/*/gemini-extension.json
// and removes any extension directory whose manifest defines mcpServers.
// These are redundant because the loom proxy covers all MCP needs.
// Returns the list of pruned extension names.
func pruneGeminiMCPExtensions(homePath string) ([]string, error) {
	extensionsDir := filepath.Join(homePath, "extensions")
	if !Exists(extensionsDir) {
		return nil, nil
	}

	entries, err := os.ReadDir(extensionsDir)
	if err != nil {
		return nil, fmt.Errorf("read extensions dir: %w", err)
	}

	var pruned []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		manifestPath := filepath.Join(extensionsDir, name, "gemini-extension.json")
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			continue // No manifest, skip
		}

		var manifest map[string]json.RawMessage
		if err := json.Unmarshal(data, &manifest); err != nil {
			continue
		}

		if _, hasMCP := manifest["mcpServers"]; !hasMCP {
			continue // No MCP servers, preserve this extension
		}

		extDir := filepath.Join(extensionsDir, name)
		if err := os.RemoveAll(extDir); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not prune extension %s: %v\n", name, err)
			continue
		}
		pruned = append(pruned, name)
		fmt.Printf("Pruned Gemini MCP extension: %s\n", name)
	}

	if len(pruned) > 0 {
		if err := removeFromExtensionEnablement(homePath, pruned); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not update extension-enablement.json: %v\n", err)
		}
	}

	return pruned, nil
}

// removeFromExtensionEnablement removes pruned extension names from
// ~/.gemini/extensions/extension-enablement.json.
func removeFromExtensionEnablement(homePath string, names []string) error {
	path := filepath.Join(homePath, "extensions", "extension-enablement.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil // Invalid JSON, leave as-is
	}

	changed := false
	for _, name := range names {
		if _, ok := obj[name]; ok {
			delete(obj, name)
			changed = true
		}
	}

	if !changed {
		return nil
	}

	updated, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(updated, '\n'), 0o600)
}

// filterPrunedExtensions removes pruned extension manifests from the pre-sync
// snapshot so ensureGeminiExtensionManifests() doesn't restore them.
func filterPrunedExtensions(snapshot geminiConfigSnapshot, pruned []string) geminiConfigSnapshot {
	if len(pruned) == 0 || len(snapshot.extensionManifests) == 0 {
		return snapshot
	}

	prunedSet := make(map[string]struct{}, len(pruned))
	for _, name := range pruned {
		prunedSet[name] = struct{}{}
	}

	filtered := make(map[string][]byte, len(snapshot.extensionManifests))
	for relPath, data := range snapshot.extensionManifests {
		// relPath is like "extensions/<name>/gemini-extension.json"
		parts := strings.Split(filepath.ToSlash(relPath), "/")
		if len(parts) >= 2 {
			extName := parts[1] // "extensions/<name>/..."
			if _, isPruned := prunedSet[extName]; isPruned {
				continue
			}
		}
		filtered[relPath] = data
	}

	snapshot.extensionManifests = filtered
	return snapshot
}
