package sync

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/crb2nu/loom/pkg/generator"
	"github.com/crb2nu/loom/pkg/registry"
	"github.com/crb2nu/loom/pkg/skills"
	"github.com/crb2nu/loom/pkg/validator"
)

func ancestorRoots(start string) []string {
	var roots []string
	current := filepath.Clean(start)
	for {
		roots = append(roots, current)
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return roots
}

func discoverWorkspaceContextFile(repoRoot, filename string) string {
	seen := map[string]struct{}{}
	for _, root := range ancestorRoots(repoRoot) {
		candidates := []string{
			filepath.Join(root, "mcp", "context", filename),
			filepath.Join(root, "platform", "gitops", "mcp", "context", filename),
		}
		for _, candidate := range candidates {
			if _, ok := seen[candidate]; ok {
				continue
			}
			seen[candidate] = struct{}{}
			if Exists(candidate) {
				return candidate
			}
		}
	}
	return ""
}

func discoverRegistryPath(repoRoot string) string {
	// Prefer workspace-local registries first (repo overrides + ancestor workspace
	// roots) before falling back to user defaults.
	if local := discoverWorkspaceContextFile(repoRoot, "registry.yaml"); local != "" {
		return local
	}
	return registry.FindRegistryOrDefault(filepath.Join(repoRoot, "mcp", "context", "registry.yaml"))
}

// SyncToHome syncs configuration from repo to home directory.
func (m *Manager) SyncToHome(profileName string, backup bool, regen bool, repoOnly bool, hubMode bool, hubURL string, loomMode bool, loomBinary string, resolveSecrets bool) error {
	p, err := m.GetProfile(profileName)
	if err != nil {
		return err
	}

	repoPath := m.ResolveRepoPath(p)
	homePath := m.ResolveHomePath(p)
	var geminiSnapshot geminiConfigSnapshot
	var geminiAuthSnapshot geminiAuthSnapshot
	var claudeSnapshot claudeConfigSnapshot
	var codexSnapshot codexConfigSnapshot
	switch p.Name {
	case "gemini":
		geminiSnapshot = readGeminiConfigSnapshot(homePath)
		geminiAuthSnapshot = readGeminiAuthSnapshot(homePath)
	case "claude":
		claudeSnapshot = readClaudeConfigSnapshot(homePath)
	case "codex":
		codexSnapshot = readCodexConfigSnapshot(homePath)
	}

	if regen {
		if err := m.Regenerate(p, hubMode, hubURL, loomMode, loomBinary, resolveSecrets); err != nil {
			return fmt.Errorf("regenerate failed: %w", err)
		}
	}

	if repoOnly {
		fmt.Printf("Skipping sync to home for %s (repo-only)\n", profileName)
		return nil
	}

	if !Exists(repoPath) {
		return fmt.Errorf("repo directory not found: %s", repoPath)
	}

	// Validate config before sync
	if p.GeneratorTarget != "" {
		configPath := filepath.Join(repoPath, p.GeneratedFile)
		if Exists(configPath) {
			v := validator.New(m.RepoRoot, m.HomeDir)
			result, err := v.ValidateFile(p.GeneratorTarget, configPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: validation failed for %s: %v\n", configPath, err)
			} else if result.HasErrors() || result.HasWarnings() {
				for _, verr := range result.Errors {
					if verr.Severity == validator.SeverityError {
						fmt.Fprintf(os.Stderr, "ERROR [%s] %s: %s\n", p.Name, verr.Field, verr.Message)
					} else {
						fmt.Fprintf(os.Stderr, "WARN  [%s] %s: %s\n", p.Name, verr.Field, verr.Message)
					}
				}
			}
		}
	}

	fmt.Printf("Syncing %s -> %s\n", repoPath, homePath)

	if backup && Exists(homePath) {
		if !p.SyncGeneratedOnly || (p.GeneratedFile != "" && Exists(filepath.Join(homePath, primaryHomeGeneratedFile(p)))) {
			if err := m.Backup(profileName, "home"); err != nil {
				return fmt.Errorf("backup failed: %w", err)
			}
		}
	}

	if p.SyncGeneratedOnly {
		if p.GeneratedFile == "" {
			return fmt.Errorf("profile %s has no generated file", p.Name)
		}
		srcFile := filepath.Join(repoPath, p.GeneratedFile)
		if !Exists(srcFile) {
			return fmt.Errorf("generated file not found: %s", srcFile)
		}
		if err := os.MkdirAll(homePath, 0755); err != nil {
			return fmt.Errorf("create home dir: %w", err)
		}
		dstFile := filepath.Join(homePath, primaryHomeGeneratedFile(p))
		if err := CopyFile(srcFile, dstFile); err != nil {
			return err
		}
		// Sync extra generated files (e.g. settings.json for hooks).
		for _, extra := range p.ExtraGeneratedFiles {
			extraSrc := filepath.Join(repoPath, extra)
			if Exists(extraSrc) {
				extraDst := filepath.Join(homePath, extra)
				if err := CopyFile(extraSrc, extraDst); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: could not sync %s: %v\n", extra, err)
				}
			}
		}
	} else {
		if err := CopyDir(repoPath, homePath, p.Excludes); err != nil {
			return err
		}
	}

	// Sync skill files from manifest (skip when skills are generated directly to home)
	if p.SkillsManifest != "" && !p.SkillsDirectToHome {
		manifest, _ := skills.ReadManifest(repoPath)
		if manifest != nil && len(manifest.Generated) > 0 {
			for _, relPath := range manifest.Generated {
				srcFile := filepath.Join(repoPath, relPath)
				dstFile := filepath.Join(homePath, relPath)
				if Exists(srcFile) {
					if err := os.MkdirAll(filepath.Dir(dstFile), 0755); err != nil {
						fmt.Fprintf(os.Stderr, "Warning: could not create dir for skill file %s: %v\n", relPath, err)
						continue
					}
					if err := CopyFile(srcFile, dstFile); err != nil {
						fmt.Fprintf(os.Stderr, "Warning: could not sync skill file %s: %v\n", relPath, err)
					}
				}
			}
			// Also copy the manifest itself
			manifestSrc := filepath.Join(repoPath, skills.ManifestFilename)
			if Exists(manifestSrc) {
				if err := CopyFile(manifestSrc, filepath.Join(homePath, skills.ManifestFilename)); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: could not sync manifest: %v\n", err)
				}
			}
			fmt.Printf("Synced %d skill files for %s\n", len(manifest.Generated), p.Name)
		}
	}

	// Also copy to workspace directory if specified (e.g., .vscode/ for local MCP config)
	if p.WorkspaceDir != "" {
		workspacePath := filepath.Join(m.RepoRoot, p.WorkspaceDir)
		// Only copy the generated file(s), not the whole directory
		srcFile := filepath.Join(repoPath, p.GeneratedFile)
		if Exists(srcFile) {
			if err := os.MkdirAll(workspacePath, 0755); err != nil {
				return fmt.Errorf("create workspace dir: %w", err)
			}
			dstFile := filepath.Join(workspacePath, p.GeneratedFile)
			if err := CopyFile(srcFile, dstFile); err != nil {
				return fmt.Errorf("copy to workspace: %w", err)
			}
			fmt.Printf("Also copied to %s\n", dstFile)
		}
		for _, extra := range p.ExtraGeneratedFiles {
			extraSrc := filepath.Join(repoPath, extra)
			if Exists(extraSrc) {
				extraDst := filepath.Join(workspacePath, extra)
				if err := CopyFile(extraSrc, extraDst); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: could not copy %s to workspace: %v\n", extra, err)
				}
			}
		}
	}

	switch p.Name {
	case "gemini":
		// Prune extensions that define mcpServers (redundant with loom proxy).
		pruned, pruneErr := pruneGeminiMCPExtensions(homePath)
		if pruneErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: extension pruning failed: %v\n", pruneErr)
		}
		geminiSnapshot = filterPrunedExtensions(geminiSnapshot, pruned)
		if err := ensureGeminiConfigFiles(homePath, geminiSnapshot); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not verify Gemini config files: %v\n", err)
		}
		if err := ensureGeminiAuthFiles(homePath, geminiAuthSnapshot); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not preserve Gemini auth files: %v\n", err)
		}
	case "claude":
		if err := ensureClaudeConfigFiles(homePath, claudeSnapshot); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not verify Claude config files: %v\n", err)
		}
	case "codex":
		if err := ensureCodexConfigFiles(homePath, codexSnapshot); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not verify Codex config files: %v\n", err)
		}
	}

	return nil
}

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

type claudeConfigSnapshot struct {
	mcp      []byte
	settings []byte
}

type codexConfigSnapshot struct {
	config []byte
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

func readClaudeConfigSnapshot(homePath string) claudeConfigSnapshot {
	return claudeConfigSnapshot{
		mcp:      readJSONSnapshot(filepath.Join(homePath, "mcp.json")),
		settings: readJSONSnapshot(filepath.Join(homePath, "settings.json")),
	}
}

func readCodexConfigSnapshot(homePath string) codexConfigSnapshot {
	return codexConfigSnapshot{
		config: readTOMLSnapshot(filepath.Join(homePath, "config.toml")),
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

func ensureClaudeConfigFiles(homePath string, snapshot claudeConfigSnapshot) error {
	var errs []string

	if err := ensureProfileJSONFile(homePath, "claude", "mcp.json", snapshot.mcp, []byte("{\"mcpServers\":{}}\n")); err != nil {
		errs = append(errs, fmt.Sprintf("mcp.json: %v", err))
	}
	if err := ensureProfileJSONFile(homePath, "claude", "settings.json", snapshot.settings, []byte("{}\n")); err != nil {
		errs = append(errs, fmt.Sprintf("settings.json: %v", err))
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func ensureCodexConfigFiles(homePath string, snapshot codexConfigSnapshot) error {
	if err := ensureProfileTOMLFile(homePath, "codex", "config.toml", snapshot.config, []byte("[mcp_servers]\n")); err != nil {
		return fmt.Errorf("config.toml: %w", err)
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

func isValidGeminiTrustedFolders(data []byte) bool {
	if len(bytes.TrimSpace(data)) == 0 {
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
	if len(bytes.TrimSpace(data)) == 0 {
		return false
	}
	var obj map[string]any
	return json.Unmarshal(data, &obj) == nil
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

// SyncAll syncs all profiles.
// resolveSecrets is a pointer: nil means use per-profile defaults.
func (m *Manager) SyncAll(backup bool, regen bool, repoOnly bool, hubMode bool, hubURL string, loomMode bool, loomBinary string, resolveSecrets *bool, loomModeExplicit bool) error {
	var names []string
	for name := range m.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		p := m.Profiles[name]
		// Apply per-profile defaults when flags were not explicitly set
		effectiveLoomMode := loomMode
		if !loomModeExplicit {
			effectiveLoomMode = p.DefaultLoomMode
		}
		effectiveResolve := false
		if resolveSecrets != nil {
			effectiveResolve = *resolveSecrets
		} else {
			effectiveResolve = p.DefaultResolveSecrets
		}

		if err := m.SyncToHome(name, backup, regen, repoOnly, hubMode, hubURL, effectiveLoomMode, loomBinary, effectiveResolve); err != nil {
			return fmt.Errorf("sync %s: %w", name, err)
		}
	}
	return nil
}

// SyncAllProjects propagates the canonical hooks from the current repo's
// settings.json into all other workspace projects that have a matching
// <profileRepoDir>/settings.json. Only the "hooks" key is replaced; other
// top-level keys (permissions, mcpServers, etc.) are preserved.
func (m *Manager) SyncAllProjects(profileName, workspaceRoot string, skipWorktrees, dryRun bool) (int, error) {
	p, err := m.GetProfile(profileName)
	if err != nil {
		return 0, err
	}

	// Verify this profile has settings.json as an extra generated file
	hasSettings := false
	for _, f := range p.ExtraGeneratedFiles {
		if f == "settings.json" {
			hasSettings = true
			break
		}
	}
	if !hasSettings {
		return 0, fmt.Errorf("profile %s does not generate settings.json", profileName)
	}

	// Read canonical settings.json from the current repo
	canonicalPath := filepath.Join(m.ResolveRepoPath(p), "settings.json")
	canonicalData, err := os.ReadFile(canonicalPath)
	if err != nil {
		return 0, fmt.Errorf("read canonical settings: %w", err)
	}

	canonicalHooks, err := ExtractHooksFromSettings(canonicalData)
	if err != nil {
		return 0, fmt.Errorf("extract hooks from canonical settings: %w", err)
	}
	if canonicalHooks == nil {
		return 0, fmt.Errorf("canonical settings.json has no hooks key")
	}

	// Discover projects
	projects, err := DiscoverProjects(workspaceRoot, p.RepoDir, skipWorktrees)
	if err != nil {
		return 0, fmt.Errorf("discover projects: %w", err)
	}

	updated := 0
	for _, projRoot := range projects {
		// Skip the current repo
		if filepath.Clean(projRoot) == filepath.Clean(m.RepoRoot) {
			continue
		}

		settingsPath := filepath.Join(projRoot, p.RepoDir, "settings.json")
		existing, _ := os.ReadFile(settingsPath)

		merged, changed, err := MergeHooksIntoSettings(existing, canonicalHooks)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: merge failed for %s: %v\n", settingsPath, err)
			continue
		}

		rel, _ := filepath.Rel(workspaceRoot, projRoot)
		if !changed {
			fmt.Printf("  %-40s already up-to-date\n", rel)
			continue
		}

		if dryRun {
			action := "would update"
			if len(existing) == 0 {
				action = "would create"
			}
			fmt.Printf("  %-40s %s\n", rel, action)
			updated++
			continue
		}

		if err := writeFileAtomic(settingsPath, merged, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: write failed for %s: %v\n", settingsPath, err)
			continue
		}

		action := "updated"
		if len(existing) == 0 {
			action = "created"
		}
		fmt.Printf("  %-40s %s\n", rel, action)
		updated++
	}

	return updated, nil
}

// Regenerate generates the configuration for a profile and updates the repo directory.
func (m *Manager) Regenerate(p *Profile, hubMode bool, hubURL string, loomMode bool, loomBinary string, resolveSecrets bool) error {
	if p.GeneratorTarget == "" {
		return fmt.Errorf("profile %s has no generator target", p.Name)
	}

	// Load registry - prefer local override, then home directory
	regPath := discoverRegistryPath(m.RepoRoot)
	reg, err := registry.LoadWithDefaults(regPath)
	if err != nil {
		return fmt.Errorf("load registry from %s: %w", regPath, err)
	}
	fmt.Printf("Using registry: %s\n", regPath)
	if err := syncAgentsSafetyPolicy(m.RepoRoot, reg); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: AGENTS.md safety policy sync failed: %v\n", err)
	}

	// Create temp dir
	tmpDir, err := os.MkdirTemp("", "loom-gen")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	// Generate
	fmt.Printf("Regenerating config for %s...\n", p.Name)
	err = generator.GenerateConfigsWithPath(reg, regPath, tmpDir, []string{p.GeneratorTarget}, hubMode, hubURL, loomMode, loomBinary, resolveSecrets)
	if err != nil {
		return err
	}

	// Copy generated file to repo dir
	genPath := filepath.Join(tmpDir, p.GeneratorTarget, p.GeneratedFile)
	if !Exists(genPath) {
		// Try without target subdir (some generators might behave differently? No, configs.go uses target subdir)
		// Wait, configs.go: destDir := filepath.Join(outputDir, target) or "vscode" or "claude_desktop"
		return fmt.Errorf("generated file not found: %s", genPath)
	}

	repoPath := m.ResolveRepoPath(p)
	if err := os.MkdirAll(repoPath, 0755); err != nil {
		return err
	}

	destFile := filepath.Join(repoPath, p.GeneratedFile)
	if err := CopyFile(genPath, destFile); err != nil {
		return err
	}

	fmt.Printf("Updated %s\n", destFile)

	// Copy extra generated files (e.g. settings.json for hooks).
	for _, extra := range p.ExtraGeneratedFiles {
		extraGen := filepath.Join(tmpDir, p.GeneratorTarget, extra)
		if Exists(extraGen) {
			extraDest := filepath.Join(repoPath, extra)
			if err := CopyFile(extraGen, extraDest); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not copy extra file %s: %v\n", extra, err)
			} else {
				fmt.Printf("Updated %s\n", extraDest)
			}
		}
	}

	// Generate skills if the profile has a skills target (unless SkipSkills is set)
	if p.SkillsTarget != "" && !m.SkipSkills {
		if err := m.regenerateSkills(p); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: skills generation failed for %s: %v\n", p.Name, err)
		}
	}

	return nil
}

// regenerateSkills generates skill files for a profile from the skills registry.
func (m *Manager) regenerateSkills(p *Profile) error {
	skillsRegPath := discoverSkillsRegistryPath(m.RepoRoot)
	if skillsRegPath == "" {
		return nil // No skills registry found, skip silently
	}

	// When skills go directly to home, clean stale repo copies first.
	if p.SkillsDirectToHome {
		m.cleanRepoSkills(p)
	}

	repoPath := m.ResolveRepoPath(p)
	fmt.Printf("Generating skills for %s from %s...\n", p.Name, skillsRegPath)

	// When SkillsDirectToHome, generate directly into the home directory
	// so skills exist in only one place (avoiding duplication warnings).
	outputDir := ""
	if p.SkillsDirectToHome {
		outputDir = m.ResolveHomePath(p)
	}

	gen, err := skills.NewGenerator(skills.GeneratorOptions{
		RegistryPath:  skillsRegPath,
		Target:        p.SkillsTarget,
		RepoRoot:      m.RepoRoot,
		WorkspaceRoot: m.RepoRoot,
		OutputDir:     outputDir,
		GeminiSkillsHome: func() string {
			if p.SkillsTarget != "gemini" {
				return ""
			}
			return p.SkillsHomePath
		}(),
		// Codex normally generates directly into ~/.codex/skills; for sync we generate
		// into the repo's .codex/ so status + sync can verify and propagate changes.
		CodexSkillsDir: func() string {
			if p.SkillsTarget != "codex" {
				return ""
			}
			return filepath.Join(repoPath, "skills")
		}(),
	})
	if err != nil {
		return fmt.Errorf("create skills generator: %w", err)
	}

	if err := gen.Generate(); err != nil {
		return fmt.Errorf("generate skills: %w", err)
	}

	// Read manifest from the directory where skills were generated.
	manifestDir := repoPath
	if p.SkillsDirectToHome {
		manifestDir = m.ResolveHomePath(p)
	}
	manifest, _ := skills.ReadManifest(manifestDir)
	if manifest != nil {
		fmt.Printf("Generated %d skill files for %s\n", len(manifest.Generated), p.Name)
	}

	return nil
}

// cleanRepoSkills removes stale skill files from the repo directory when
// skills are generated directly to home (SkillsDirectToHome).
func (m *Manager) cleanRepoSkills(p *Profile) {
	repoPath := m.ResolveRepoPath(p)

	// Remove the skills directory (e.g. <repo>/.gemini/skills/)
	skillsDir := filepath.Join(repoPath, "skills")
	if Exists(skillsDir) {
		if err := os.RemoveAll(skillsDir); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not remove stale repo skills %s: %v\n", skillsDir, err)
		} else {
			fmt.Printf("Cleaned stale repo skills: %s\n", skillsDir)
		}
	}

	// Remove the manifest file
	manifestPath := filepath.Join(repoPath, skills.ManifestFilename)
	if Exists(manifestPath) {
		if err := os.Remove(manifestPath); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not remove stale manifest %s: %v\n", manifestPath, err)
		}
	}

	// Remove instructions.md/GEMINI.md if it exists (generated alongside skills)
	for _, f := range []string{"instructions.md", "GEMINI.md"} {
		p := filepath.Join(repoPath, f)
		if Exists(p) {
			if err := os.Remove(p); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not remove stale %s %s: %v\n", f, p, err)
			}
		}
	}
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

// SyncSkills generates and syncs skill files for a profile.
func (m *Manager) SyncSkills(profileName string) error {
	p, err := m.GetProfile(profileName)
	if err != nil {
		return err
	}
	if p.SkillsTarget == "" {
		return fmt.Errorf("profile %s does not have a skills target", profileName)
	}

	// Generate skills
	if err := m.regenerateSkills(p); err != nil {
		return err
	}

	// When skills are generated directly to home, no copy step needed.
	if p.SkillsDirectToHome {
		homePath := m.ResolveHomePath(p)
		manifest, _ := skills.ReadManifest(homePath)
		if manifest != nil {
			fmt.Printf("Generated %d skill files directly to %s\n", len(manifest.Generated), homePath)
		}
		return nil
	}

	// Sync skill files from repo to home
	repoPath := m.ResolveRepoPath(p)
	homePath := m.ResolveHomePath(p)

	manifest, _ := skills.ReadManifest(repoPath)
	if manifest == nil || len(manifest.Generated) == 0 {
		fmt.Println("No skill files to sync")
		return nil
	}

	for _, relPath := range manifest.Generated {
		srcFile := filepath.Join(repoPath, relPath)
		dstFile := filepath.Join(homePath, relPath)
		if Exists(srcFile) {
			if err := os.MkdirAll(filepath.Dir(dstFile), 0755); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not create dir for %s: %v\n", relPath, err)
				continue
			}
			if err := CopyFile(srcFile, dstFile); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not sync %s: %v\n", relPath, err)
			}
		}
	}

	// Copy manifest
	manifestSrc := filepath.Join(repoPath, skills.ManifestFilename)
	if Exists(manifestSrc) {
		if err := CopyFile(manifestSrc, filepath.Join(homePath, skills.ManifestFilename)); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not sync manifest: %v\n", err)
		}
	}

	fmt.Printf("Synced %d skill files for %s\n", len(manifest.Generated), profileName)
	return nil
}

// discoverSkillsRegistryPath locates the skills-registry.yaml file.
func discoverSkillsRegistryPath(repoRoot string) string {
	if local := discoverWorkspaceContextFile(repoRoot, "skills-registry.yaml"); local != "" {
		return local
	}
	// Try the skills package finder as fallback
	if path, found := skills.FindRegistry(); found {
		return path
	}
	return ""
}

// PullFromHome pulls configuration from home directory to repo.
func (m *Manager) PullFromHome(profileName string, backup bool) error {
	p, err := m.GetProfile(profileName)
	if err != nil {
		return err
	}

	repoPath := m.ResolveRepoPath(p)
	homePath := m.ResolveHomePath(p)

	if !Exists(homePath) {
		return fmt.Errorf("home directory not found: %s", homePath)
	}

	fmt.Printf("Pulling %s -> %s\n", homePath, repoPath)

	if backup && Exists(repoPath) {
		if !p.SyncGeneratedOnly || (p.GeneratedFile != "" && Exists(filepath.Join(repoPath, p.GeneratedFile))) {
			if err := m.Backup(profileName, "repo"); err != nil {
				return fmt.Errorf("backup failed: %w", err)
			}
		}
	}

	if p.SyncGeneratedOnly {
		if p.GeneratedFile == "" {
			return fmt.Errorf("profile %s has no generated file", p.Name)
		}
		srcFile := filepath.Join(homePath, primaryHomeGeneratedFile(p))
		if !Exists(srcFile) {
			return fmt.Errorf("generated file not found: %s", srcFile)
		}
		if err := os.MkdirAll(repoPath, 0755); err != nil {
			return fmt.Errorf("create repo dir: %w", err)
		}
		dstFile := filepath.Join(repoPath, p.GeneratedFile)
		return CopyFile(srcFile, dstFile)
	}

	return CopyDir(homePath, repoPath, p.Excludes)
}

// Backup creates a timestamped backup of the profile configuration.
func (m *Manager) Backup(profileName string, source string) error {
	p, err := m.GetProfile(profileName)
	if err != nil {
		return err
	}

	var srcPath string
	if source == "repo" {
		srcPath = m.ResolveRepoPath(p)
	} else {
		srcPath = m.ResolveHomePath(p)
	}

	if !Exists(srcPath) {
		return fmt.Errorf("source not found: %s", srcPath)
	}

	// Determine backup dir (usually ~/.<profile>/backups)
	// But scripts put them in ~/.codex/backups for codex.
	// Let's standardize on ~/.config/loom/backups/<profile> or keep it local?
	// The bash script used $HOME/.codex/backups.

	backupRoot := filepath.Join(m.ResolveHomePath(p), "backups")
	if p.SyncGeneratedOnly {
		// Avoid writing backups into large application config directories (VS Code, Claude Desktop, etc.)
		backupRoot = filepath.Join(m.HomeDir, ".config", "loom", "backups", p.Name)
	}

	timestamp := time.Now().Format("20060102_150405")
	backupPath := filepath.Join(backupRoot, fmt.Sprintf("%s_%s_%s", p.Name, source, timestamp))

	fmt.Printf("Creating backup at %s\n", backupPath)

	if p.SyncGeneratedOnly {
		if p.GeneratedFile == "" {
			return fmt.Errorf("profile %s has no generated file", p.Name)
		}
		if err := os.MkdirAll(backupPath, 0755); err != nil {
			return err
		}
		fileName := p.GeneratedFile
		if source != "repo" {
			fileName = primaryHomeGeneratedFile(p)
		}
		srcFile := filepath.Join(srcPath, fileName)
		if !Exists(srcFile) {
			return fmt.Errorf("generated file not found: %s", srcFile)
		}
		return CopyFile(srcFile, filepath.Join(backupPath, fileName))
	}

	// Merge default excludes with profile excludes
	excludes := []string{"backups", "sessions"}
	excludes = append(excludes, p.Excludes...)

	return CopyDir(srcPath, backupPath, excludes)
}

// Validate checks if the configuration is valid using schema and runtime validation.
func (m *Manager) Validate(profileName string) error {
	p, err := m.GetProfile(profileName)
	if err != nil {
		return err
	}

	homePath := m.ResolveHomePath(p)
	if !Exists(homePath) {
		return fmt.Errorf("config directory not found: %s", homePath)
	}

	// Determine config file based on profile
	var configFile string
	var target string
	switch p.GeneratorTarget {
	case "codex", "kilocode", "gemini":
		configFile = filepath.Join(homePath, "config.toml")
		target = p.GeneratorTarget
	case "claude_desktop":
		configFile = filepath.Join(homePath, "claude_desktop_config.json")
		target = "claude_desktop"
	case "claude", "vscode", "antigravity":
		fileName := primaryHomeGeneratedFile(p)
		if fileName == "" {
			fileName = "mcp.json"
		}
		configFile = filepath.Join(homePath, fileName)
		target = p.GeneratorTarget
	default:
		// Fallback: check for known config files
		if Exists(filepath.Join(homePath, "config.toml")) {
			configFile = filepath.Join(homePath, "config.toml")
			target = "codex"
		} else if Exists(filepath.Join(homePath, "mcp_config.json")) {
			configFile = filepath.Join(homePath, "mcp_config.json")
			target = "antigravity"
		} else if Exists(filepath.Join(homePath, "mcp.json")) {
			configFile = filepath.Join(homePath, "mcp.json")
			target = "claude"
		} else if Exists(filepath.Join(homePath, "claude_desktop_config.json")) {
			configFile = filepath.Join(homePath, "claude_desktop_config.json")
			target = "claude_desktop"
		}
	}

	if configFile == "" || !Exists(configFile) {
		return fmt.Errorf("no configuration file found in %s", homePath)
	}

	fmt.Printf("Validating %s...\n", configFile)

	// Perform schema and runtime validation
	v := validator.New(m.RepoRoot, m.HomeDir)
	result, err := v.ValidateFile(target, configFile)
	if err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	// Print validation results
	if result.Valid && !result.HasWarnings() {
		fmt.Printf("✓ Configuration is valid\n")
		return nil
	}

	for _, verr := range result.Errors {
		if verr.Severity == validator.SeverityError {
			fmt.Fprintf(os.Stderr, "ERROR %s: %s\n", verr.Field, verr.Message)
		} else {
			fmt.Fprintf(os.Stderr, "WARN  %s: %s\n", verr.Field, verr.Message)
		}
	}

	if result.HasErrors() {
		return fmt.Errorf("configuration has %d error(s)", result.ErrorCount())
	}

	fmt.Printf("✓ Configuration is valid (with %d warning(s))\n", result.WarningCount())
	return nil
}
