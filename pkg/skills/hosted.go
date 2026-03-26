package skills

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	hostedAgentSkillsIndexPath = "/.well-known/agent-skills/index.json"
	hostedSkillsIndexPath      = "/.well-known/skills/index.json"
	hostedFetchTimeout         = 30 * time.Second
	HostedMetadataFilename     = ".loom-hosted-skill.json"
)

// HostedCatalog describes a discovered hosted skills source.
type HostedCatalog struct {
	SourceURL string
	IndexURL  string
	Skills    []HostedSkill
}

// HostedSkill describes one skill entry from a hosted catalog.
type HostedSkill struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Path        string   `json:"path,omitempty"`
	Files       []string `json:"files,omitempty"`
}

// HostedImportResult records a successfully imported hosted skill bundle.
type HostedImportResult struct {
	Name        string
	Destination string
	Files       []string
}

// HostedInstallMetadata tracks Loom-managed hosted skill provenance.
type HostedInstallMetadata struct {
	Name       string   `json:"name"`
	SourceURL  string   `json:"source_url"`
	IndexURL   string   `json:"index_url"`
	Path       string   `json:"path,omitempty"`
	Files      []string `json:"files"`
	ImportedAt string   `json:"imported_at"`
	ManagedBy  string   `json:"managed_by"`
}

// HostedInstalledSkill describes one Loom-managed hosted skill installed on disk.
type HostedInstalledSkill struct {
	Name         string
	SourceURL    string
	IndexURL     string
	Path         string
	Destination  string
	Files        []string
	ImportedAt   string
	MetadataPath string
}

// DiscoverHostedCatalog fetches the hosted skills index from the preferred
// well-known endpoint, falling back to the legacy skills path when needed.
func DiscoverHostedCatalog(source string) (*HostedCatalog, error) {
	normalized, err := normalizeHostedSource(source)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: hostedFetchTimeout}
	indexURL, data, err := fetchHostedIndex(client, normalized, []string{hostedAgentSkillsIndexPath, hostedSkillsIndexPath})
	if err != nil {
		return nil, err
	}

	skills, err := decodeHostedSkills(data)
	if err != nil {
		return nil, fmt.Errorf("decode hosted skills index: %w", err)
	}

	return &HostedCatalog{
		SourceURL: normalized.String(),
		IndexURL:  indexURL.String(),
		Skills:    skills,
	}, nil
}

// ImportHostedSkills downloads hosted skill bundles into destRoot.
func ImportHostedSkills(source, destRoot string, selected []string) ([]HostedImportResult, error) {
	catalog, err := DiscoverHostedCatalog(source)
	if err != nil {
		return nil, err
	}

	selectedSet := make(map[string]struct{}, len(selected))
	for _, name := range selected {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		selectedSet[name] = struct{}{}
	}

	indexURL, err := url.Parse(catalog.IndexURL)
	if err != nil {
		return nil, fmt.Errorf("parse index url: %w", err)
	}
	skillRootURL := hostedSkillRootURL(indexURL)

	client := &http.Client{Timeout: hostedFetchTimeout}
	var results []HostedImportResult
	for _, skill := range catalog.Skills {
		installName := hostedSkillInstallName(skill)
		if installName == "" {
			continue
		}
		if err := validateHostedInstallName(installName); err != nil {
			return nil, fmt.Errorf("invalid hosted skill name %q: %w", installName, err)
		}
		if len(selectedSet) > 0 {
			if _, ok := selectedSet[installName]; !ok {
				continue
			}
		}

		files := skill.Files
		if len(files) == 0 {
			files = []string{"SKILL.md"}
		}

		rootURL := hostedResolveSkillRoot(skillRootURL, skill, installName)
		destDir := filepath.Join(destRoot, installName)
		written := make([]string, 0, len(files))
		relativeFiles := make([]string, 0, len(files))

		for _, rel := range files {
			rel, err = sanitizeHostedRelativePath(rel)
			if err != nil {
				return nil, fmt.Errorf("sanitize %s/%s: %w", installName, rel, err)
			}

			body, err := fetchHostedFile(client, rootURL, rel)
			if err != nil {
				return nil, fmt.Errorf("fetch %s/%s: %w", installName, rel, err)
			}

			dst := filepath.Join(destDir, filepath.FromSlash(rel))
			perm := fileModeForHostedPath(rel, body)
			if err := writeHostedFile(dst, body, perm); err != nil {
				return nil, fmt.Errorf("write %s: %w", dst, err)
			}
			written = append(written, filepath.ToSlash(filepath.Join(installName, filepath.FromSlash(rel))))
			relativeFiles = append(relativeFiles, rel)
		}

		if err := writeHostedMetadata(destDir, HostedInstallMetadata{
			Name:       installName,
			SourceURL:  catalog.SourceURL,
			IndexURL:   catalog.IndexURL,
			Path:       skill.Path,
			Files:      relativeFiles,
			ImportedAt: time.Now().UTC().Format(time.RFC3339),
			ManagedBy:  "loom",
		}); err != nil {
			return nil, fmt.Errorf("write metadata for %s: %w", installName, err)
		}

		if len(written) == 0 {
			continue
		}
		results = append(results, HostedImportResult{
			Name:        installName,
			Destination: destDir,
			Files:       written,
		})
	}

	return results, nil
}

// ListHostedSkills returns Loom-managed hosted skills installed under destRoot.
func ListHostedSkills(destRoot string) ([]HostedInstalledSkill, error) {
	entries, err := os.ReadDir(destRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	skills := make([]HostedInstalledSkill, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillDir := filepath.Join(destRoot, entry.Name())
		metadataPath := filepath.Join(skillDir, HostedMetadataFilename)
		meta, err := readHostedMetadata(metadataPath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", metadataPath, err)
		}
		if meta == nil {
			continue
		}
		skills = append(skills, HostedInstalledSkill{
			Name:         meta.Name,
			SourceURL:    meta.SourceURL,
			IndexURL:     meta.IndexURL,
			Path:         meta.Path,
			Destination:  skillDir,
			Files:        append([]string(nil), meta.Files...),
			ImportedAt:   meta.ImportedAt,
			MetadataPath: metadataPath,
		})
	}

	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	return skills, nil
}

// RemoveHostedSkills removes Loom-managed hosted skills from destRoot.
func RemoveHostedSkills(destRoot string, selected []string, removeAll bool) ([]HostedInstalledSkill, error) {
	if !removeAll && len(selected) == 0 {
		return nil, fmt.Errorf("at least one skill must be selected")
	}

	installed, err := ListHostedSkills(destRoot)
	if err != nil {
		return nil, err
	}

	selectedSet := make(map[string]struct{}, len(selected))
	for _, name := range selected {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		selectedSet[name] = struct{}{}
	}

	removed := make([]HostedInstalledSkill, 0)
	for _, skill := range installed {
		if !removeAll {
			if _, ok := selectedSet[skill.Name]; !ok {
				continue
			}
		}
		if err := os.RemoveAll(skill.Destination); err != nil {
			return nil, fmt.Errorf("remove %s: %w", skill.Destination, err)
		}
		removed = append(removed, skill)
	}

	return removed, nil
}

func normalizeHostedSource(source string) (*url.URL, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil, fmt.Errorf("source is required")
	}

	if !strings.Contains(source, "://") {
		source = "https://" + strings.TrimPrefix(source, "//")
	}

	u, err := url.Parse(source)
	if err != nil {
		return nil, fmt.Errorf("parse source url: %w", err)
	}
	if u.Scheme == "" {
		u.Scheme = "https"
	}
	if u.Host == "" {
		return nil, fmt.Errorf("source must include a host: %q", source)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u, nil
}

func fetchHostedIndex(client *http.Client, source *url.URL, paths []string) (*url.URL, []byte, error) {
	var lastErr error
	for _, p := range paths {
		indexURL := cloneURL(source)
		indexURL.Path = joinURLPath(source.Path, p)
		body, err := fetchURL(client, indexURL)
		if err != nil {
			lastErr = err
			continue
		}
		return indexURL, body, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no hosted skills index found")
	}
	return nil, nil, lastErr
}

func decodeHostedSkills(data []byte) ([]HostedSkill, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, fmt.Errorf("empty index response")
	}

	var skills []HostedSkill
	if data[0] == '[' {
		if err := json.Unmarshal(data, &skills); err != nil {
			return nil, err
		}
		return skills, nil
	}

	var wrapper struct {
		Skills []HostedSkill `json:"skills"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, err
	}
	return wrapper.Skills, nil
}

func hostedSkillInstallName(skill HostedSkill) string {
	name := strings.TrimSpace(skill.Name)
	if name != "" {
		return name
	}
	if skill.Path != "" {
		trimmed := strings.Trim(skill.Path, "/")
		if trimmed != "" {
			return path.Base(trimmed)
		}
	}
	return ""
}

func validateHostedInstallName(name string) error {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return fmt.Errorf("name is required")
	case strings.Contains(name, "/"), strings.Contains(name, `\`):
		return fmt.Errorf("path separators are not allowed")
	case name == ".", name == "..":
		return fmt.Errorf("relative directory names are not allowed")
	default:
		return nil
	}
}

func hostedSkillRootURL(indexURL *url.URL) *url.URL {
	root := cloneURL(indexURL)
	root.Path = strings.TrimSuffix(root.Path, "index.json")
	if !strings.HasSuffix(root.Path, "/") {
		root.Path += "/"
	}
	return root
}

func hostedResolveSkillRoot(indexRoot *url.URL, skill HostedSkill, installName string) *url.URL {
	root := cloneURL(indexRoot)
	if skill.Path != "" {
		trimmed := strings.Trim(skill.Path, "/")
		root.Path = joinURLPath(indexRoot.Path, trimmed)
		if !strings.HasSuffix(root.Path, "/") {
			root.Path += "/"
		}
		return root
	}
	root.Path = joinURLPath(indexRoot.Path, installName)
	if !strings.HasSuffix(root.Path, "/") {
		root.Path += "/"
	}
	return root
}

func fetchHostedFile(client *http.Client, root *url.URL, rel string) ([]byte, error) {
	fileURL := cloneURL(root)
	fileURL.Path = joinURLPath(root.Path, filepath.ToSlash(rel))
	return fetchURL(client, fileURL)
}

func sanitizeHostedRelativePath(rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", fmt.Errorf("path is required")
	}
	if strings.Contains(rel, `\`) {
		return "", fmt.Errorf("backslashes are not allowed")
	}

	clean := path.Clean(rel)
	switch {
	case clean == ".", clean == "":
		return "", fmt.Errorf("path is required")
	case strings.HasPrefix(clean, "/"):
		return "", fmt.Errorf("absolute paths are not allowed")
	case clean == "..", strings.HasPrefix(clean, "../"), strings.Contains(clean, "/../"):
		return "", fmt.Errorf("path traversal is not allowed")
	default:
		return clean, nil
	}
}

func fetchURL(client *http.Client, target *url.URL) ([]byte, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

func writeHostedMetadata(destDir string, metadata HostedInstallMetadata) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(destDir, HostedMetadataFilename), append(data, '\n'), 0o644)
}

func readHostedMetadata(path string) (*HostedInstallMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var metadata HostedInstallMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, err
	}
	if strings.TrimSpace(metadata.ManagedBy) != "loom" || strings.TrimSpace(metadata.Name) == "" {
		return nil, nil
	}
	return &metadata, nil
}

func cloneURL(src *url.URL) *url.URL {
	if src == nil {
		return &url.URL{}
	}
	dst := *src
	return &dst
}

func joinURLPath(parts ...string) string {
	joined := ""
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		joined = path.Join(joined, part)
	}
	if joined == "." {
		return "/"
	}
	if !strings.HasPrefix(joined, "/") {
		joined = "/" + joined
	}
	return joined
}

func fileModeForHostedPath(rel string, content []byte) os.FileMode {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	if strings.Contains(rel, "/scripts/") || strings.HasPrefix(rel, "scripts/") || bytes.HasPrefix(content, []byte("#!")) {
		return 0o755
	}
	return 0o644
}

func writeHostedFile(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, data) {
		return nil
	}
	return os.WriteFile(path, data, perm)
}
