package registry

import (
	"testing"
)

func TestLoadEmbeddedMetadata(t *testing.T) {
	meta, err := LoadEmbeddedMetadata()
	if err != nil {
		t.Fatalf("LoadEmbeddedMetadata failed: %v", err)
	}

	if meta == nil {
		t.Fatal("LoadEmbeddedMetadata returned nil")
	}

	if meta.Version <= 0 {
		t.Errorf("expected positive version, got %d", meta.Version)
	}

	if len(meta.Servers) == 0 {
		t.Error("expected at least one server in metadata")
	}

	if len(meta.Categories) == 0 {
		t.Error("expected at least one category in metadata")
	}
}

func TestMetadata_GetServerMetadata(t *testing.T) {
	meta, err := LoadEmbeddedMetadata()
	if err != nil {
		t.Fatal(err)
	}

	// Test existing server
	gitMeta := meta.GetServerMetadata("mcp-git")
	if gitMeta == nil {
		t.Error("expected mcp-git server metadata to exist")
	} else {
		if gitMeta.Description == "" {
			t.Error("expected mcp-git to have a description")
		}
		if gitMeta.Category == "" {
			t.Error("expected mcp-git to have a category")
		}
	}

	// Test non-existing server
	nonExistent := meta.GetServerMetadata("non-existent")
	if nonExistent != nil {
		t.Error("expected non-existent server to return nil")
	}
}

func TestMetadata_GetToolMetadata(t *testing.T) {
	meta, err := LoadEmbeddedMetadata()
	if err != nil {
		t.Fatal(err)
	}

	// Test existing tool
	gitStatus := meta.GetToolMetadata("mcp-git", "git_status")
	if gitStatus == nil {
		t.Error("expected git_status tool metadata to exist")
	} else {
		if gitStatus.UsageHint == "" {
			t.Error("expected git_status to have a usage hint")
		}
		if gitStatus.Priority <= 0 {
			t.Error("expected git_status to have a positive priority")
		}
	}

	// Test non-existing tool on existing server
	nonExistentTool := meta.GetToolMetadata("mcp-git", "non_existent")
	if nonExistentTool != nil {
		t.Error("expected non-existent tool to return nil")
	}

	// Test tool on non-existing server
	toolOnNonExistent := meta.GetToolMetadata("non-existent", "some_tool")
	if toolOnNonExistent != nil {
		t.Error("expected tool on non-existent server to return nil")
	}
}

func TestMetadata_GetCategoryServers(t *testing.T) {
	meta, err := LoadEmbeddedMetadata()
	if err != nil {
		t.Fatal(err)
	}

	// Test existing category
	vcServers := meta.GetCategoryServers("version-control")
	if len(vcServers) == 0 {
		t.Error("expected version-control category to have servers")
	}

	// Check that mcp-git is in version-control
	hasGit := false
	for _, s := range vcServers {
		if s == "mcp-git" {
			hasGit = true
			break
		}
	}
	if !hasGit {
		t.Error("expected mcp-git in version-control category")
	}

	// Test non-existing category
	nonExistent := meta.GetCategoryServers("non-existent")
	if nonExistent != nil {
		t.Error("expected non-existent category to return nil")
	}
}

func TestMetadata_EnhanceDescription(t *testing.T) {
	meta, err := LoadEmbeddedMetadata()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name         string
		server       string
		tool         string
		original     string
		wantContains string
	}{
		{
			name:         "existing tool with hint",
			server:       "mcp-git",
			tool:         "git_status",
			original:     "Show working tree status",
			wantContains: "Hint:",
		},
		{
			name:         "non-existing tool",
			server:       "mcp-git",
			tool:         "non_existent",
			original:     "Original description",
			wantContains: "Original description",
		},
		{
			name:         "non-existing server",
			server:       "non-existent",
			tool:         "some_tool",
			original:     "Original",
			wantContains: "Original",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := meta.EnhanceDescription(tt.server, tt.tool, tt.original)
			if result == "" {
				t.Error("EnhanceDescription returned empty string")
			}
			if tt.wantContains != "" && !containsSubstring(result, tt.wantContains) {
				t.Errorf("expected result to contain '%s', got '%s'", tt.wantContains, result)
			}
		})
	}
}

func TestMetadata_EnhanceDescription_EmptyOriginal(t *testing.T) {
	meta, err := LoadEmbeddedMetadata()
	if err != nil {
		t.Fatal(err)
	}

	// With empty original description, should return just the hint
	result := meta.EnhanceDescription("mcp-git", "git_status", "")
	if result == "" {
		t.Error("expected non-empty result even with empty original")
	}
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
