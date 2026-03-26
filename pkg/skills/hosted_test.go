package skills

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverHostedCatalog_PrefersAgentSkillsIndex(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(hostedAgentSkillsIndexPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method %s", r.Method)
		}
		_, _ = w.Write([]byte(`{"skills":[{"name":"deploy","description":"Deploy things","files":["SKILL.md"]}]}`))
	})
	mux.HandleFunc(hostedSkillsIndexPath, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("legacy fallback should not be used when preferred endpoint exists")
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	catalog, err := DiscoverHostedCatalog(srv.URL)
	if err != nil {
		t.Fatalf("DiscoverHostedCatalog: %v", err)
	}

	if !strings.Contains(catalog.IndexURL, hostedAgentSkillsIndexPath) {
		t.Fatalf("expected preferred index url, got %s", catalog.IndexURL)
	}
	if len(catalog.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(catalog.Skills))
	}
	if catalog.Skills[0].Name != "deploy" {
		t.Fatalf("expected skill name deploy, got %q", catalog.Skills[0].Name)
	}
}

func TestDiscoverHostedCatalog_FallsBackToSkillsIndex(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(hostedAgentSkillsIndexPath, func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.HandleFunc(hostedSkillsIndexPath, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"skills":[{"name":"lint","description":"Lint things","files":["SKILL.md"]}]}`))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	catalog, err := DiscoverHostedCatalog(srv.URL)
	if err != nil {
		t.Fatalf("DiscoverHostedCatalog: %v", err)
	}

	if !strings.Contains(catalog.IndexURL, hostedSkillsIndexPath) {
		t.Fatalf("expected legacy fallback index url, got %s", catalog.IndexURL)
	}
	if len(catalog.Skills) != 1 || catalog.Skills[0].Name != "lint" {
		t.Fatalf("unexpected catalog: %#v", catalog.Skills)
	}
}

func TestImportHostedSkills_WritesBundleToHome(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(hostedAgentSkillsIndexPath, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"skills":[{"name":"deploy","description":"Deploy things","files":["SKILL.md","scripts/run.sh","references/guide.md"]}]}`))
	})
	mux.HandleFunc("/.well-known/agent-skills/deploy/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("# Deploy\n\nDo the thing.\n"))
	})
	mux.HandleFunc("/.well-known/agent-skills/deploy/scripts/run.sh", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("#!/usr/bin/env bash\necho run\n"))
	})
	mux.HandleFunc("/.well-known/agent-skills/deploy/references/guide.md", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("Guide"))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	dest := t.TempDir()
	results, err := ImportHostedSkills(srv.URL, dest, nil)
	if err != nil {
		t.Fatalf("ImportHostedSkills: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 imported skill, got %d", len(results))
	}
	if results[0].Name != "deploy" {
		t.Fatalf("expected imported skill deploy, got %q", results[0].Name)
	}

	skillDir := filepath.Join(dest, "deploy")
	for _, rel := range []string{"SKILL.md", filepath.Join("scripts", "run.sh"), filepath.Join("references", "guide.md")} {
		if _, err := os.Stat(filepath.Join(skillDir, rel)); err != nil {
			t.Fatalf("expected %s to exist: %v", rel, err)
		}
	}

	data, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	if !strings.Contains(string(data), "Do the thing.") {
		t.Fatalf("unexpected SKILL.md contents:\n%s", string(data))
	}

	info, err := os.Stat(filepath.Join(skillDir, "scripts", "run.sh"))
	if err != nil {
		t.Fatalf("stat run.sh: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("expected scripts/run.sh to be executable, mode=%v", info.Mode())
	}

	metadataPath := filepath.Join(skillDir, HostedMetadataFilename)
	metadata, err := readHostedMetadata(metadataPath)
	if err != nil {
		t.Fatalf("readHostedMetadata: %v", err)
	}
	if metadata == nil || metadata.Name != "deploy" {
		t.Fatalf("unexpected metadata: %#v", metadata)
	}
}

func TestImportHostedSkills_RejectsPathTraversal(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(hostedAgentSkillsIndexPath, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"skills":[{"name":"deploy","description":"Deploy things","files":["../escape"]}]}`))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := ImportHostedSkills(srv.URL, t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "path traversal") {
		t.Fatalf("expected path traversal error, got %v", err)
	}
}

func TestListAndRemoveHostedSkills(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(hostedAgentSkillsIndexPath, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"skills":[{"name":"deploy","description":"Deploy things","files":["SKILL.md"]},{"name":"lint","description":"Lint things","files":["SKILL.md"]}]}`))
	})
	mux.HandleFunc("/.well-known/agent-skills/deploy/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("# Deploy\n"))
	})
	mux.HandleFunc("/.well-known/agent-skills/lint/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("# Lint\n"))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	dest := t.TempDir()
	if _, err := ImportHostedSkills(srv.URL, dest, nil); err != nil {
		t.Fatalf("ImportHostedSkills: %v", err)
	}

	installed, err := ListHostedSkills(dest)
	if err != nil {
		t.Fatalf("ListHostedSkills: %v", err)
	}
	if len(installed) != 2 {
		t.Fatalf("expected 2 installed skills, got %d", len(installed))
	}

	removed, err := RemoveHostedSkills(dest, []string{"deploy"}, false)
	if err != nil {
		t.Fatalf("RemoveHostedSkills: %v", err)
	}
	if len(removed) != 1 || removed[0].Name != "deploy" {
		t.Fatalf("unexpected removed skills: %#v", removed)
	}
	if _, err := os.Stat(filepath.Join(dest, "deploy")); !os.IsNotExist(err) {
		t.Fatalf("expected deploy skill dir removed, stat err=%v", err)
	}

	installed, err = ListHostedSkills(dest)
	if err != nil {
		t.Fatalf("ListHostedSkills after remove: %v", err)
	}
	if len(installed) != 1 || installed[0].Name != "lint" {
		t.Fatalf("unexpected remaining skills: %#v", installed)
	}
}
