// vendor_specs.go — loader + drift checker for vendor documentation specs.
//
// This is a pure utility used by the `loom vendor-specs check` subcommand.
// It does NOT touch the network on its own; callers must supply a DocFetcher
// implementation. Tests inject a stub fetcher; the CLI wires a real HTTP
// fetcher with a 10-second timeout.
package generator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// VendorSpec describes drift-check assertions for one vendor.
//
// must_contain / must_not_contain are substrings asserted against the
// fetched doc body (docs_url + each extra_urls page, concatenated).
// emitted_keys are substrings asserted against the generator's test fixture
// source — this catches drift in the OTHER direction, where someone edits
// the generator/test without consulting the vendor manifest.
type VendorSpec struct {
	Name           string   `yaml:"-"`
	DocsURL        string   `yaml:"docs_url"`
	ExtraURLs      []string `yaml:"extra_urls,omitempty"`
	MustContain    []string `yaml:"must_contain,omitempty"`
	MustNotContain []string `yaml:"must_not_contain,omitempty"`
	EmittedKeys    []string `yaml:"emitted_keys,omitempty"`
}

// vendorSpecsFile matches the on-disk YAML shape.
type vendorSpecsFile struct {
	Vendors map[string]VendorSpec `yaml:"vendors"`
}

// DocFetcher abstracts HTTP so tests can stub it without the network.
type DocFetcher interface {
	Fetch(ctx context.Context, url string) (body string, err error)
}

// AssertionFailure captures a single failed assertion for a vendor.
type AssertionFailure struct {
	Kind    string `json:"kind"`            // "must_contain" | "must_not_contain" | "emitted_key" | "fetch"
	Token   string `json:"token,omitempty"` // the missing/forbidden substring
	URL     string `json:"url,omitempty"`   // source URL (for fetch kind)
	Message string `json:"message"`         // human-readable + suggested remedy
}

// CheckResult is one vendor's drift-check outcome.
type CheckResult struct {
	Vendor    string             `json:"vendor"`
	DocsURL   string             `json:"docs_url"`
	CheckedAt time.Time          `json:"checked_at"`
	Passed    bool               `json:"passed"`
	Failures  []AssertionFailure `json:"failures,omitempty"`
}

// LoadVendorSpecs reads the YAML manifest and returns a deterministic slice
// of VendorSpecs ordered alphabetically by name for stable output.
func LoadVendorSpecs(path string) ([]VendorSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read vendor specs: %w", err)
	}
	var file vendorSpecsFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse vendor specs: %w", err)
	}
	if len(file.Vendors) == 0 {
		return nil, errors.New("vendor specs manifest has no vendors")
	}

	// Stable, alphabetical order for reproducible reports.
	names := make([]string, 0, len(file.Vendors))
	for k := range file.Vendors {
		names = append(names, k)
	}
	// sort without pulling in sort.Strings to keep imports tight.
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}

	out := make([]VendorSpec, 0, len(names))
	for _, name := range names {
		v := file.Vendors[name]
		v.Name = name
		out = append(out, v)
	}
	return out, nil
}

// CheckVendor runs all drift assertions for one vendor.
//
// testFixtureSource is the raw text of pkg/generator/configs_test.go (or any
// source we expect emitted_keys to appear in); callers read the file once
// and reuse the string across vendors. The fetcher is called once per URL;
// fetch failures do not short-circuit the rest of the assertions — we still
// check what we can against the fixture source.
func CheckVendor(ctx context.Context, v VendorSpec, fetcher DocFetcher, testFixtureSource string) CheckResult {
	res := CheckResult{
		Vendor:    v.Name,
		DocsURL:   v.DocsURL,
		CheckedAt: time.Now().UTC(),
		Passed:    true,
	}

	// Concatenate doc bodies from docs_url and extra_urls.
	var docBody strings.Builder
	urls := append([]string{v.DocsURL}, v.ExtraURLs...)
	fetchedAny := false
	for _, url := range urls {
		if url == "" {
			continue
		}
		body, err := fetcher.Fetch(ctx, url)
		if err != nil {
			res.Passed = false
			res.Failures = append(res.Failures, AssertionFailure{
				Kind:    "fetch",
				URL:     url,
				Message: fmt.Sprintf("fetch %s failed: %v (check URL validity or network)", url, err),
			})
			continue
		}
		fetchedAny = true
		docBody.WriteString(body)
		docBody.WriteString("\n")
	}
	body := docBody.String()

	// must_contain / must_not_contain only meaningful if we fetched at least one page.
	if fetchedAny {
		for _, token := range v.MustContain {
			if !strings.Contains(body, token) {
				res.Passed = false
				res.Failures = append(res.Failures, AssertionFailure{
					Kind:    "must_contain",
					Token:   token,
					URL:     v.DocsURL,
					Message: fmt.Sprintf("docs missing required token %q; re-read %s to confirm vendor still documents this key", token, v.DocsURL),
				})
			}
		}
		for _, token := range v.MustNotContain {
			if strings.Contains(body, token) {
				res.Passed = false
				res.Failures = append(res.Failures, AssertionFailure{
					Kind:    "must_not_contain",
					Token:   token,
					URL:     v.DocsURL,
					Message: fmt.Sprintf("docs now contain forbidden token %q; likely a deprecated/invalid key resurfacing — verify against %s", token, v.DocsURL),
				})
			}
		}
	}

	// emitted_keys are always checked against the supplied fixture source.
	for _, token := range v.EmittedKeys {
		if !strings.Contains(testFixtureSource, token) {
			res.Passed = false
			res.Failures = append(res.Failures, AssertionFailure{
				Kind:    "emitted_key",
				Token:   token,
				Message: fmt.Sprintf("fixture missing emitted key %q; generator drifted or test fixture was changed without updating the vendor manifest", token),
			})
		}
	}

	return res
}
