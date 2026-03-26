package skills

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const (
	hostedAgentSkillsIndexPath = "/.well-known/agent-skills/index.json"
	hostedSkillsIndexPath      = "/.well-known/skills/index.json"
	hostedFetchTimeout         = 30 * time.Second
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

		for _, rel := range files {
			rel = strings.TrimSpace(rel)
			if rel == "" {
				continue
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

func fetchURL(client *http.Client, target *url.URL) ([]byte, error) {
	resp, err := client.Get(target.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
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
