package skills

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchSkillsSH(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/search", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "openai" {
			t.Fatalf("unexpected query %q", got)
		}
		if got := r.URL.Query().Get("limit"); got != "3" {
			t.Fatalf("unexpected limit %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"skills": []map[string]any{
				{"id": "openai/skills/openai-docs", "skillId": "openai-docs", "name": "openai-docs", "source": "openai/skills", "installs": 10},
				{"id": "openai/skills/screenshot", "skillId": "screenshot", "name": "screenshot", "source": "openai/skills", "installs": 30},
			},
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	t.Setenv("LOOM_SKILLS_SH_URL", srv.URL)
	results, err := SearchSkillsSH("openai", 3)
	if err != nil {
		t.Fatalf("SearchSkillsSH: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].ID != "openai/skills/screenshot" {
		t.Fatalf("expected highest installs first, got %s", results[0].ID)
	}
}

func TestParseSkillsSHReference(t *testing.T) {
	t.Setenv("LOOM_SKILLS_SH_URL", "https://skills.sh")

	ref, err := ParseSkillsSHReference("https://skills.sh/openai/skills/openai-docs")
	if err != nil {
		t.Fatalf("ParseSkillsSHReference: %v", err)
	}
	if ref.ID != "openai/skills/openai-docs" {
		t.Fatalf("unexpected id %q", ref.ID)
	}
	if ref.Source != "openai/skills" {
		t.Fatalf("unexpected source %q", ref.Source)
	}
	if ref.SkillID != "openai-docs" {
		t.Fatalf("unexpected skill id %q", ref.SkillID)
	}
	if ref.SourceURL != "https://github.com/openai/skills" {
		t.Fatalf("unexpected source url %q", ref.SourceURL)
	}
}

func TestImportSkillsSHSkill(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/openai/skills", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(githubRepoMetadata{DefaultBranch: "main"})
	})
	mux.HandleFunc("/repos/openai/skills/git/trees/main", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(githubTreeResponse{
			Tree: []githubTreeEntry{
				{Path: "skills/.curated/openai-docs/SKILL.md", Type: "blob"},
				{Path: "skills/.curated/openai-docs/references/latest-model.md", Type: "blob"},
				{Path: "skills/.curated/openai-docs/scripts/install.sh", Type: "blob"},
			},
		})
	})
	mux.HandleFunc("/raw/openai/skills/main/skills/.curated/openai-docs/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("---\nname: openai-docs\ndescription: docs\n---\n\n# OpenAI Docs\n"))
	})
	mux.HandleFunc("/raw/openai/skills/main/skills/.curated/openai-docs/references/latest-model.md", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("latest"))
	})
	mux.HandleFunc("/raw/openai/skills/main/skills/.curated/openai-docs/scripts/install.sh", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("#!/usr/bin/env bash\necho install\n"))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	t.Setenv("LOOM_GITHUB_API_URL", srv.URL)
	t.Setenv("LOOM_GITHUB_RAW_URL", srv.URL+"/raw")
	t.Setenv("LOOM_SKILLS_SH_URL", "https://skills.sh")

	dest := t.TempDir()
	result, err := ImportSkillsSHSkill("openai/skills/openai-docs", dest)
	if err != nil {
		t.Fatalf("ImportSkillsSHSkill: %v", err)
	}
	if result == nil || result.Name != "openai-docs" {
		t.Fatalf("unexpected result: %#v", result)
	}

	skillDir := filepath.Join(dest, "openai-docs")
	for _, rel := range []string{"SKILL.md", filepath.Join("references", "latest-model.md"), filepath.Join("scripts", "install.sh")} {
		if _, err := os.Stat(filepath.Join(skillDir, rel)); err != nil {
			t.Fatalf("expected %s to exist: %v", rel, err)
		}
	}

	metadata, err := readHostedMetadata(filepath.Join(skillDir, HostedMetadataFilename))
	if err != nil {
		t.Fatalf("readHostedMetadata: %v", err)
	}
	if metadata == nil {
		t.Fatalf("expected metadata")
	}
	if metadata.SourceURL != "https://skills.sh/openai/skills/openai-docs" {
		t.Fatalf("unexpected metadata source url %q", metadata.SourceURL)
	}
	if metadata.IndexURL != "https://github.com/openai/skills" {
		t.Fatalf("unexpected metadata index url %q", metadata.IndexURL)
	}
}

func TestFindGitHubSkillRootPrefersCuratedSkillsPath(t *testing.T) {
	tree := []githubTreeEntry{
		{Path: ".codex/skills/openai-docs/SKILL.md", Type: "blob"},
		{Path: "skills/.curated/openai-docs/SKILL.md", Type: "blob"},
		{Path: "examples/openai-docs/SKILL.md", Type: "blob"},
	}

	root, err := findGitHubSkillRoot(tree, "openai-docs")
	if err != nil {
		t.Fatalf("findGitHubSkillRoot: %v", err)
	}
	if root != "skills/.curated/openai-docs" {
		t.Fatalf("unexpected root %q", root)
	}
}

func TestParseSkillsSHReferenceRejectsBadRefs(t *testing.T) {
	t.Setenv("LOOM_SKILLS_SH_URL", "https://skills.sh")
	_, err := ParseSkillsSHReference("openai/skills")
	if err == nil || !strings.Contains(err.Error(), "owner/repo/skill") {
		t.Fatalf("expected owner/repo/skill error, got %v", err)
	}
}
