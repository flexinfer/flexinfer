package generator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubFetcher is an in-memory DocFetcher for tests. No network.
type stubFetcher struct {
	bodies map[string]string
	errs   map[string]error
}

func (s *stubFetcher) Fetch(_ context.Context, url string) (string, error) {
	if err, ok := s.errs[url]; ok {
		return "", err
	}
	body, ok := s.bodies[url]
	if !ok {
		return "", errors.New("no canned body for url")
	}
	return body, nil
}

func TestLoadVendorSpecs_RealManifest(t *testing.T) {
	t.Parallel()
	// Manifest lives alongside this file.
	path := "vendor_specs.yaml"
	specs, err := LoadVendorSpecs(path)
	if err != nil {
		t.Fatalf("LoadVendorSpecs: %v", err)
	}
	if len(specs) < 2 {
		t.Fatalf("expected multiple vendors, got %d", len(specs))
	}

	// Check the load-bearing codex entry is fully populated.
	var codex *VendorSpec
	for i := range specs {
		if specs[i].Name == "codex" {
			codex = &specs[i]
			break
		}
	}
	if codex == nil {
		t.Fatal("codex vendor not found in manifest")
	}
	if codex.DocsURL == "" {
		t.Error("codex docs_url empty")
	}
	if len(codex.MustContain) == 0 {
		t.Error("codex must_contain empty — manifest is a stub")
	}
	if len(codex.MustNotContain) == 0 {
		t.Error("codex must_not_contain empty — manifest is a stub")
	}
	if len(codex.EmittedKeys) == 0 {
		t.Error("codex emitted_keys empty — manifest is a stub")
	}
}

func TestLoadVendorSpecs_MissingFile(t *testing.T) {
	t.Parallel()
	_, err := LoadVendorSpecs(filepath.Join(t.TempDir(), "nope.yaml"))
	if err == nil {
		t.Fatal("expected error for missing manifest, got nil")
	}
}

func TestLoadVendorSpecs_EmptyManifest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(p, []byte("vendors: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadVendorSpecs(p)
	if err == nil || !strings.Contains(err.Error(), "no vendors") {
		t.Fatalf("expected 'no vendors' error, got %v", err)
	}
}

func TestCheckVendor_AllGreen(t *testing.T) {
	t.Parallel()
	spec := VendorSpec{
		Name:           "codex",
		DocsURL:        "https://example.test/codex",
		MustContain:    []string{"default_tools_approval_mode", "approve"},
		MustNotContain: []string{"approval_mode = \"always\""},
		EmittedKeys:    []string{`default_tools_approval_mode = "approve"`},
	}
	fetcher := &stubFetcher{bodies: map[string]string{
		"https://example.test/codex": "Configure the server with default_tools_approval_mode = \"approve\" to require user confirmation.",
	}}
	fixture := `if !strings.Contains(content, ` + "`" + `default_tools_approval_mode = "approve"` + "`" + `) { t.Fatal() }`

	res := CheckVendor(context.Background(), spec, fetcher, fixture)
	if !res.Passed {
		t.Fatalf("expected passed, got failures: %+v", res.Failures)
	}
	if res.CheckedAt.IsZero() {
		t.Error("CheckedAt not populated")
	}
}

func TestCheckVendor_MustContainMissing(t *testing.T) {
	t.Parallel()
	spec := VendorSpec{
		Name:        "codex",
		DocsURL:     "https://example.test/codex",
		MustContain: []string{"default_tools_approval_mode"},
	}
	fetcher := &stubFetcher{bodies: map[string]string{
		"https://example.test/codex": "docs no longer mention the key",
	}}

	res := CheckVendor(context.Background(), spec, fetcher, "")
	if res.Passed {
		t.Fatal("expected failure when must_contain token missing")
	}
	if len(res.Failures) != 1 {
		t.Fatalf("expected 1 failure, got %d", len(res.Failures))
	}
	if res.Failures[0].Kind != "must_contain" {
		t.Errorf("expected kind=must_contain, got %s", res.Failures[0].Kind)
	}
	if res.Failures[0].Token != "default_tools_approval_mode" {
		t.Errorf("unexpected token: %s", res.Failures[0].Token)
	}
}

func TestCheckVendor_MustNotContainPresent(t *testing.T) {
	t.Parallel()
	spec := VendorSpec{
		Name:           "codex",
		DocsURL:        "https://example.test/codex",
		MustNotContain: []string{"always_allow"},
	}
	fetcher := &stubFetcher{bodies: map[string]string{
		"https://example.test/codex": "some vendors use always_allow for this",
	}}

	res := CheckVendor(context.Background(), spec, fetcher, "")
	if res.Passed {
		t.Fatal("expected failure when must_not_contain token present")
	}
	if res.Failures[0].Kind != "must_not_contain" {
		t.Errorf("expected kind=must_not_contain, got %s", res.Failures[0].Kind)
	}
}

func TestCheckVendor_EmittedKeyMissingInFixture(t *testing.T) {
	t.Parallel()
	spec := VendorSpec{
		Name:        "codex",
		DocsURL:     "https://example.test/codex",
		EmittedKeys: []string{`default_tools_approval_mode = "approve"`},
	}
	fetcher := &stubFetcher{bodies: map[string]string{
		"https://example.test/codex": "",
	}}
	// Fixture source still has the OLD bad key — drift in the other direction.
	fixture := `approval_mode = "always"`

	res := CheckVendor(context.Background(), spec, fetcher, fixture)
	if res.Passed {
		t.Fatal("expected failure when emitted_key missing from fixture")
	}
	var sawEmitted bool
	for _, f := range res.Failures {
		if f.Kind == "emitted_key" {
			sawEmitted = true
		}
	}
	if !sawEmitted {
		t.Fatalf("expected an emitted_key failure, got: %+v", res.Failures)
	}
}

func TestCheckVendor_FetchError(t *testing.T) {
	t.Parallel()
	spec := VendorSpec{
		Name:        "codex",
		DocsURL:     "https://example.test/codex",
		MustContain: []string{"approve"},
		EmittedKeys: []string{"emitted"},
	}
	fetcher := &stubFetcher{errs: map[string]error{
		"https://example.test/codex": errors.New("timeout"),
	}}
	// Even though fetch fails, emitted_keys should still be checked.
	fixture := "emitted"

	res := CheckVendor(context.Background(), spec, fetcher, fixture)
	if res.Passed {
		t.Fatal("expected failure on fetch error")
	}
	var sawFetch bool
	for _, f := range res.Failures {
		if f.Kind == "fetch" {
			sawFetch = true
		}
	}
	if !sawFetch {
		t.Fatalf("expected a fetch failure, got: %+v", res.Failures)
	}
	// must_contain should be skipped (no body to search), emitted_keys passed.
	for _, f := range res.Failures {
		if f.Kind == "must_contain" {
			t.Errorf("must_contain should be skipped when no body fetched, got: %+v", f)
		}
		if f.Kind == "emitted_key" {
			t.Errorf("emitted_key should have passed, got: %+v", f)
		}
	}
}

func TestCheckVendor_ExtraURLsConcatenated(t *testing.T) {
	t.Parallel()
	spec := VendorSpec{
		Name:        "codex",
		DocsURL:     "https://example.test/a",
		ExtraURLs:   []string{"https://example.test/b"},
		MustContain: []string{"token_on_a", "token_on_b"},
	}
	fetcher := &stubFetcher{bodies: map[string]string{
		"https://example.test/a": "token_on_a",
		"https://example.test/b": "token_on_b",
	}}
	res := CheckVendor(context.Background(), spec, fetcher, "")
	if !res.Passed {
		t.Fatalf("expected passed when tokens split across urls, got: %+v", res.Failures)
	}
}
