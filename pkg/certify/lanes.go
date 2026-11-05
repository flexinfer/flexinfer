package certify

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// Annotation keys read from Model manifests.
const (
	annotationPromotionEvidence = "flexinfer.ai/promotion-evidence"
	annotationCanary            = "flexinfer.ai/canary"
)

// Modalities. The advertised set always renders, with an explicit level, so
// the site never has to infer a claim from an absent key.
const (
	ModalityChat       = "chat"
	ModalityEmbeddings = "embeddings"
	ModalityRerank     = "rerank"
	ModalityASR        = "asr"
	ModalityTTS        = "tts"
	ModalityAudio      = "audio"
	ModalityImage      = "image"
	ModalityVideo      = "video"
	ModalityVision     = "vision"
	ModalityRAG        = "rag"
)

var advertisedModalities = []string{ModalityChat, ModalityEmbeddings, ModalityRerank, ModalityASR, ModalityTTS, ModalityImage, ModalityVideo, ModalityRAG}

// GPU class of lanes that allocate no GPU.
const classCPU = "cpu"

type lane struct {
	obj    *aiv1alpha2.Model
	source string
}

func loadLanes(root, pattern string) ([]lane, error) {
	files, err := filepath.Glob(filepath.Join(root, pattern))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var out []lane
	for _, f := range files {
		docs, err := readDocs(f)
		if err != nil {
			return nil, err
		}
		for _, doc := range docs {
			if kindOf(doc) != "Model" {
				continue
			}
			m := &aiv1alpha2.Model{}
			if err := yaml.Unmarshal(doc, m); err != nil {
				return nil, fmt.Errorf("%s: %w", f, err)
			}
			out = append(out, lane{obj: m, source: relSlash(root, f)})
		}
	}
	return out, nil
}

var matrixRowRe = regexp.MustCompile(`matrix row ([a-z0-9][a-z0-9._-]+[a-z0-9])`)

// laneEntries renders one entry per lane with the evidence rule in rules.
func laneEntries(lanes []lane, rows []validationRow, profiles []profile, nodeClasses map[string]string) []Entry {
	vendorClass := map[string][]string{}
	for _, p := range profiles {
		vendorClass[p.vendor()] = append(vendorClass[p.vendor()], p.class)
	}
	matrixSource := ""
	if len(rows) > 0 {
		matrixSource = rows[0].source
	}

	var out []Entry
	for _, l := range lanes {
		m := l.obj
		node := m.Spec.NodeSelector["kubernetes.io/hostname"]
		e := Entry{
			ID:       m.Name,
			Kind:     KindLane,
			Backend:  m.Spec.Backend,
			Model:    m.Name,
			Modality: laneModality(m),
			Canary:   strings.Contains(m.Name, "canary") || truthy(m.GetAnnotations()[annotationCanary]),
			Source:   l.source,
		}
		if node != "" {
			e.Nodes = []string{node}
		}
		switch {
		case m.Spec.GetGPUCount() == 0 || m.Spec.GetGPUVendor() == aiv1alpha2.GPUVendorCPU:
			e.GPUClass = classCPU
		case nodeClasses[node] != "":
			e.GPUClass = nodeClasses[node]
		case len(vendorClass[string(m.Spec.GetGPUVendor())]) == 1:
			e.GPUClass = vendorClass[string(m.Spec.GetGPUVendor())][0]
		default:
			e.GPUClass = "unknown"
		}

		var covering []string
		for _, r := range rows {
			if namesLane(r.covers, m.Name) {
				covering = append(covering, r.id)
			}
		}
		row, hasRow := evidenceRow(rows, func(r validationRow) bool { return namesLane(r.covers, m.Name) })
		if hasRow {
			e.SupportLevel = row.support
			e.Decision = row.decision
			e.ValidationRows = covering
		}

		pe := strings.TrimSpace(m.GetAnnotations()[annotationPromotionEvidence])
		peDate := dateRe.FindString(pe)
		switch {
		case pe != "" && (!hasRow || peDate >= row.date):
			e.Evidence, e.EvidenceNote = evidencePointer(pe, l.source, matrixSource)
			e.Date = strPtr(peDate)
			e.Evidenced = true
			if !hasRow {
				e.SupportLevel = SupportSupported
			}
		case hasRow:
			e.Evidence = row.anchor()
			e.Date = strPtr(row.date)
			e.Evidenced = row.verdict()
		}
		if !e.Evidenced {
			// Only plans (pending, skip) name this lane: it keeps its rows
			// and decision so a pending kill-test shows, but claims nothing.
			e.SupportLevel = SupportUnverified
		}
		out = append(out, e)
	}
	return out
}

// evidencePointer turns a promotion-evidence annotation into a pointer: the
// value itself when it is a path or reference, the matrix row it names, else
// the lane manifest, with the free text kept as the note.
func evidencePointer(pe, laneSource, matrixSource string) (pointer, note string) {
	if !strings.ContainsAny(pe, " \t\n") {
		return pe, ""
	}
	if m := matrixRowRe.FindStringSubmatch(pe); m != nil && matrixSource != "" {
		return matrixSource + "#" + m[1], pe
	}
	return laneSource + "#" + annotationPromotionEvidence, pe
}

func truthy(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	return v != "" && v != "false" && v != "0" && v != "no"
}

// laneModality classifies a lane from its backend, config and names. The
// order matters: TTS and ASR lanes run on chat backends, audio and video
// lanes on diffusers, rerankers on embedding servers.
func laneModality(m *aiv1alpha2.Model) string {
	cfg := map[string]any{}
	if m.Spec.Config != nil && len(m.Spec.Config.Raw) > 0 {
		_ = json.Unmarshal(m.Spec.Config.Raw, &cfg)
	}
	parts := []string{m.Name, m.Spec.Source}
	parts = append(parts, m.Spec.ServiceLabels...)
	if m.Spec.LiteLLM != nil {
		parts = append(parts, m.Spec.LiteLLM.Aliases...)
	}
	words := strings.ToLower(strings.Join(parts, " "))
	tokens := map[string]bool{}
	for _, t := range nonSlugRe.Split(words, -1) {
		tokens[t] = true
	}
	// has matches substrings; short markers (vl, tts, asr, ocr) must be whole
	// tokens so qwen3-1p7b-vllm is not a vision lane.
	has := func(subs ...string) bool {
		for _, s := range subs {
			if len(s) <= 4 && tokens[s] || len(s) > 4 && strings.Contains(words, s) {
				return true
			}
		}
		return false
	}
	flag := func(key string) bool { v, _ := cfg[key].(bool); return v }
	backend := strings.ToLower(m.Spec.Backend)

	switch {
	case backend == "snac" || has("orpheus", "kokoro", "tts"):
		return ModalityTTS
	case backend == "pyannote" || has("whisper", "transcri", "asr", "diariz"):
		return ModalityASR
	case has("stable-audio", "musicgen"):
		return ModalityAudio
	case backend == "diffusers" || backend == "comfyui":
		if has("wan2", "t2v", "i2v", "video", "vace") {
			return ModalityVideo
		}
		return ModalityImage
	case flag("reranking") || has("rerank", "crossencoder", "cross-encoder"):
		return ModalityRerank
	case flag("embedding") || has("embed"):
		return ModalityEmbeddings
	case (m.Spec.Capabilities != nil && m.Spec.Capabilities.Vision != nil && *m.Spec.Capabilities.Vision) ||
		has("vision", "vl", "ocr", "omni", "multimodal"):
		return ModalityVision
	default:
		return ModalityChat
	}
}

// modalities rolls the lanes up into one claim per modality.
func modalities(lanes []Entry, rows []validationRow) []Modality {
	byName := map[string][]Entry{}
	for _, l := range lanes {
		byName[l.Modality] = append(byName[l.Modality], l)
	}
	names := append([]string{}, advertisedModalities...)
	for name := range byName {
		if !contains(names, name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	var out []Modality
	for _, name := range names {
		mo := Modality{Modality: name, Level: SupportUnverified, Lanes: len(byName[name]), GPUClasses: []string{}}
		var classes, evidencedBy []string
		latest := ""
		for _, l := range byName[name] {
			if !l.Evidenced {
				continue
			}
			mo.EvidencedLanes++
			if l.Canary || l.GPUClass == classCPU || l.GPUClass == "unknown" ||
				l.Decision == DecisionFail || l.Decision == DecisionBlock {
				continue
			}
			strong := (l.SupportLevel == SupportSupported || l.SupportLevel == SupportCertified) &&
				(l.Decision == "" || l.Decision == DecisionPromote || l.Decision == DecisionPass || l.Decision == DecisionConditional)
			switch {
			case strong:
				mo.Level = SupportSupported
			case mo.Level != SupportSupported:
				mo.Level = SupportExperimental
			}
			classes = append(classes, l.GPUClass)
			evidencedBy = append(evidencedBy, l.ID)
			if d := dateKey(l.Date); d > latest {
				latest = d
			}
		}
		if name == ModalityRAG {
			mo.Note = "rag is the proxy's /v1/rag read path, not a lane; it is claimable once a validation row names rag or /v1/rag"
			if r, ok := evidenceRow(rows, func(r validationRow) bool {
				return (namesLane(r.artifact, "rag") || strings.Contains(r.artifact, "/v1/rag")) &&
					r.decision != DecisionFail && r.decision != DecisionBlock
			}); ok {
				mo.Level = r.support
				evidencedBy = append(evidencedBy, r.id)
				classes = append(classes, r.gpuClass)
				latest = r.date
			}
		}
		mo.GPUClasses = sortedUnique(classes)
		if len(evidencedBy) > 0 {
			mo.Evidence = sortedUnique(evidencedBy)
		}
		mo.LatestEvidence = strPtr(latest)
		out = append(out, mo)
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
