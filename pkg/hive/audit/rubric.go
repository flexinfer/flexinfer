package audit

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"strings"
	"text/template"

	"github.com/crb2nu/loom/pkg/hive/store"
)

//go:embed rubric/*.md
var rubricFS embed.FS

// Rubric pairs the council + pipeline audit prompt templates. Each is a
// text/template with two substitution tags — {{.Artifact}} and
// {{.SidecarJSON}} — so the Dispatcher can render a fully-formed prompt
// from a Request without per-member templating.
type Rubric struct {
	id       string
	council  *template.Template
	pipeline *template.Template
}

// LoadRubric parses the embedded prompt assets. Returns a Rubric ready to
// render against any audit Request. The bundled rubric id is the
// package-level RubricID constant.
func LoadRubric() (*Rubric, error) {
	council, err := parseAsset("rubric/audit_v1_council.md")
	if err != nil {
		return nil, fmt.Errorf("audit: load council rubric: %w", err)
	}
	pipeline, err := parseAsset("rubric/audit_v1_pipeline.md")
	if err != nil {
		return nil, fmt.Errorf("audit: load pipeline rubric: %w", err)
	}
	return &Rubric{id: RubricID, council: council, pipeline: pipeline}, nil
}

// MustLoadRubric is the panic-on-error variant for callers that boot
// the operator with embedded assets — assets are guaranteed to be
// valid by `go test` so a runtime parse failure is a programming error,
// not a user input error.
func MustLoadRubric() *Rubric {
	r, err := LoadRubric()
	if err != nil {
		panic(err)
	}
	return r
}

// ID returns the rubric version stamped onto every persisted finding.
// Callers thread it into store.AuditFinding.RubricID.
func (r *Rubric) ID() string {
	if r == nil {
		return ""
	}
	return r.id
}

// Render returns the fully-substituted prompt for the given request.
// Selects the council vs. pipeline template by SubjectKind and runs it
// against an inputs struct that exposes Artifact + SidecarJSON. Empty
// SidecarJSON renders as the literal "{}" so the prompt stays valid
// JSON even when no sidecar is available.
func (r *Rubric) Render(req *Request, sidecarJSON string) (string, error) {
	if r == nil {
		return "", errors.New("audit: rubric nil")
	}
	if req == nil {
		return "", errors.New("audit: render request nil")
	}
	tmpl, err := r.templateFor(req.SubjectKind)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(sidecarJSON) == "" {
		sidecarJSON = "{}"
	}
	var buf bytes.Buffer
	data := promptInputs{Artifact: req.Artifact, SidecarJSON: sidecarJSON}
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("audit: render: %w", err)
	}
	return buf.String(), nil
}

// templateFor maps subject_kind → template. New subject kinds added in
// future rubric revisions are rejected here so a typo in the caller
// surfaces as a clear error rather than rendering the wrong rubric.
func (r *Rubric) templateFor(kind store.AuditSubjectKind) (*template.Template, error) {
	switch kind {
	case store.AuditSubjectCouncilArtifact:
		return r.council, nil
	case store.AuditSubjectPipelineMerge:
		return r.pipeline, nil
	default:
		return nil, fmt.Errorf("audit: unknown subject_kind %q", kind)
	}
}

// promptInputs is the data passed into the prompt templates. A flat
// struct keeps the templates readable; we add new fields here when the
// rubric grows.
type promptInputs struct {
	Artifact    string
	SidecarJSON string
}

func parseAsset(name string) (*template.Template, error) {
	body, err := rubricFS.ReadFile(name)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("audit: asset %s is empty", name)
	}
	tmpl, err := template.New(name).Parse(string(body))
	if err != nil {
		return nil, err
	}
	return tmpl, nil
}
