// ops_sync.go — Core sync entry points: SyncToHome, SyncAll, SyncAllProjects,
// CleanAllProjectsGenerated, PullFromHome. These orchestrate syncing configuration
// between repo and home directories.
package sync

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/crb2nu/loom/pkg/skills"
	"github.com/crb2nu/loom/pkg/validator"
)

// SyncToHome syncs configuration from repo to home directory.
func (m *Manager) SyncToHome(profileName string, backup bool, regen bool, repoOnly bool, hubMode bool, hubURL string, loomMode bool, loomBinary string, resolveSecrets bool) error {
	p, err := m.GetProfile(profileName)
	if err != nil {
		return err
	}

	if repoOnly && (p.GeneratedDirectToHome || p.SkillsDirectToHome) {
		fmt.Printf("Skipping %s (repo-only incompatible with home-only generation)\n", profileName)
		return nil
	}

	repoPath := m.ResolveRepoPath(p)
	homePath := m.ResolveHomePath(p)
	var geminiSnap geminiConfigSnapshot
	var geminiAuthSnap geminiAuthSnapshot
	var claudeSnap claudeConfigSnapshot
	var codexSnap codexConfigSnapshot
	switch p.Name {
	case "gemini":
		geminiSnap = readGeminiConfigSnapshot(homePath)
		geminiAuthSnap = readGeminiAuthSnapshot(homePath)
	case "claude":
		claudeSnap = readClaudeConfigSnapshot(homePath)
	case "codex":
		codexSnap = readCodexConfigSnapshot(homePath)
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

	configBasePath := repoPath
	if p.GeneratedDirectToHome {
		configBasePath = homePath
	}

	// Direct-to-home profiles are already updated by Regenerate; skip repo->home copy.
	if p.GeneratedDirectToHome {
		if p.GeneratorTarget != "" {
			configPath := filepath.Join(configBasePath, primaryHomeGeneratedFile(p))
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

		if p.SkillsDirectToHome {
			manifest, _ := skills.ReadManifest(homePath)
			if manifest != nil && len(manifest.Generated) > 0 {
				fmt.Printf("Generated %d skill files directly to %s\n", len(manifest.Generated), homePath)
			}
		}
		return nil
	}

	if !Exists(repoPath) {
		return fmt.Errorf("repo directory not found: %s", repoPath)
	}

	// Validate config before sync
	if p.GeneratorTarget != "" {
		configPath := filepath.Join(configBasePath, p.GeneratedFile)
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
		hasHomeGenerated := false
		for _, rel := range homeGeneratedFiles(p) {
			if Exists(filepath.Join(homePath, rel)) {
				hasHomeGenerated = true
				break
			}
		}
		if !p.SyncGeneratedOnly || hasHomeGenerated {
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
			if !Exists(extraSrc) {
				continue
			}
			extraDst := filepath.Join(homePath, extra)

			if extra == "settings.json" {
				// Smart merge: hooks should only live at the user/home level.
				// Take non-hook keys (permissions) from repo, preserve home hooks
				// if repo copy has none (non-regen case).
				repoData, err := os.ReadFile(extraSrc)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Warning: could not read %s: %v\n", extra, err)
					continue
				}
				homeData, _ := os.ReadFile(extraDst)
				merged, _, err := MergeSettingsForHome(homeData, repoData)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Warning: could not merge %s: %v\n", extra, err)
					continue
				}
				if err := writeFileAtomic(extraDst, merged, 0o644); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: could not write %s: %v\n", extra, err)
				}
				// Strip hooks from repo copy to prevent duplicate execution.
				// Claude Code runs hooks from all ancestor settings.json files.
				if _, err := StripHooksFromFile(extraSrc); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: could not strip hooks from %s: %v\n", extraSrc, err)
				}
			} else {
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
		geminiSnap = filterPrunedExtensions(geminiSnap, pruned)
		if err := ensureGeminiConfigFiles(homePath, geminiSnap); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not verify Gemini config files: %v\n", err)
		}
		if err := ensureGeminiAuthFiles(homePath, geminiAuthSnap); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not preserve Gemini auth files: %v\n", err)
		}
	case "claude":
		if err := ensureClaudeConfigFiles(homePath, claudeSnap); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not verify Claude config files: %v\n", err)
		}
	case "codex":
		if err := ensureCodexConfigFiles(homePath, codexSnap); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not verify Codex config files: %v\n", err)
		}
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

// SyncAllProjects strips home-managed settings keys from all workspace projects
// that have a matching <profileRepoDir>/settings.json. Hooks and approval
// settings should live at the user level only, preventing duplicate execution
// and per-project approval drift across the workspace.
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
	if len(p.HomeManagedSettingsKeys) == 0 {
		return 0, nil
	}

	// Discover projects
	projects, err := DiscoverProjects(workspaceRoot, p.RepoDir, skipWorktrees)
	if err != nil {
		return 0, fmt.Errorf("discover projects: %w", err)
	}

	updated := 0
	for _, projRoot := range projects {
		settingsPath := filepath.Join(projRoot, p.RepoDir, "settings.json")
		existing, _ := os.ReadFile(settingsPath)
		if len(existing) == 0 {
			continue // No settings file — nothing to strip
		}

		merged, changed, err := StripSettingsKeys(existing, p.HomeManagedSettingsKeys...)
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
			fmt.Printf("  %-40s would strip home-managed settings\n", rel)
			updated++
			continue
		}

		if err := writeFileAtomic(settingsPath, merged, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: write failed for %s: %v\n", settingsPath, err)
			continue
		}

		fmt.Printf("  %-40s stripped home-managed settings\n", rel)
		updated++
	}

	return updated, nil
}

// CleanAllProjectsGenerated removes stale generated config files from
// workspace projects for profiles whose config is managed directly in the home
// directory. This keeps home-level config authoritative across projects.
func (m *Manager) CleanAllProjectsGenerated(profileName, workspaceRoot string, skipWorktrees, dryRun bool) (int, error) {
	p, err := m.GetProfile(profileName)
	if err != nil {
		return 0, err
	}
	if !p.GeneratedDirectToHome || p.GeneratedFile == "" {
		return 0, nil
	}

	projects, err := DiscoverProjectsWithFile(workspaceRoot, p.RepoDir, p.GeneratedFile, skipWorktrees)
	if err != nil {
		return 0, fmt.Errorf("discover projects: %w", err)
	}

	files := []string{p.GeneratedFile}
	files = append(files, p.ExtraGeneratedFiles...)

	updated := 0
	for _, projRoot := range projects {
		rel, _ := filepath.Rel(workspaceRoot, projRoot)
		removedAny := false

		for _, name := range files {
			if name == "" {
				continue
			}
			target := filepath.Join(projRoot, p.RepoDir, name)
			if !Exists(target) {
				continue
			}
			removedAny = true
			if dryRun {
				continue
			}
			if err := os.Remove(target); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: remove failed for %s: %v\n", target, err)
				continue
			}
		}

		if !removedAny {
			continue
		}
		if dryRun {
			fmt.Printf("  %-40s would remove stale generated config\n", rel)
		} else {
			fmt.Printf("  %-40s removed stale generated config\n", rel)
		}
		updated++
	}

	return updated, nil
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
