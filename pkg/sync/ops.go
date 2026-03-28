// ops.go — Top-level sync dispatch: SyncToHome, SyncAll, Regenerate, Backup, Validate,
// and shared discovery helpers. Platform-specific logic lives in ops_gemini.go,
// ops_claude.go, ops_codex.go. Shared utilities live in ops_helpers.go.
package sync

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

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
			filepath.Join(root, "services", "loom-core", "mcp", "context", filename),
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

	// Copy generated file to the profile destination.
	genPath := filepath.Join(tmpDir, p.GeneratorTarget, p.GeneratedFile)
	if !Exists(genPath) {
		return fmt.Errorf("generated file not found: %s", genPath)
	}

	destRoot := m.ResolveRepoPath(p)
	primaryDestName := p.GeneratedFile
	if p.GeneratedDirectToHome {
		m.cleanRepoGenerated(p)
		destRoot = m.ResolveHomePath(p)
		primaryDestName = primaryHomeGeneratedFile(p)
	}
	if err := os.MkdirAll(destRoot, 0755); err != nil {
		return err
	}

	destFile := filepath.Join(destRoot, primaryDestName)
	if err := CopyFile(genPath, destFile); err != nil {
		return err
	}

	fmt.Printf("Updated %s\n", destFile)

	// Copy extra generated files (e.g. settings.json for hooks).
	for _, extra := range p.ExtraGeneratedFiles {
		extraGen := filepath.Join(tmpDir, p.GeneratorTarget, extra)
		if Exists(extraGen) {
			extraDest := filepath.Join(destRoot, extra)
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
		if p.SkillsTarget == "codex" {
			outputDir = ""
		}
	}

	gen, err := skills.NewGenerator(skills.GeneratorOptions{
		RegistryPath:  skillsRegPath,
		Target:        p.SkillsTarget,
		RepoRoot:      m.RepoRoot,
		WorkspaceRoot: m.WorkspaceRoot,
		OutputDir:     outputDir,
		GeminiSkillsHome: func() string {
			if p.SkillsTarget != "gemini" {
				return ""
			}
			return p.SkillsHomePath
		}(),
		// Codex defaults to ~/.codex/skills, but callers can still override the
		// skills root when they explicitly need a repo-local mirror.
		CodexSkillsDir: func() string {
			if p.SkillsTarget != "codex" {
				return ""
			}
			if p.SkillsDirectToHome {
				return filepath.Join(m.ResolveHomePath(p), "skills")
			}
			return filepath.Join(repoPath, "skills")
		}(),
		CodexRootDir: func() string {
			if p.SkillsTarget != "codex" {
				return ""
			}
			if p.SkillsDirectToHome {
				return m.ResolveHomePath(p)
			}
			return repoPath
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
// Also cleans the workspace root if it differs from the repo root.
func (m *Manager) cleanRepoSkills(p *Profile) {
	m.cleanSkillsAt(m.ResolveRepoPath(p))

	// Also clean workspace root if different from repo root
	if m.WorkspaceRoot != "" && m.WorkspaceRoot != m.RepoRoot {
		wsPath := filepath.Join(m.WorkspaceRoot, p.RepoDir)
		if wsPath != m.ResolveHomePath(p) {
			m.cleanSkillsAt(wsPath)
		}
	}
}

// cleanSkillsAt removes stale skill files from the given directory.
func (m *Manager) cleanSkillsAt(dir string) {
	skillsDir := filepath.Join(dir, "skills")
	if Exists(skillsDir) {
		if err := os.RemoveAll(skillsDir); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not remove stale repo skills %s: %v\n", skillsDir, err)
		} else {
			fmt.Printf("Cleaned stale repo skills: %s\n", skillsDir)
		}
	}

	manifestPath := filepath.Join(dir, skills.ManifestFilename)
	if Exists(manifestPath) {
		if err := os.Remove(manifestPath); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not remove stale manifest %s: %v\n", manifestPath, err)
		}
	}

	for _, f := range []string{"instructions.md", "GEMINI.md"} {
		p := filepath.Join(dir, f)
		if Exists(p) {
			if err := os.Remove(p); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not remove stale %s %s: %v\n", f, p, err)
			}
		}
	}
}

// cleanRepoGenerated removes stale generated config files from the repo
// directory when a profile now writes them directly to home.
// Also cleans the workspace root if it differs from the repo root.
func (m *Manager) cleanRepoGenerated(p *Profile) {
	m.cleanGeneratedAt(m.ResolveRepoPath(p), p)

	// Also clean workspace root if different from repo root
	if m.WorkspaceRoot != "" && m.WorkspaceRoot != m.RepoRoot {
		wsPath := filepath.Join(m.WorkspaceRoot, p.RepoDir)
		if wsPath != m.ResolveHomePath(p) {
			m.cleanGeneratedAt(wsPath, p)
		}
	}
}

// cleanGeneratedAt removes stale generated config files from the given directory.
func (m *Manager) cleanGeneratedAt(dir string, p *Profile) {
	files := []string{p.GeneratedFile}
	files = append(files, p.ExtraGeneratedFiles...)

	for _, rel := range files {
		if rel == "" {
			continue
		}
		path := filepath.Join(dir, rel)
		if !Exists(path) {
			continue
		}
		if err := os.Remove(path); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not remove stale generated file %s: %v\n", path, err)
		}
	}
}

// SyncSkills generates and syncs skill files for a profile.
func (m *Manager) SyncSkills(profileName string, repoOnly bool) error {
	p, err := m.GetProfile(profileName)
	if err != nil {
		return err
	}
	if p.SkillsTarget == "" {
		return fmt.Errorf("profile %s does not have a skills target", profileName)
	}

	if repoOnly && p.SkillsDirectToHome {
		fmt.Printf("Skipping skill sync for %s (repo-only incompatible with home-only skills)\n", profileName)
		return nil
	}

	// Generate skills
	if err := m.regenerateSkills(p); err != nil {
		return err
	}

	if repoOnly {
		fmt.Printf("Skipping skill sync to home for %s (repo-only)\n", profileName)
		return nil
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
