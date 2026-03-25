package codebase

import (
	"context"
	"testing"
)

func TestParseGitRemoteProject(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "SSH with .git suffix",
			url:  "git@github.com:owner/repo.git",
			want: "owner/repo",
		},
		{
			name: "SSH without .git suffix",
			url:  "git@github.com:owner/repo",
			want: "owner/repo",
		},
		{
			name: "HTTPS with .git suffix",
			url:  "https://github.com/owner/repo.git",
			want: "owner/repo",
		},
		{
			name: "HTTPS without .git suffix",
			url:  "https://github.com/owner/repo",
			want: "owner/repo",
		},
		{
			name: "GitLab nested groups",
			url:  "git@gitlab.com:group/subgroup/repo.git",
			want: "group/subgroup/repo",
		},
		{
			name: "HTTPS nested groups",
			url:  "https://gitlab.com/group/subgroup/repo.git",
			want: "group/subgroup/repo",
		},
		{
			name: "self-hosted SSH",
			url:  "git@git.example.com:team/project.git",
			want: "team/project",
		},
		{
			name: "self-hosted HTTPS",
			url:  "https://git.example.com/team/project.git",
			want: "team/project",
		},
		{
			name: "empty string",
			url:  "",
			want: "",
		},
		{
			name: "whitespace only",
			url:  "  \n  ",
			want: "",
		},
		{
			name: "malformed URL no path",
			url:  "https://github.com",
			want: "",
		},
		{
			name: "malformed SSH no path",
			url:  "git@github.com:",
			want: "",
		},
		{
			name: "only owner no repo",
			url:  "git@github.com:owner",
			want: "",
		},
		{
			name: "HTTP scheme",
			url:  "http://github.com/owner/repo.git",
			want: "owner/repo",
		},
		{
			name: "trailing slash",
			url:  "https://github.com/owner/repo/",
			want: "owner/repo",
		},
		{
			name: "SSH with port-like format",
			url:  "ssh://git@github.com/owner/repo.git",
			want: "owner/repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseGitRemoteProject(tt.url)
			if got != tt.want {
				t.Errorf("ParseGitRemoteProject(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestDetectPipelineProject(t *testing.T) {
	// DetectPipelineProject on a nonexistent directory returns empty.
	got := DetectPipelineProject(context.Background(), "/nonexistent-dir-for-test")
	if got != "" {
		t.Errorf("DetectPipelineProject(/nonexistent) = %q, want empty", got)
	}
}
