// cmd_vendor_specs.go — `loom vendor-specs check` command.
//
// Compares vendor documentation against a static manifest and our generator
// test fixtures to surface drift before it reaches production. Shipped as
// Slice H following the 2026-04-18 Codex regression where the generator
// emitted an invalid `approval_mode = "always"` key.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/crb2nu/loom/pkg/generator"
)

// vendorSpecsFetcher is the package-level DocFetcher used by the CLI.
// Tests override this via newVendorSpecsCmdWithFetcher to avoid real HTTP.
var vendorSpecsFetcher generator.DocFetcher = &httpDocFetcher{timeout: 10 * time.Second}

type httpDocFetcher struct {
	timeout time.Duration
}

func (h *httpDocFetcher) Fetch(ctx context.Context, url string) (string, error) {
	client := &http.Client{Timeout: h.timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "loom-vendor-specs-check/1")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func newVendorSpecsCmd() *cobra.Command {
	return newVendorSpecsCmdWithFetcher(nil)
}

// newVendorSpecsCmdWithFetcher is the test seam: pass a stub fetcher to
// avoid network calls. Passing nil uses the default HTTP fetcher.
func newVendorSpecsCmdWithFetcher(fetcher generator.DocFetcher) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vendor-specs",
		Short: "Check vendor documentation against the static manifest for drift",
	}
	cmd.AddCommand(newVendorSpecsCheckCmd(fetcher))
	return cmd
}

func newVendorSpecsCheckCmd(fetcher generator.DocFetcher) *cobra.Command {
	var (
		jsonOut      bool
		manifestPath string
	)
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Fetch vendor docs and assert our generator matches them",
		Long: `Load pkg/generator/vendor_specs.yaml, fetch each vendor's docs_url
(and any extra_urls), assert required substrings appear and deprecated ones
do not, and verify every emitted_keys entry shows up in
pkg/generator/configs_test.go. Exits non-zero on any assertion failure so CI
can flag vendor drift early.`,
		Example: `  loom vendor-specs check
  loom vendor-specs check --json
  loom vendor-specs check --manifest pkg/generator/vendor_specs.yaml`,
		// Keep stdout clean on drift failures so --json output remains
		// parseable; the report itself already lists every failure.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			f := fetcher
			if f == nil {
				f = vendorSpecsFetcher
			}
			return runVendorSpecsCheck(cmd.OutOrStdout(), f, manifestPath, jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output results as JSON")
	cmd.Flags().StringVar(&manifestPath, "manifest", "", "Path to vendor_specs.yaml (default: pkg/generator/vendor_specs.yaml relative to workspace root)")
	return cmd
}

// resolveManifestPath finds the vendor_specs.yaml and the sibling
// configs_test.go fixture, walking up from cwd to locate the repo root when
// no explicit path is provided.
func resolveManifestPath(override string) (manifest, fixture string, err error) {
	if override != "" {
		dir := filepath.Dir(override)
		return override, filepath.Join(dir, "configs_test.go"), nil
	}

	cwd, _ := os.Getwd()
	// Walk up at most 10 levels looking for pkg/generator/vendor_specs.yaml.
	dir := cwd
	for range 11 {
		candidate := filepath.Join(dir, "pkg", "generator", "vendor_specs.yaml")
		if _, statErr := os.Stat(candidate); statErr == nil {
			return candidate, filepath.Join(dir, "pkg", "generator", "configs_test.go"), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", "", fmt.Errorf("could not locate pkg/generator/vendor_specs.yaml (tried walking up from %s); pass --manifest to override", cwd)
}

// runVendorSpecsCheck loads the manifest, iterates vendors, prints a report,
// and returns an error when any vendor fails so the CLI exits non-zero.
func runVendorSpecsCheck(out io.Writer, fetcher generator.DocFetcher, manifestOverride string, jsonOut bool) error {
	manifest, fixturePath, err := resolveManifestPath(manifestOverride)
	if err != nil {
		return err
	}
	specs, err := generator.LoadVendorSpecs(manifest)
	if err != nil {
		return err
	}

	fixtureBytes, err := os.ReadFile(fixturePath)
	if err != nil {
		// Fixture missing is a hard failure: without it we cannot check emitted_keys.
		return fmt.Errorf("read fixture %s: %w", fixturePath, err)
	}
	fixture := string(fixtureBytes)

	ctx := context.Background()
	results := make([]generator.CheckResult, 0, len(specs))
	allPassed := true
	for _, spec := range specs {
		r := generator.CheckVendor(ctx, spec, fetcher, fixture)
		if !r.Passed {
			allPassed = false
		}
		results = append(results, r)
	}

	if jsonOut {
		payload := struct {
			Passed  bool                    `json:"passed"`
			Results []generator.CheckResult `json:"results"`
			Meta    map[string]string       `json:"meta"`
		}{
			Passed:  allPassed,
			Results: results,
			Meta: map[string]string{
				"manifest": manifest,
				"fixture":  fixturePath,
			},
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(payload); err != nil {
			return err
		}
	} else {
		printVendorSpecsHuman(out, results)
	}

	if !allPassed {
		return fmt.Errorf("vendor drift detected; see report above")
	}
	return nil
}

func printVendorSpecsHuman(out io.Writer, results []generator.CheckResult) {
	fmt.Fprintf(out, "%-15s %-8s %s\n", "VENDOR", "STATUS", "DOCS_URL")
	fmt.Fprintln(out, "------------------------------------------------------------")
	for _, r := range results {
		status := "ok"
		if !r.Passed {
			status = "DRIFT"
		}
		fmt.Fprintf(out, "%-15s %-8s %s\n", r.Vendor, status, r.DocsURL)
		for _, f := range r.Failures {
			fmt.Fprintf(out, "  - [%s] %s\n", f.Kind, f.Message)
		}
	}
}
