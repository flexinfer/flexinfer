package main

import "testing"

func TestResumeEndpointToolSpecs_UniqueNames(t *testing.T) {
	specs := resumeEndpointToolSpecs()
	if len(specs) == 0 {
		t.Fatal("expected resume endpoint tool specs")
	}

	seen := map[string]struct{}{}
	for _, spec := range specs {
		if spec.Name == "" {
			t.Fatal("tool spec name must not be empty")
		}
		if _, exists := seen[spec.Name]; exists {
			t.Fatalf("duplicate tool name: %s", spec.Name)
		}
		seen[spec.Name] = struct{}{}
	}
}

func TestResumeEndpointToolSpecs_CriticalCoverage(t *testing.T) {
	specs := resumeEndpointToolSpecs()
	byName := map[string]endpointToolSpec{}
	for _, spec := range specs {
		byName[spec.Name] = spec
	}

	cases := []struct {
		name         string
		method       string
		pathTemplate string
	}{
		{
			name:         "jobsearch_resumes_data_get",
			method:       httpMethodGet,
			pathTemplate: "/resumes/data",
		},
		{
			name:         "jobsearch_resumes_compose_save",
			method:       httpMethodPost,
			pathTemplate: "/resumes/compose/save",
		},
		{
			name:         "jobsearch_resumes_db_variant_delete",
			method:       httpMethodDelete,
			pathTemplate: "/resumes/db/variants/{variant_id}",
		},
		{
			name:         "jobsearch_resumes_versions_restore",
			method:       httpMethodPost,
			pathTemplate: "/resumes/versions/{version_id}/restore",
		},
		{
			name:         "jobsearch_resume_chat_message_send",
			method:       httpMethodPost,
			pathTemplate: "/chat/resume/sessions/{session_id}/messages",
		},
		{
			name:         "jobsearch_entities_compose_resume",
			method:       httpMethodPost,
			pathTemplate: "/entities/{entity_id}/compose-resume",
		},
	}

	for _, tc := range cases {
		spec, ok := byName[tc.name]
		if !ok {
			t.Fatalf("missing expected tool: %s", tc.name)
		}
		if spec.Method != tc.method {
			t.Fatalf("%s method = %s, want %s", tc.name, spec.Method, tc.method)
		}
		if spec.PathTemplate != tc.pathTemplate {
			t.Fatalf("%s path template = %s, want %s", tc.name, spec.PathTemplate, tc.pathTemplate)
		}
	}
}

func TestResumeEndpointToolSpecs_DestructiveOperationsRequireConfirm(t *testing.T) {
	specs := resumeEndpointToolSpecs()
	confirmRequired := map[string]bool{
		"jobsearch_resumes_variant_delete":    false,
		"jobsearch_resumes_db_variant_delete": false,
		"jobsearch_resume_chat_session_end":   false,
	}

	for _, spec := range specs {
		if _, ok := confirmRequired[spec.Name]; ok {
			confirmRequired[spec.Name] = spec.ConfirmField == "confirm"
		}
	}

	for name, ok := range confirmRequired {
		if !ok {
			t.Fatalf("%s should require confirm=true", name)
		}
	}
}
