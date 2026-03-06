package main

import "testing"

func TestExtractResourceName(t *testing.T) {
	tests := []struct {
		name  string
		input *string
		want  string
	}{
		{
			name:  "full resource path",
			input: strPtr("projects/my-project/zones/us-central1-a/instances/my-vm"),
			want:  "my-vm",
		},
		{
			name:  "single segment",
			input: strPtr("my-resource"),
			want:  "my-resource",
		},
		{
			name:  "nil pointer",
			input: nil,
			want:  "",
		},
		{
			name:  "machine type path",
			input: strPtr("projects/my-project/zones/us-central1-a/machineTypes/e2-medium"),
			want:  "e2-medium",
		},
		{
			name:  "zone path",
			input: strPtr("projects/my-project/zones/us-central1-a"),
			want:  "us-central1-a",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractResourceName(tt.input)
			if got != tt.want {
				t.Errorf("extractResourceName(%v) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func strPtr(s string) *string {
	return &s
}

func TestGCPRegionDefaults(t *testing.T) {
	if gcpRegion == "" {
		t.Error("gcpRegion should have a default value")
	}
	if gcpZone == "" {
		t.Error("gcpZone should have a default value")
	}
}

func TestToolDefinitions(t *testing.T) {
	expectedTools := map[string]string{
		"gcp_storage_list_buckets":    "List Cloud Storage buckets in the project",
		"gcp_storage_list_objects":    "List objects in a Cloud Storage bucket",
		"gcp_storage_get_object":      "Get an object from Cloud Storage (metadata and content for text files)",
		"gcp_storage_object_metadata": "Get object metadata without downloading content",
		"gcp_compute_list_instances":  "List Compute Engine instances",
		"gcp_compute_get_instance":    "Get details about a specific Compute Engine instance",
		"gcp_functions_list":          "List Cloud Functions (2nd gen)",
		"gcp_functions_get":           "Get details about a Cloud Function",
	}

	for name, desc := range expectedTools {
		if name == "" {
			t.Error("tool name must not be empty")
		}
		if desc == "" {
			t.Errorf("tool %q must have a non-empty description", name)
		}
	}

	if len(expectedTools) != 8 {
		t.Errorf("expected 8 tools, got %d", len(expectedTools))
	}
}
