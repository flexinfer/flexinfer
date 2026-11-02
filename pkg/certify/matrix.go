// Package certify renders the public certification matrix: what flexinfer
// serves on which consumer GPU class, at what support level, and the evidence
// behind each claim (D7, .loom/10-research-gfx1100-gfx906-next-differentiators-2026-10-31.md §6.7).
//
// The matrix is generated offline from files in the repository so CI can
// render it without cluster access:
//
//   - GPUProfile manifests: the declared per-backend posture
//     (spec.backends.<b>.support) and the backend-capability certificates
//     (flexinfer.ai/backend-certificate-* annotation triples);
//   - Model manifests (the lanes): backend, node, modality and the
//     flexinfer.ai/promotion-evidence annotation;
//   - the Promotion Matrix table of .loom/60-validation-matrix.md;
//   - the chart values' runtime.profiles, which map nodes to GPU classes.
//
// Rendering is deterministic (sorted, no timestamps) so a checked-in copy can
// be compared byte for byte.
package certify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Schema identifies the JSON layout. Bump the suffix on a breaking change.
const Schema = "flexinfer.ai/certification-matrix/v1"

// Entry kinds.
const (
	KindBackend     = "backend"     // a GPUProfile's declared posture for one backend
	KindCertificate = "certificate" // a backend-capability certificate on a GPUProfile
	KindLane        = "lane"        // one Model manifest
	KindValidation  = "validation"  // one row of the validation matrix
)

// Support levels. Every entry carries exactly one.
const (
	SupportCertified    = "certified"    // complete certificate triple (image, since, evidence)
	SupportSupported    = "supported"    // declared full, or validated as supported/production
	SupportExperimental = "experimental" // declared or validated experimental
	SupportDeprecated   = "deprecated"
	SupportUnsupported  = "unsupported"
	SupportParked       = "parked"     // validated but not startable
	SupportIncomplete   = "incomplete" // partial certificate: treated as uncertified
	SupportUnverified   = "unverified" // no evidence in any source
	SupportNA           = "n/a"
)

// Matrix is the rendered document.
type Matrix struct {
	Schema     string     `json:"schema"`
	Sources    Sources    `json:"sources"`
	Rules      []string   `json:"rules"`
	GPUClasses []GPUClass `json:"gpu_classes"`
	Modalities []Modality `json:"modalities"`
	Entries    []Entry    `json:"entries"`
}

// Sources records the inputs, relative to the repository root.
type Sources struct {
	GPUProfiles      string `json:"gpu_profiles"`
	Models           string `json:"models"`
	ValidationMatrix string `json:"validation_matrix"`
	NodeClasses      string `json:"node_classes"`
}

// GPUClass is one GPUProfile architecture and the nodes that carry it.
type GPUClass struct {
	Class   string   `json:"class"`
	Vendor  string   `json:"vendor"`
	Product string   `json:"product,omitempty"`
	VRAMMB  int64    `json:"vram_mb,omitempty"`
	Profile string   `json:"profile"`
	Nodes   []string `json:"nodes,omitempty"`
	Source  string   `json:"source"`
}

// Modality is the claim the matrix supports for one kind of workload.
type Modality struct {
	Modality string `json:"modality"`
	// Level is the strongest claim an evidenced, non-canary GPU lane of this
	// modality supports: supported, experimental or unverified.
	Level          string   `json:"level"`
	Lanes          int      `json:"lanes"`
	EvidencedLanes int      `json:"evidenced_lanes"`
	GPUClasses     []string `json:"gpu_classes"`
	LatestEvidence *string  `json:"latest_evidence_date"`
	// Evidence lists the lanes (or, for rag, validation rows) behind Level.
	Evidence []string `json:"evidence,omitempty"`
	Note     string   `json:"note,omitempty"`
}

// Entry is one claim with its evidence pointer.
type Entry struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	SupportLevel string   `json:"support_level"`
	Backend      string   `json:"backend"`
	GPUClass     string   `json:"gpu_class"`
	Nodes        []string `json:"nodes,omitempty"`
	Capability   string   `json:"capability,omitempty"`
	Model        string   `json:"model,omitempty"`
	Modality     string   `json:"modality,omitempty"`
	Canary       bool     `json:"canary,omitempty"`
	Decision     string   `json:"decision,omitempty"`
	Title        string   `json:"title,omitempty"`
	Image        string   `json:"image,omitempty"`
	// Evidenced is false for a declaration with nothing behind it: a backend
	// posture with no validation row, an incomplete certificate, an
	// unverified lane, a validation row that is still a plan (pending, skip).
	Evidenced      bool     `json:"evidenced"`
	Evidence       string   `json:"evidence"`
	EvidenceNote   string   `json:"evidence_note,omitempty"`
	ValidationRows []string `json:"validation_rows,omitempty"`
	// Date is the evidence date (YYYY-MM-DD); null when no source dates it.
	Date   *string `json:"date"`
	Source string  `json:"source"`
}

// Options names the inputs, relative to Root.
type Options struct {
	Root             string
	GPUProfiles      string // glob
	Models           string // glob
	ValidationMatrix string
	NodeClasses      string // chart values file with runtime.profiles
}

// DefaultOptions are the repository's own paths.
func DefaultOptions() Options {
	return Options{
		Root:             ".",
		GPUProfiles:      "deploy/gpuprofiles/*.yaml",
		Models:           "deploy/models/*.yaml",
		ValidationMatrix: ".loom/60-validation-matrix.md",
		NodeClasses:      "deploy/system/values-k3s.yaml",
	}
}

// rules ships the derivation contract inside the document, so a reader of the
// JSON does not need this package to interpret it.
var rules = []string{
	"backend: a GPUProfile's spec.backends.<backend>.support as declared (full=supported); evidence and date are the evidence row (below) with the same GPU class and backend, else the profile file, null and evidenced=false.",
	"certificate: a flexinfer.ai/backend-certificate-<backend>-<capability> annotation; certified only when the image, -since and -evidence keys are all present, else incomplete.",
	"lane: one Model manifest; its GPU class comes from its kubernetes.io/hostname node selector via the chart's runtime.profiles, else the only GPUProfile of its GPU vendor (CPU lanes are class cpu). Its support level and decision come from the evidence row that names the lane in the artifact or canary_command cell; the evidence pointer is that row or the flexinfer.ai/promotion-evidence annotation, whichever is newer; with only the annotation it is supported; when only plan rows (pending, skip) name it, or nothing does, it is unverified.",
	"validation: one row of the validation matrix's Promotion Matrix table; support level after the last arrow of the support_level cell, decision from the promotion_decision cell, date from the row id or the first date in the row. The evidence row for a claim is the newest matching row with a verdict (promote, pass, conditional, fail, block, parked), else the newest matching row.",
	"modality: counts evidenced, non-canary GPU lanes only; supported when one of them is supported with a promote, pass or conditional decision (or carries promotion evidence and no row); experimental when one exists that did not fail or block; else unverified. rag has no lane: it needs a validation row whose artifact cell names rag or /v1/rag.",
}

// Render reads the inputs and builds the matrix.
func Render(opts Options) (*Matrix, error) {
	if opts.Root == "" {
		opts.Root = "."
	}
	nodeClasses, err := loadNodeClasses(filepath.Join(opts.Root, opts.NodeClasses))
	if err != nil {
		return nil, err
	}
	profiles, err := loadProfiles(opts.Root, opts.GPUProfiles)
	if err != nil {
		return nil, err
	}
	rows, err := loadValidationRows(opts.Root, opts.ValidationMatrix)
	if err != nil {
		return nil, err
	}
	lanes, err := loadLanes(opts.Root, opts.Models)
	if err != nil {
		return nil, err
	}

	lanesOut := laneEntries(lanes, rows, profiles, nodeClasses)
	m := &Matrix{
		Schema: Schema,
		Sources: Sources{
			GPUProfiles:      filepath.ToSlash(opts.GPUProfiles),
			Models:           filepath.ToSlash(opts.Models),
			ValidationMatrix: filepath.ToSlash(opts.ValidationMatrix),
			NodeClasses:      filepath.ToSlash(opts.NodeClasses),
		},
		Rules:      rules,
		GPUClasses: gpuClasses(profiles, nodeClasses, lanesOut),
	}

	var entries []Entry
	for _, p := range profiles {
		entries = append(entries, p.backendEntries(rows)...)
		entries = append(entries, p.certificateEntries()...)
	}
	entries = append(entries, lanesOut...)
	for _, r := range rows {
		entries = append(entries, r.entry())
	}
	sortEntries(entries)
	m.Entries = entries
	m.Modalities = modalities(lanesOut, rows)
	return m, nil
}

// Marshal renders the matrix as indented JSON with a trailing newline.
func (m *Matrix) Marshal() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Check returns an error unless the file at path holds exactly rendered.
func Check(rendered []byte, path string) error {
	current, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, rendered) {
		return fmt.Errorf("%s is stale: regenerate it with `make certify-matrix` and commit the result", path)
	}
	return nil
}

var kindOrder = map[string]int{KindBackend: 0, KindCertificate: 1, KindLane: 2, KindValidation: 3}

// sortEntries orders backend, certificate and lane entries by GPU class,
// backend and id, and validation rows newest first in file order.
func sortEntries(entries []Entry) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if kindOrder[a.Kind] != kindOrder[b.Kind] {
			return kindOrder[a.Kind] < kindOrder[b.Kind]
		}
		if a.Kind == KindValidation {
			return dateKey(a.Date) > dateKey(b.Date)
		}
		if a.GPUClass != b.GPUClass {
			return a.GPUClass < b.GPUClass
		}
		if a.Backend != b.Backend {
			return a.Backend < b.Backend
		}
		return a.ID < b.ID
	})
}

func dateKey(d *string) string {
	if d == nil {
		return ""
	}
	return *d
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// relSlash renders path relative to root with forward slashes.
func relSlash(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}

func sortedUnique(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
