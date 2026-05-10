package main

import "testing"

func TestGitRepoURL_DerivesHTTPSCloneURL(t *testing.T) {
	got, host, err := gitRepoURL("https://gitlab.flexinfer.ai/api/v4", "services/loom-core")
	if err != nil {
		t.Fatalf("gitRepoURL: %v", err)
	}
	if got != "https://gitlab.flexinfer.ai/services/loom-core.git" {
		t.Errorf("url = %q", got)
	}
	if host != "gitlab.flexinfer.ai" {
		t.Errorf("host = %q", host)
	}
}

func TestGitRepoURL_RejectsNumericProject(t *testing.T) {
	if _, _, err := gitRepoURL("https://gitlab.flexinfer.ai/api/v4", "47"); err == nil {
		t.Fatal("expected numeric project id to be rejected")
	}
}
