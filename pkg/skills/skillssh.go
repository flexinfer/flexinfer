package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/env"
)

const (
	skillsSHDefaultBaseURL  = "https://skills.sh"
	githubAPIDefaultBaseURL = "https://api.github.com"
	githubRawDefaultBaseURL = "https://raw.githubusercontent.com"
)

// SkillsSHSearchResult describes one result returned by skills.sh search.
type SkillsSHSearchResult struct {
	ID       string `json:"id"`
	SkillID  string `json:"skillId"`
	Name     string `json:"name"`
	Source   string `json:"source"`
	Installs int    `json:"installs"`
}

// SkillsSHReference is a normalized reference to one skills.sh-hosted skill.
type SkillsSHReference struct {
	ID        string
	SkillID   string
	Source    string
	SiteURL   string
	SourceURL string
}

type githubRepoMetadata struct {
	DefaultBranch string `json:"default_branch"`
}

type githubTreeResponse struct {
	Tree      []githubTreeEntry `json:"tree"`
	Truncated bool              `json:"truncated"`
}

type githubTreeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
}

// SearchSkillsSH searches the public skills.sh directory for matching skills.
func SearchSkillsSH(query string, limit int) ([]SkillsSHSearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}
	if limit <= 0 {
		limit = 10
	}

	baseURL, err := normalizeHTTPBaseURL(env.String("LOOM_SKILLS_SH_URL", env.String("SKILLS_API_URL", skillsSHDefaultBaseURL)))
	if err != nil {
		return nil, fmt.Errorf("normalize skills.sh url: %w", err)
	}
	searchURL := cloneURL(baseURL)
	searchURL.Path = joinURLPath(searchURL.Path, "/api/search")

	values := searchURL.Query()
	values.Set("q", query)
	values.Set("limit", fmt.Sprintf("%d", limit))
	searchURL.RawQuery = values.Encode()

	client := &http.Client{Timeout: hostedFetchTimeout}
	body, err := fetchURL(client, searchURL)
	if err != nil {
		return nil, err
	}

	var payload struct {
		Skills []SkillsSHSearchResult `json:"skills"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode skills.sh search response: %w", err)
	}

	sort.SliceStable(payload.Skills, func(i, j int) bool {
		if payload.Skills[i].Installs == payload.Skills[j].Installs {
			return payload.Skills[i].ID < payload.Skills[j].ID
		}
		return payload.Skills[i].Installs > payload.Skills[j].Installs
	})
	return payload.Skills, nil
}

// ParseSkillsSHReference normalizes either a skills.sh page URL or an
// owner/repo/skill slug into a GitHub-backed install reference.
func ParseSkillsSHReference(ref string) (*SkillsSHReference, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("skill reference is required")
	}

	id := ref
	if strings.Contains(ref, "://") {
		u, err := url.Parse(ref)
		if err != nil {
			return nil, fmt.Errorf("parse skills.sh url: %w", err)
		}
		id = strings.Trim(u.Path, "/")
	}

	parts := strings.Split(id, "/")
	if len(parts) != 3 {
		return nil, fmt.Errorf("expected skills.sh ref in owner/repo/skill form, got %q", ref)
	}

	source := path.Join(parts[0], parts[1])
	skillID := strings.TrimSpace(parts[2])
	if err := validateHostedInstallName(skillID); err != nil {
		return nil, fmt.Errorf("invalid skill id %q: %w", skillID, err)
	}

	baseURL, err := normalizeHTTPBaseURL(env.String("LOOM_SKILLS_SH_URL", env.String("SKILLS_API_URL", skillsSHDefaultBaseURL)))
	if err != nil {
		return nil, fmt.Errorf("normalize skills.sh url: %w", err)
	}
	siteURL := cloneURL(baseURL)
	siteURL.Path = joinURLPath(siteURL.Path, id)

	return &SkillsSHReference{
		ID:        id,
		SkillID:   skillID,
		Source:    source,
		SiteURL:   siteURL.String(),
		SourceURL: "https://github.com/" + source,
	}, nil
}

// ImportSkillsSHSkill imports a single selected skills.sh result into destRoot.
func ImportSkillsSHSkill(ref, destRoot string) (*HostedImportResult, error) {
	skillRef, err := ParseSkillsSHReference(ref)
	if err != nil {
		return nil, err
	}

	owner, repo, err := parseGitHubOwnerRepo(skillRef.Source)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: hostedFetchTimeout}
	defaultBranch, err := githubDefaultBranch(client, owner, repo)
	if err != nil {
		return nil, err
	}

	tree, err := githubRepositoryTree(client, owner, repo, defaultBranch)
	if err != nil {
		return nil, err
	}

	skillRoot, err := findGitHubSkillRoot(tree, skillRef.SkillID)
	if err != nil {
		return nil, err
	}

	files := filesUnderSkillRoot(tree, skillRoot)
	if len(files) == 0 {
		return nil, fmt.Errorf("no files found under %s in %s", skillRoot, skillRef.Source)
	}

	destDir := filepath.Join(destRoot, skillRef.SkillID)
	written := make([]string, 0, len(files))
	relativeFiles := make([]string, 0, len(files))
	for _, entry := range files {
		rel := strings.TrimPrefix(entry.Path, skillRoot+"/")
		rel, err = sanitizeHostedRelativePath(rel)
		if err != nil {
			return nil, fmt.Errorf("sanitize %s/%s: %w", skillRef.SkillID, rel, err)
		}

		body, err := fetchGitHubRawFile(client, owner, repo, defaultBranch, entry.Path)
		if err != nil {
			return nil, fmt.Errorf("fetch %s from %s: %w", entry.Path, skillRef.Source, err)
		}

		dst := filepath.Join(destDir, filepath.FromSlash(rel))
		perm := fileModeForHostedPath(rel, body)
		if err := writeHostedFile(dst, body, perm); err != nil {
			return nil, fmt.Errorf("write %s: %w", dst, err)
		}

		written = append(written, filepath.ToSlash(filepath.Join(skillRef.SkillID, filepath.FromSlash(rel))))
		relativeFiles = append(relativeFiles, rel)
	}

	if err := writeHostedMetadata(destDir, HostedInstallMetadata{
		Name:       skillRef.SkillID,
		SourceURL:  skillRef.SiteURL,
		IndexURL:   skillRef.SourceURL,
		Path:       skillRoot,
		Files:      relativeFiles,
		ImportedAt: time.Now().UTC().Format(time.RFC3339),
		ManagedBy:  "loom",
	}); err != nil {
		return nil, fmt.Errorf("write metadata for %s: %w", skillRef.SkillID, err)
	}

	return &HostedImportResult{
		Name:        skillRef.SkillID,
		Destination: destDir,
		Files:       written,
	}, nil
}

func normalizeHTTPBaseURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("base url is required")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + strings.TrimPrefix(raw, "//")
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse base url: %w", err)
	}
	if u.Scheme == "" {
		u.Scheme = "https"
	}
	if u.Host == "" {
		return nil, fmt.Errorf("base url must include a host")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u, nil
}

func parseGitHubOwnerRepo(source string) (string, string, error) {
	parts := strings.Split(strings.Trim(source, "/"), "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("skills.sh source %q is not a supported GitHub owner/repo", source)
	}
	if strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", fmt.Errorf("skills.sh source %q is not a supported GitHub owner/repo", source)
	}
	return parts[0], parts[1], nil
}

func githubDefaultBranch(client *http.Client, owner, repo string) (string, error) {
	repoURL, err := normalizeHTTPBaseURL(env.String("LOOM_GITHUB_API_URL", githubAPIDefaultBaseURL))
	if err != nil {
		return "", fmt.Errorf("normalize github api url: %w", err)
	}
	repoURL.Path = joinURLPath(repoURL.Path, "repos", owner, repo)

	body, err := fetchURLWithHeaders(client, repoURL, githubAPIHeaders())
	if err != nil {
		return "", fmt.Errorf("fetch github repo metadata: %w", err)
	}

	var metadata githubRepoMetadata
	if err := json.Unmarshal(body, &metadata); err != nil {
		return "", fmt.Errorf("decode github repo metadata: %w", err)
	}
	if strings.TrimSpace(metadata.DefaultBranch) == "" {
		return "", fmt.Errorf("github repo %s/%s did not report a default branch", owner, repo)
	}
	return metadata.DefaultBranch, nil
}

func githubRepositoryTree(client *http.Client, owner, repo, ref string) ([]githubTreeEntry, error) {
	apiURL, err := normalizeHTTPBaseURL(env.String("LOOM_GITHUB_API_URL", githubAPIDefaultBaseURL))
	if err != nil {
		return nil, fmt.Errorf("normalize github api url: %w", err)
	}
	apiURL.Path = joinURLPath(apiURL.Path, "repos", owner, repo, "git", "trees", ref)
	values := apiURL.Query()
	values.Set("recursive", "1")
	apiURL.RawQuery = values.Encode()

	body, err := fetchURLWithHeaders(client, apiURL, githubAPIHeaders())
	if err != nil {
		return nil, fmt.Errorf("fetch github tree: %w", err)
	}

	var payload githubTreeResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode github tree response: %w", err)
	}
	if payload.Truncated {
		return nil, fmt.Errorf("github tree for %s/%s was truncated", owner, repo)
	}
	return payload.Tree, nil
}

func findGitHubSkillRoot(tree []githubTreeEntry, skillID string) (string, error) {
	type candidate struct {
		root  string
		score int
	}

	var candidates []candidate
	for _, entry := range tree {
		if entry.Type != "blob" || path.Base(entry.Path) != "SKILL.md" {
			continue
		}
		root := path.Dir(entry.Path)
		if path.Base(root) != skillID {
			continue
		}
		candidates = append(candidates, candidate{
			root:  root,
			score: scoreGitHubSkillRoot(root, skillID),
		})
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("no SKILL.md found for %s", skillID)
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			if len(candidates[i].root) == len(candidates[j].root) {
				return candidates[i].root < candidates[j].root
			}
			return len(candidates[i].root) < len(candidates[j].root)
		}
		return candidates[i].score > candidates[j].score
	})
	return candidates[0].root, nil
}

func scoreGitHubSkillRoot(root, skillID string) int {
	root = strings.Trim(root, "/")
	switch {
	case root == path.Join("skills", ".curated", skillID):
		return 500
	case root == path.Join("skills", skillID):
		return 450
	case strings.Contains(root, "/.curated/"+skillID):
		return 350
	case strings.Contains(root, "/skills/"+skillID):
		return 300
	case strings.HasSuffix(root, "/"+skillID):
		return 200 - strings.Count(root, "/")
	default:
		return 100 - strings.Count(root, "/")
	}
}

func filesUnderSkillRoot(tree []githubTreeEntry, skillRoot string) []githubTreeEntry {
	files := make([]githubTreeEntry, 0)
	prefix := skillRoot + "/"
	for _, entry := range tree {
		if entry.Type != "blob" {
			continue
		}
		if entry.Path == path.Join(skillRoot, "SKILL.md") || strings.HasPrefix(entry.Path, prefix) {
			files = append(files, entry)
		}
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files
}

func fetchGitHubRawFile(client *http.Client, owner, repo, ref, filePath string) ([]byte, error) {
	rawURL, err := normalizeHTTPBaseURL(env.String("LOOM_GITHUB_RAW_URL", githubRawDefaultBaseURL))
	if err != nil {
		return nil, fmt.Errorf("normalize github raw url: %w", err)
	}
	rawURL.Path = joinURLPath(rawURL.Path, owner, repo, ref, filePath)
	return fetchURL(client, rawURL)
}

func githubAPIHeaders() map[string]string {
	headers := map[string]string{
		"Accept": "application/vnd.github+json",
	}
	token := env.StringWithFallbacks("GITHUB_PERSONAL_ACCESS_TOKEN", "GITHUB_TOKEN")
	if strings.TrimSpace(token) != "" {
		headers["Authorization"] = "Bearer " + token
	}
	return headers
}

func fetchURLWithHeaders(client *http.Client, target *url.URL, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		if strings.TrimSpace(value) == "" {
			continue
		}
		req.Header.Set(key, value)
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
