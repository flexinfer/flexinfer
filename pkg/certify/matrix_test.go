package certify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

func fixtureOptions() Options {
	return Options{
		Root:             "testdata",
		GPUProfiles:      "gpuprofiles/*.yaml",
		Models:           "models/*.yaml",
		ValidationMatrix: "matrix.md",
		NodeClasses:      "values.yaml",
	}
}

func renderFixture(t *testing.T) *Matrix {
	t.Helper()
	m, err := Render(fixtureOptions())
	require.NoError(t, err)
	return m
}

func entry(t *testing.T, m *Matrix, kind, id string) Entry {
	t.Helper()
	for _, e := range m.Entries {
		if e.Kind == kind && e.ID == id {
			return e
		}
	}
	t.Fatalf("no %s entry %q", kind, id)
	return Entry{}
}

func modality(t *testing.T, m *Matrix, name string) Modality {
	t.Helper()
	for _, mo := range m.Modalities {
		if mo.Modality == name {
			return mo
		}
	}
	t.Fatalf("no modality %q", name)
	return Modality{}
}

func date(d *string) string { return dateKey(d) }

func TestValidationRows(t *testing.T) {
	rows, err := loadValidationRows("testdata", "matrix.md")
	require.NoError(t, err)
	require.Len(t, rows, 7, "only the Promotion Matrix table, not the contract or archive tables")

	byID := map[string]validationRow{}
	for _, r := range rows {
		byID[r.id] = r
	}

	r := byID["gfx1100-vllm-2026-10-27"]
	assert.Equal(t, "gfx1100", r.gpuClass)
	assert.Equal(t, []string{"b", "xtx"}, r.nodes, "both sides of the arrow")
	assert.Equal(t, "vllm", r.backend)
	assert.Equal(t, SupportSupported, r.support, "posture after the last arrow")
	assert.Equal(t, DecisionPromote, r.decision)
	assert.Equal(t, "2026-10-27", r.date)
	assert.Equal(t, "warm lane on chat-lane-long", r.title)

	r = byID["gfx1100-llamacpp-2026-10-24"]
	assert.Equal(t, []string{"b", "xtx"}, r.nodes)
	assert.Equal(t, DecisionPass, r.decision, "bold code span")
	assert.Contains(t, r.title, "a|b", "escaped pipe stays in the cell")

	r = byID["required-canary-gfx1100-textgen"]
	assert.Equal(t, SupportSupported, r.support, "production is supported")
	assert.Equal(t, DecisionConditional, r.decision)
	assert.Empty(t, r.date)

	assert.Equal(t, DecisionPending, byID["kt-embed-2026-10-31"].decision)
	assert.Equal(t, DecisionFail, byID["gfx1100-diffusers-2026-10-01"].decision)

	// Duplicate ids get an ordinal; control-plane rows are class n/a.
	require.Contains(t, byID, "proxy-routing-fix-2026-05-14")
	require.Contains(t, byID, "proxy-routing-fix-2026-05-14#2")
	assert.Equal(t, SupportNA, byID["proxy-routing-fix-2026-05-14"].gpuClass)
	assert.Equal(t, DecisionSuperseded, byID["proxy-routing-fix-2026-05-14#2"].decision)
}

func TestValidationRows_RejectsRaggedRow(t *testing.T) {
	dir := t.TempDir()
	md := "| `artifact` | `gpu_class` | `backend` | `support_level` | `canary_command` | `promotion_decision` |\n" +
		"|---|---|---|---|---|---|\n" +
		"| `x` | `gfx906/x` | `vllm` | a | b | c | extra |\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "m.md"), []byte(md), 0o644))
	_, err := loadValidationRows(dir, "m.md")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "m.md:3")
}

func TestCertificates(t *testing.T) {
	m := renderFixture(t)

	c := entry(t, m, KindCertificate, "gfx1100/llamacpp/draft-mtp")
	assert.Equal(t, SupportCertified, c.SupportLevel)
	assert.True(t, c.Evidenced)
	assert.Equal(t, "registry.example/llamacpp@sha256:aaaa", c.Image)
	assert.Equal(t, "2026-10-24", date(c.Date))
	assert.Equal(t, ".loom/60-validation-matrix.md#gfx1100-llamacpp-2026-10-24", c.Evidence)

	c = entry(t, m, KindCertificate, "gfx1100/vllm/qwen35-moe-mtp1")
	assert.Equal(t, SupportCertified, c.SupportLevel)

	c = entry(t, m, KindCertificate, "gfx1100/vllm-omni/audio")
	assert.Equal(t, SupportIncomplete, c.SupportLevel, "no image key: a partial triple is uncertified")
	assert.False(t, c.Evidenced)

	var certs []string
	for _, e := range m.Entries {
		if e.Kind == KindCertificate {
			certs = append(certs, e.ID)
		}
	}
	assert.Len(t, certs, 3, "-artifact / -max-model-len are attributes, not certificates: %v", certs)
}

func TestBackendPosture(t *testing.T) {
	m := renderFixture(t)

	b := entry(t, m, KindBackend, "gfx1100/vllm")
	assert.Equal(t, SupportSupported, b.SupportLevel, "full is supported")
	assert.True(t, b.Evidenced)
	assert.Equal(t, "matrix.md#gfx1100-vllm-2026-10-27", b.Evidence, "the newest verdict row, not the older conditional")
	assert.Equal(t, "2026-10-27", date(b.Date))

	b = entry(t, m, KindBackend, "gfx1100/diffusers")
	assert.True(t, b.Evidenced, "a fail is a verdict")
	assert.Equal(t, DecisionFail, b.Decision)

	b = entry(t, m, KindBackend, "gfx1100/vllm-omni")
	assert.Equal(t, SupportExperimental, b.SupportLevel)
	assert.False(t, b.Evidenced)
	assert.Equal(t, "gpuprofiles/gfx1100.yaml", b.Evidence)
	assert.Nil(t, b.Date)

	assert.Equal(t, SupportUnsupported, entry(t, m, KindBackend, "sm_52/vllm").SupportLevel)
}

func TestLanes(t *testing.T) {
	m := renderFixture(t)

	l := entry(t, m, KindLane, "chat-lane")
	assert.Equal(t, "gfx1100", l.GPUClass, "node-b maps through runtime.profiles")
	assert.Equal(t, ModalityChat, l.Modality)
	assert.Equal(t, SupportSupported, l.SupportLevel, "promotion evidence and no row")
	assert.True(t, l.Evidenced)
	assert.Equal(t, "matrix.md#gfx1100-llamacpp-2026-10-24", l.Evidence, "free text naming a matrix row points at it")
	assert.Contains(t, l.EvidenceNote, "6/6 gauntlet")
	assert.Equal(t, "2026-10-20", date(l.Date))
	assert.Empty(t, l.ValidationRows, "chat-lane-long and chat-lane-longer are other lanes")

	l = entry(t, m, KindLane, "chat-lane-long")
	assert.Equal(t, []string{"gfx1100-vllm-2026-10-27"}, l.ValidationRows, "chat-lane-longer is not chat-lane-long")
	assert.Equal(t, SupportSupported, l.SupportLevel)
	assert.Equal(t, DecisionPromote, l.Decision)

	l = entry(t, m, KindLane, "embed-lane")
	assert.Equal(t, ModalityEmbeddings, l.Modality)
	assert.Equal(t, []string{"kt-embed-2026-10-31", "gfx1100-vllm-2026-10-27"}, l.ValidationRows, "named in a canary_command")
	assert.Equal(t, "matrix.md#gfx1100-vllm-2026-10-27", l.Evidence, "the pending plan row does not displace the verdict")
	assert.True(t, l.Evidenced)

	l = entry(t, m, KindLane, "rerank-cpu")
	assert.Equal(t, classCPU, l.GPUClass)
	assert.Equal(t, ModalityRerank, l.Modality)
	assert.Equal(t, SupportUnverified, l.SupportLevel)
	assert.False(t, l.Evidenced)
	assert.Empty(t, l.Evidence)

	l = entry(t, m, KindLane, "video-canary")
	assert.True(t, l.Canary)
	assert.Equal(t, ModalityVideo, l.Modality)
	assert.Equal(t, DecisionFail, l.Decision)

	l = entry(t, m, KindLane, "image-980ti")
	assert.Equal(t, "sm_52", l.GPUClass, "no runtime profile: the only nvidia GPUProfile")
	assert.Equal(t, ModalityImage, l.Modality)

	for _, e := range m.Entries {
		assert.NotEqual(t, "not-a-lane", e.ID)
	}
}

func TestModalities(t *testing.T) {
	m := renderFixture(t)

	assert.Equal(t, SupportSupported, modality(t, m, ModalityChat).Level)
	assert.Equal(t, []string{"gfx1100"}, modality(t, m, ModalityChat).GPUClasses)
	assert.Equal(t, SupportSupported, modality(t, m, ModalityEmbeddings).Level)

	rr := modality(t, m, ModalityRerank)
	assert.Equal(t, SupportUnverified, rr.Level, "a CPU lane never makes a GPU claim")
	assert.Equal(t, 1, rr.Lanes)

	v := modality(t, m, ModalityVideo)
	assert.Equal(t, SupportUnverified, v.Level, "canary lanes and failed rows claim nothing")
	assert.Equal(t, 1, v.EvidencedLanes)

	for _, name := range advertisedModalities {
		modality(t, m, name) // every advertised modality renders, even with no lane
	}
	rag := modality(t, m, ModalityRAG)
	assert.Equal(t, SupportUnverified, rag.Level)
	assert.NotEmpty(t, rag.Note)
	assert.Nil(t, rag.LatestEvidence)
}

func TestGPUClasses(t *testing.T) {
	m := renderFixture(t)
	require.Len(t, m.GPUClasses, 2)
	assert.Equal(t, GPUClass{
		Class: "gfx1100", Vendor: "amd", Product: "AMD Radeon RX 7900 XTX", VRAMMB: 24576,
		Profile: "gfx1100", Nodes: []string{"node-b", "node-xtx"}, Source: "gpuprofiles/gfx1100.yaml",
	}, m.GPUClasses[0])
	assert.Equal(t, []string{"node-980ti"}, m.GPUClasses[1].Nodes, "nodes of lanes resolved by vendor")
}

func TestRenderIsDeterministic(t *testing.T) {
	a, err := renderFixture(t).Marshal()
	require.NoError(t, err)
	b, err := renderFixture(t).Marshal()
	require.NoError(t, err)
	assert.Equal(t, string(a), string(b))
	assert.True(t, strings.HasSuffix(string(a), "}\n"))

	var doc map[string]any
	require.NoError(t, json.Unmarshal(a, &doc))
	assert.Equal(t, Schema, doc["schema"])
	for _, raw := range doc["entries"].([]any) {
		e := raw.(map[string]any)
		for _, key := range []string{"support_level", "backend", "gpu_class", "evidence", "date"} {
			_, ok := e[key]
			assert.True(t, ok, "entry %v lacks %q", e["id"], key)
		}
	}
}

func TestCheck(t *testing.T) {
	out, err := renderFixture(t).Marshal()
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "matrix.json")
	require.NoError(t, os.WriteFile(path, out, 0o644))
	require.NoError(t, Check(out, path))

	require.NoError(t, os.WriteFile(path, append(out, '\n'), 0o644))
	err = Check(out, path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "make certify-matrix")
}

func TestLaneModality(t *testing.T) {
	vision := true
	cases := []struct {
		name string
		m    aiv1alpha2.Model
		want string
	}{
		{"snac backend", model("snac-decode-gfx1100", "snac", "HF://hubertsiuzdak/snac_24khz", nil), ModalityTTS},
		{"orpheus on llama.cpp", model("orpheus-3b-radeonvii", "llamacpp", "HF://x/orpheus-3b-GGUF", nil), ModalityTTS},
		{"whisper on vllm", model("whisper-large-v3-turbo", "vllm", "HF://openai/whisper-large-v3-turbo", nil), ModalityASR},
		{"pyannote diarization", model("pyannote-diarization-radeonvii", "pyannote", "HF://pyannote/speaker-diarization-3.1", nil), ModalityASR},
		{"stable audio", model("stable-audio-open-gfx1100", "diffusers", "HF://stabilityai/stable-audio-open-1.0", nil), ModalityAudio},
		{"wan video", model("wan21-vace-1p3b-i2v-gfx1100", "diffusers", "HF://Wan-AI/Wan2.1-VACE-1.3B", nil), ModalityVideo},
		{"sdxl image", model("sdxl-inpainting", "diffusers", "HF://x/sdxl-inpainting", nil), ModalityImage},
		{"reranker flag", model("bge-reranker-radeonvii", "llamacpp", "HF://gpustack/bge-reranker-v2-m3-GGUF", map[string]any{"reranking": true}), ModalityRerank},
		{"cross encoder", model("openjev-0p8b-crossencoder-gfx1100", "vllm", "HF://x/openjev", nil), ModalityRerank},
		{"embedding flag", model("gte-qwen2-1p5b-radeonvii", "llamacpp", "HF://second-state/gte-Qwen2-1.5B-instruct-GGUF", map[string]any{"embedding": true}), ModalityEmbeddings},
		{"vllm in the name is not vl", model("qwen3-1p7b-vllm-radeonvii", "vllm", "HF://Qwen/Qwen3-1.7B", nil), ModalityChat},
		{"vl model", model("qwen3-vl-radeonvii", "llamacpp", "HF://Qwen/Qwen3-VL-8B", nil), ModalityVision},
		{"chat", model("qwen38-27b-autoround-workhorse", "vllm", "HF://biMEMO/Qwen3.8-27B-int4-AutoRound", nil), ModalityChat},
	}
	vm := model("gemma4-e4b-multimodal", "vllm", "HF://google/gemma-4-e4b", nil)
	vm.Spec.Capabilities = &aiv1alpha2.ModelCapabilities{Vision: &vision}
	cases = append(cases, struct {
		name string
		m    aiv1alpha2.Model
		want string
	}{"vision capability", vm, ModalityVision})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, laneModality(&tc.m))
		})
	}
}

func model(name, backend, source string, cfg map[string]any) aiv1alpha2.Model {
	m := aiv1alpha2.Model{}
	m.Name = name
	m.Spec.Backend = backend
	m.Spec.Source = source
	if cfg != nil {
		raw, _ := json.Marshal(cfg)
		m.Spec.Config = &apiextensionsv1.JSON{Raw: raw}
	}
	return m
}

// TestRenderRepository renders the real inputs: a malformed validation-matrix
// row or manifest fails here before it reaches CI's staleness check.
func TestRenderRepository(t *testing.T) {
	opts := DefaultOptions()
	opts.Root = filepath.Join("..", "..")
	m, err := Render(opts)
	require.NoError(t, err)

	classes := map[string]bool{}
	for _, c := range m.GPUClasses {
		classes[c.Class] = true
	}
	assert.True(t, classes["gfx1100"] && classes["gfx906"], "both ROCm classes render: %v", classes)

	certified := 0
	for _, e := range m.Entries {
		if e.Kind == KindCertificate && e.SupportLevel == SupportCertified {
			certified++
		}
		assert.NotEmpty(t, e.SupportLevel, e.ID)
	}
	assert.Positive(t, certified)
}
