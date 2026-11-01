package certify

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// validationRow is one row of the validation matrix's Promotion Matrix table.
type validationRow struct {
	id       string
	title    string
	gpuClass string
	nodes    []string
	backend  string
	support  string
	decision string
	date     string
	// covers is the artifact and canary_command text, searched for lane names.
	covers   string
	artifact string
	source   string
}

// The Promotion Matrix header names these columns; the table is found by them,
// not by position, so a column added later does not shift the parse.
var requiredColumns = []string{"artifact", "gpu_class", "backend", "support_level", "canary_command", "promotion_decision"}

var (
	backtickRe = regexp.MustCompile("`([^`]+)`")
	boldRe     = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	dateRe     = regexp.MustCompile(`\b(20\d\d-[01]\d-[0-3]\d)\b`)
	archRe     = regexp.MustCompile(`^(gfx[0-9a-f]+|sm_[0-9]+)\b`)
	idTokenRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:._/+-]*$`)
	nonSlugRe  = regexp.MustCompile(`[^a-z0-9]+`)
)

// loadValidationRows parses the first table whose header carries the
// required columns. Rows whose cell count differs from the header's are an
// error: they would mis-render on GitLab too.
func loadValidationRows(root, rel string) ([]validationRow, error) {
	path := filepath.Join(root, rel)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	source := relSlash(root, path)
	lines := strings.Split(string(data), "\n")

	header := -1
	var cols map[string]int
	for i := 0; i+1 < len(lines); i++ {
		if !strings.HasPrefix(lines[i], "|") || !strings.HasPrefix(lines[i+1], "|---") {
			continue
		}
		c := map[string]int{}
		for j, cell := range splitRow(lines[i]) {
			c[strings.Trim(cell, "` ")] = j
		}
		ok := true
		for _, name := range requiredColumns {
			if _, found := c[name]; !found {
				ok = false
				break
			}
		}
		if ok {
			header, cols = i, c
			break
		}
	}
	if header < 0 {
		return nil, fmt.Errorf("%s: no table with columns %s", source, strings.Join(requiredColumns, ", "))
	}
	width := len(splitRow(lines[header]))

	var rows []validationRow
	seen := map[string]int{}
	for i := header + 2; i < len(lines) && strings.HasPrefix(lines[i], "|"); i++ {
		cells := splitRow(lines[i])
		if len(cells) != width {
			return nil, fmt.Errorf("%s:%d: row has %d cells, the header has %d (escape | inside cells as \\|)", source, i+1, len(cells), width)
		}
		cell := func(name string) string { return cells[cols[name]] }
		artifact := cell("artifact")
		r := validationRow{
			backend:  rowBackend(cell("backend")),
			support:  rowSupport(cell("support_level")),
			decision: rowDecision(cell("promotion_decision")),
			covers:   artifact + "\n" + cell("canary_command"),
			artifact: artifact,
			source:   source,
		}
		r.gpuClass, r.nodes = rowGPUClass(cell("gpu_class"))
		if m := dateRe.FindString(artifact); m != "" {
			r.date = m
		} else {
			r.date = dateRe.FindString(lines[i])
		}
		r.id, r.title = rowIDTitle(artifact)
		if r.date != "" && !strings.Contains(r.id, r.date) {
			r.id += "@" + r.date
		}
		seen[r.id]++
		if n := seen[r.id]; n > 1 {
			r.id = fmt.Sprintf("%s#%d", r.id, n)
		}
		rows = append(rows, r)
	}
	return rows, nil
}

// splitRow splits a markdown table row on unescaped pipes.
func splitRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	var cells []string
	var cur strings.Builder
	for i := 0; i < len(line); i++ {
		if line[i] == '\\' && i+1 < len(line) && line[i+1] == '|' {
			cur.WriteByte('|')
			i++
			continue
		}
		if line[i] == '|' {
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
			continue
		}
		cur.WriteByte(line[i])
	}
	return append(cells, strings.TrimSpace(cur.String()))
}

// verdict reports whether the row records a decision rather than a plan.
func (r validationRow) verdict() bool {
	switch r.decision {
	case DecisionPromote, DecisionPass, DecisionConditional, DecisionFail, DecisionBlock, DecisionParked:
		return true
	}
	return false
}

func (r validationRow) entry() Entry {
	return Entry{
		ID:           r.id,
		Kind:         KindValidation,
		SupportLevel: r.support,
		Backend:      r.backend,
		GPUClass:     r.gpuClass,
		Nodes:        r.nodes,
		Decision:     r.decision,
		Title:        r.title,
		Evidenced:    r.verdict(),
		Evidence:     r.anchor(),
		Date:         strPtr(r.date),
		Source:       r.source,
	}
}

// anchor is the pointer convention the certificates already use
// (.loom/60-validation-matrix.md#<row-id>).
func (r validationRow) anchor() string { return r.source + "#" + r.id }

// rowIDTitle takes the row id from the artifact cell's leading code span,
// else slugs its first words; the title is the rest of the cell, stripped of
// markdown and cut at 200 characters.
func rowIDTitle(artifact string) (id, title string) {
	s := strings.TrimSpace(artifact)
	if strings.HasPrefix(s, "`") {
		if end := strings.Index(s[1:], "`"); end > 0 && idTokenRe.MatchString(s[1:1+end]) {
			id = s[1 : 1+end]
			title = strings.TrimSpace(s[2+end:])
			title = strings.TrimSpace(strings.TrimLeft(title, "—-:"))
		}
	}
	if id == "" && strings.HasPrefix(s, "**") {
		if m := boldRe.FindStringSubmatch(s); m != nil {
			id = strings.Trim(nonSlugRe.ReplaceAllString(strings.ToLower(stripMarkdown(m[1])), "-"), "-")
			title = s
		}
	}
	if id == "" {
		plain := stripMarkdown(s)
		words := strings.Fields(plain)
		if len(words) > 6 {
			words = words[:6]
		}
		id = strings.Trim(nonSlugRe.ReplaceAllString(strings.ToLower(strings.Join(words, " ")), "-"), "-")
		title = s
	}
	return id, truncate(stripMarkdown(title), 200)
}

func stripMarkdown(s string) string {
	s = strings.NewReplacer("**", "", "`", "", "<br>", " ").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := strings.LastIndex(s[:n], " ")
	if cut < n/2 {
		cut = n
	}
	return strings.TrimRight(s[:cut], " ,;:—-") + "…"
}

// rowGPUClass reads `gfx1100/7900xtx`, `gfx1100/7900xtx+5930k` and
// `gfx1100/7900xtx` + `gfx1100/5930k` forms. Control-plane rows (k3s-cp-1,
// n/a) are class n/a.
func rowGPUClass(cell string) (class string, nodes []string) {
	for _, tok := range backtickRe.FindAllStringSubmatch(cell, -1) {
		t := strings.TrimSpace(tok[1])
		m := archRe.FindString(t)
		if m == "" {
			continue
		}
		if class == "" {
			class = m
		}
		if m != class {
			continue
		}
		if _, rest, ok := strings.Cut(t, "/"); ok {
			for _, n := range strings.Split(rest, "+") {
				if n = strings.TrimSpace(strings.Fields(n + " ")[0]); n != "" {
					nodes = append(nodes, n)
				}
			}
		}
	}
	if class == "" {
		return SupportNA, nil
	}
	return class, sortedUnique(nodes)
}

func rowBackend(cell string) string {
	t := firstToken(cell)
	if f := strings.Fields(t); len(f) > 0 {
		t = f[0]
	}
	t = strings.ToLower(strings.TrimRight(t, ":,"))
	if t == "" {
		return SupportNA
	}
	return t
}

// rowSupport reads the posture after the last arrow (`experimental` →
// `supported`), falling back to the cell's first code span.
func rowSupport(cell string) string {
	t := ""
	if i := strings.LastIndex(cell, "→"); i >= 0 {
		if m := backtickRe.FindStringSubmatch(cell[i:]); m != nil {
			t = m[1]
		}
	}
	if t == "" {
		t = firstToken(cell)
	}
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "supported", "production":
		return SupportSupported
	case "experimental", "conditional":
		return SupportExperimental
	case "deprecated":
		return SupportDeprecated
	case "unsupported":
		return SupportUnsupported
	case "parked":
		return SupportParked
	default:
		return SupportNA
	}
}

// Decisions, normalized.
const (
	DecisionPromote     = "promote"
	DecisionPass        = "pass"
	DecisionConditional = "conditional"
	DecisionPending     = "pending"
	DecisionFail        = "fail"
	DecisionBlock       = "block"
	DecisionSkip        = "skip"
	DecisionSuperseded  = "superseded"
	DecisionParked      = "parked"
	DecisionOther       = "other"
)

func rowDecision(cell string) string {
	t := ""
	b := backtickRe.FindStringSubmatchIndex(cell)
	bold := boldRe.FindStringSubmatchIndex(cell)
	switch {
	case b != nil && (bold == nil || b[0] <= bold[1]):
		t = cell[b[2]:b[3]]
	case bold != nil:
		t = cell[bold[2]:bold[3]]
	default:
		if f := strings.Fields(cell); len(f) > 0 {
			t = f[0]
		}
	}
	t = strings.ToLower(strings.Trim(t, "`*:—- "))
	switch {
	case t == "promote" || t == "production" || strings.HasPrefix(t, "promote"):
		return DecisionPromote
	case t == "pass" || t == "keep auto" || strings.HasPrefix(t, "pass"):
		return DecisionPass
	case t == "conditional" || t == "feasibility-only":
		return DecisionConditional
	case t == "pending":
		return DecisionPending
	case t == "fail":
		return DecisionFail
	case t == "block":
		return DecisionBlock
	case t == "skip":
		return DecisionSkip
	case t == "superseded":
		return DecisionSuperseded
	case t == "parked":
		return DecisionParked
	default:
		return DecisionOther
	}
}

func firstToken(cell string) string {
	if m := backtickRe.FindStringSubmatch(cell); m != nil {
		return m[1]
	}
	if f := strings.Fields(stripMarkdown(cell)); len(f) > 0 {
		return f[0]
	}
	return ""
}

// namesLane reports whether lane appears in text as a whole name (not as a
// prefix of a longer lane name such as gemma4-26b-a4b-gptq-long).
func namesLane(text, lane string) bool {
	for i := 0; ; {
		j := strings.Index(text[i:], lane)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(lane)
		if (start == 0 || !laneRune(text[start-1])) && (end == len(text) || !laneRune(text[end])) {
			return true
		}
		i = start + 1
	}
}

func laneRune(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.'
}
