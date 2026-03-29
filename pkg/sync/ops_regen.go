// ops_regen.go — Config and skill regeneration: Regenerate, regenerateSkills,
// SyncSkills, and cleanup helpers (cleanRepoSkills, cleanSkillsAt,
// cleanRepoGenerated, cleanGeneratedAt, discoverSkillsRegistryPath).
package sync

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/crb2nu/loom/pkg/generator"
	"github.com/crb2nu/loom/pkg/registry"
	"github.com/crb2nu/loom/pkg/skills"
)

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
