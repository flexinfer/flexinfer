package controllers

import (
	"testing"
)

func TestParseValidatorJSON_PlainObject(t *testing.T) {
	in := `{"ok":true,"layout":"vllm-gptq","family":"gemma4-26b-a4b","errors":[],"warnings":[]}`
	m, err := parseValidatorJSON(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !m.Ok {
		t.Errorf("Ok = false, want true")
	}
	if m.Layout != "vllm-gptq" {
		t.Errorf("Layout = %q, want vllm-gptq", m.Layout)
	}
	if m.Family != "gemma4-26b-a4b" {
		t.Errorf("Family = %q, want gemma4-26b-a4b", m.Family)
	}
}

func TestParseValidatorJSON_TrailingNoise(t *testing.T) {
	// Wrapper script tees stdout into termination-log, so set -eu traces or
	// python warnings can sneak in around the JSON document.
	in := "+ python3 /opt/...\n" +
		`{"ok":false,"errors":["bad shape"],"warnings":["flat module"],"layout":"hf-native","family":"unknown"}` +
		"\nWARN: deprecated flag\n"
	m, err := parseValidatorJSON(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Ok {
		t.Errorf("Ok = true, want false")
	}
	if len(m.Errors) != 1 || m.Errors[0] != "bad shape" {
		t.Errorf("errors = %v, want [bad shape]", m.Errors)
	}
	if len(m.Warnings) != 1 || m.Warnings[0] != "flat module" {
		t.Errorf("warnings = %v, want [flat module]", m.Warnings)
	}
}

func TestParseValidatorJSON_NoJSON(t *testing.T) {
	if _, err := parseValidatorJSON("nothing here"); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestParseValidatorJSON_MalformedJSON(t *testing.T) {
	if _, err := parseValidatorJSON(`{"ok": tru`); err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}
