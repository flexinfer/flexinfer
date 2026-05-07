package generator

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"

	"gopkg.in/yaml.v3"
)

// TestTemplatesParseValidity walks pkg/generator/templates/ and verifies that
// every *.tmpl parses as text/template and every *.yaml parses as YAML. This
// gates EPIC 3 slice MRs (CONFIG-1..CONFIG-4): a template with a syntax error
// should fail CI here rather than at config-generation time.
//
// The walk tolerates the .gitkeep placeholder files seeded in the pre-flight
// scaffold MR. Once real templates land, the placeholders can stay or be
// removed — they're inert either way.
func TestTemplatesParseValidity(t *testing.T) {
	root := "templates"
	if _, err := os.Stat(root); os.IsNotExist(err) {
		t.Fatalf("templates/ directory missing — pre-flight scaffold not applied")
	}

	var (
		tmplCount   int
		yamlCount   int
		gitkeepSeen bool
	)

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		switch {
		case base == ".gitkeep":
			gitkeepSeen = true
			return nil
		case strings.HasSuffix(base, ".tmpl"):
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Errorf("read %s: %v", path, readErr)
				return nil
			}
			// Templates may reference custom funcs (e.g. buildHooks, json,
			// shellQuote). text/template binds funcs at parse time, so we
			// have to provide them as stubs. Real renders use the
			// closure-bound funcs from hookTemplateFuncs.
			tmpl := template.New(base).Funcs(hookTemplateFuncs(nil, nil, ""))
			if _, parseErr := tmpl.Parse(string(data)); parseErr != nil {
				t.Errorf("template %s parse error: %v", path, parseErr)
				return nil
			}
			tmplCount++
		case strings.HasSuffix(base, ".yaml") || strings.HasSuffix(base, ".yml"):
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Errorf("read %s: %v", path, readErr)
				return nil
			}
			var any any
			if parseErr := yaml.Unmarshal(data, &any); parseErr != nil {
				t.Errorf("yaml %s parse error: %v", path, parseErr)
				return nil
			}
			yamlCount++
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk templates/: %v", walkErr)
	}

	t.Logf("templates/ walk: %d .tmpl files, %d yaml files, gitkeep_seen=%v", tmplCount, yamlCount, gitkeepSeen)
}

// TestPlatformProfilesVersion ensures platform_profiles.yaml declares the
// version this loader expects. Guards against silent schema drift when
// future slices add fields gated on version: 2 (or higher).
func TestPlatformProfilesVersion(t *testing.T) {
	var pf profilesFile
	if err := yaml.Unmarshal(embeddedProfiles, &pf); err != nil {
		t.Fatalf("parse platform_profiles.yaml: %v", err)
	}
	if pf.Version == 0 {
		t.Fatalf("platform_profiles.yaml missing version field — pre-flight scaffold not applied")
	}
	if pf.Version > supportedProfileVersion {
		t.Fatalf("platform_profiles.yaml version=%d > supportedProfileVersion=%d", pf.Version, supportedProfileVersion)
	}
}
