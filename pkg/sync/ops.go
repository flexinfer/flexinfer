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
		if !p.SyncGeneratedOnly || (p.GeneratedFile != "" && Exists(filepath.Join(homePath, p.GeneratedFile))) {
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
		dstFile := filepath.Join(homePath, p.GeneratedFile)
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

	// Sync skill files from manifest
	if p.SkillsManifest != "" {
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

// Regenerate generates the configuration for a profile and updates the repo directory.
func (m *Manager) Regenerate(p *Profile, hubMode bool, hubURL string, loomMode bool, loomBinary string, resolveSecrets bool) error {
	if p.GeneratorTarget == "" {
		return fmt.Errorf("profile %s has no generator target", p.Name)
	}

	// Load registry - prefer local override, then home directory
	regPath := discoverRegistryPath(m.RepoRoot)
	reg, err := registry.Load(regPath)
	if err != nil {
		return fmt.Errorf("load registry from %s: %w", regPath, err)
	}
	fmt.Printf("Using registry: %s\n", regPath)

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

	repoPath := m.ResolveRepoPath(p)
	fmt.Printf("Generating skills for %s from %s...\n", p.Name, skillsRegPath)

	gen, err := skills.NewGenerator(skills.GeneratorOptions{
		RegistryPath:  skillsRegPath,
		Target:        p.SkillsTarget,
		RepoRoot:      m.RepoRoot,
		WorkspaceRoot: m.RepoRoot,
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

	// Count generated files from manifest
	manifest, _ := skills.ReadManifest(repoPath)
	if manifest != nil {
		fmt.Printf("Generated %d skill files for %s\n", len(manifest.Generated), p.Name)
	}

	return nil
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
		srcFile := filepath.Join(homePath, p.GeneratedFile)
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
		srcFile := filepath.Join(srcPath, p.GeneratedFile)
		if !Exists(srcFile) {
			return fmt.Errorf("generated file not found: %s", srcFile)
		}
		return CopyFile(srcFile, filepath.Join(backupPath, p.GeneratedFile))
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
		configFile = filepath.Join(homePath, "mcp.json")
		target = p.GeneratorTarget
	default:
		// Fallback: check for known config files
		if Exists(filepath.Join(homePath, "config.toml")) {
			configFile = filepath.Join(homePath, "config.toml")
			target = "codex"
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
