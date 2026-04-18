package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crb2nu/loom/pkg/generator"
)

type fakeFetcher struct {
	bodies map[string]string
}

func (f *fakeFetcher) Fetch(_ context.Context, url string) (string, error) {
	if b, ok := f.bodies[url]; ok {
		return b, nil
	}
	// Return empty body for unmapped urls so stub doesn't error; the
	// must_contain checks will flag missing tokens on their own.
	return "", nil
}

// testManifest returns the path to the real manifest and fixture in
// pkg/generator/ relative to this test file.
func testManifest(t *testing.T) (manifest, fixture string) {
	t.Helper()
	// cmd/loom/cmd_vendor_specs_test.go -> ../../pkg/generator
	manifest = filepath.Join("..", "..", "pkg", "generator", "vendor_specs.yaml")
	fixture = filepath.Join("..", "..", "pkg", "generator", "configs_test.go")
	return manifest, fixture
}

func TestVendorSpecsCheckCmd_JSONOutput(t *testing.T) {
	t.Parallel()
	manifest, _ := testManifest(t)

	// Canned docs so we don't hit the network.
	fake := &fakeFetcher{bodies: map[string]string{
		"https://developers.openai.com/codex/mcp":                      "configure default_tools_approval_mode to approve or prompt",
		"https://developers.openai.com/codex/agent-approvals-security": "confirmations require prompt",
		"https://kilocode.ai":                                          "set always_allow for trusted tools",
		"https://docs.claude.com/claude-code":                          "configure the mcp server",
		"https://github.com/google-gemini/gemini-cli":                  "mcp configuration",
	}}

	cmd := newVendorSpecsCmdWithFetcher(fake)
	cmd.SetArgs([]string{"check", "--json", "--manifest", manifest})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	// Command returns non-nil error if any vendor fails. We don't assert the
	// return value because fixture drift (approval_mode = "always" still
	// present pre-Slice-G) may cause a legitimate emitted_key failure. What
	// we DO assert: output is valid JSON with expected structure.
	_ = cmd.Execute()

	var payload struct {
		Passed  bool                    `json:"passed"`
		Results []generator.CheckResult `json:"results"`
		Meta    map[string]string       `json:"meta"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json output not parseable: %v\nraw:\n%s", err, out.String())
	}
	if len(payload.Results) == 0 {
		t.Fatal("expected at least one vendor result")
	}
	if payload.Meta["manifest"] == "" {
		t.Error("meta.manifest empty")
	}
	// Each result must have a stamp + URL.
	for _, r := range payload.Results {
		if r.Vendor == "" {
			t.Error("vendor name empty")
		}
		if r.CheckedAt.IsZero() {
			t.Errorf("vendor %s missing CheckedAt", r.Vendor)
		}
	}
}

func TestVendorSpecsCheckCmd_HumanOutput(t *testing.T) {
	t.Parallel()
	manifest, _ := testManifest(t)

	fake := &fakeFetcher{bodies: map[string]string{}}
	cmd := newVendorSpecsCmdWithFetcher(fake)
	cmd.SetArgs([]string{"check", "--manifest", manifest})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	_ = cmd.Execute()

	s := out.String()
	if !strings.Contains(s, "VENDOR") || !strings.Contains(s, "STATUS") {
		t.Errorf("expected table header in human output, got:\n%s", s)
	}
	if !strings.Contains(s, "codex") {
		t.Errorf("expected codex row in human output, got:\n%s", s)
	}
}

func TestVendorSpecsCheckCmd_AllGreenExitsZero(t *testing.T) {
	t.Parallel()
	// Write a minimal manifest + fixture to a tempdir so we fully control inputs.
	dir := t.TempDir()
	manifest := filepath.Join(dir, "vendor_specs.yaml")
	fixture := filepath.Join(dir, "configs_test.go")
	if err := writeFile(manifest, `vendors:
  testvendor:
    docs_url: https://example.test/docs
    must_contain:
      - required_key
    must_not_contain:
      - bad_key
    emitted_keys:
      - required_key_value
`); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(fixture, `package x
// fixture contains required_key_value
`); err != nil {
		t.Fatal(err)
	}

	fake := &fakeFetcher{bodies: map[string]string{
		"https://example.test/docs": "docs mention required_key and nothing else",
	}}
	cmd := newVendorSpecsCmdWithFetcher(fake)
	cmd.SetArgs([]string{"check", "--json", "--manifest", manifest})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected passing check, got err: %v\nout:\n%s", err, out.String())
	}
}

func TestVendorSpecsCheckCmd_DriftExitsNonZero(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := filepath.Join(dir, "vendor_specs.yaml")
	fixture := filepath.Join(dir, "configs_test.go")
	if err := writeFile(manifest, `vendors:
  testvendor:
    docs_url: https://example.test/docs
    must_contain:
      - required_key
    emitted_keys:
      - required_key_value
`); err != nil {
		t.Fatal(err)
	}
	// Fixture lacks the emitted key -> drift.
	if err := writeFile(fixture, `package x
`); err != nil {
		t.Fatal(err)
	}

	fake := &fakeFetcher{bodies: map[string]string{
		"https://example.test/docs": "required_key",
	}}
	cmd := newVendorSpecsCmdWithFetcher(fake)
	cmd.SetArgs([]string{"check", "--manifest", manifest})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected drift to produce a non-nil error, got nil.\nout:\n%s", out.String())
	}
}

// writeFile is a tiny helper so tests stay concise.
func writeFile(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o644)
}
