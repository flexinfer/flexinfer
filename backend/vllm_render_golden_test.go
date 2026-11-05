package backend

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// vllmRenderGoldenEnv regenerates testdata/vllm-render-golden.json from the
// manifests under deploy/ instead of comparing.
const vllmRenderGoldenEnv = "FLEXINFER_UPDATE_VLLM_RENDER_GOLDEN"

const vllmRenderGoldenPath = "testdata/vllm-render-golden.json"

// vllmRenderManifestDirs hold the vLLM lanes whose command line and env the
// golden pins: every Model, and every ModelExperiment candidate.
var vllmRenderManifestDirs = []string{"../deploy/models", "../deploy/debug", "../deploy/experiments"}

// vllmRenderCase is one vLLM lane's render input (frozen from its manifest)
// and the Args and Env the backend produced for it.
type vllmRenderCase struct {
	Manifest string          `json:"manifest"`
	GPUArch  string          `json:"gpuArch"`
	Model    string          `json:"model"`
	Config   map[string]any  `json:"config,omitempty"`
	Args     []string        `json:"args"`
	Env      []corev1.EnvVar `json:"env"`
}

func (c *vllmRenderCase) spec() *ModelSpec {
	return &ModelSpec{Model: c.Model, Config: c.Config, GPUVendor: GPUVendorAMD, GPUArch: c.GPUArch}
}

// TestVLLMRenderGoldenUnchanged renders every vLLM lane frozen in the golden
// through VLLMBackend.Args and Env and compares the result. The inputs are
// frozen copies of spec.config, so the test pins the backend's render, not
// the manifests: later manifest edits do not touch it. A diff here changes a
// lane's command line or env on its next roll.
//
// The config is the manifest's own: GPUProfile defaults (enforceEager,
// kvCacheDtype) are not applied, because they only fill keys that the
// manifest left unset and render through the same code. Every lane renders
// for AMD; a manifest named for the Radeon VII renders for gfx906, the rest
// for gfx1100.
//
// Regenerate only for an intentional render change, and say so in the MR:
//
//	FLEXINFER_UPDATE_VLLM_RENDER_GOLDEN=1 go test ./backend -run TestVLLMRenderGoldenUnchanged
func TestVLLMRenderGoldenUnchanged(t *testing.T) {
	b := &VLLMBackend{}

	if os.Getenv(vllmRenderGoldenEnv) == "1" {
		cases, err := loadVLLMRenderCases(vllmRenderManifestDirs)
		if err != nil {
			t.Fatalf("load manifests: %v", err)
		}
		for i := range cases {
			cases[i].Args = b.Args(cases[i].spec())
			cases[i].Env = b.Env(cases[i].spec())
		}
		raw, err := json.MarshalIndent(cases, "", "  ")
		if err != nil {
			t.Fatalf("marshal golden: %v", err)
		}
		if err := os.WriteFile(vllmRenderGoldenPath, append(raw, '\n'), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("wrote %d vLLM lanes to %s", len(cases), vllmRenderGoldenPath)
		return
	}

	raw, err := os.ReadFile(vllmRenderGoldenPath)
	if err != nil {
		t.Fatalf("read golden (regenerate with %s=1): %v", vllmRenderGoldenEnv, err)
	}
	var cases []vllmRenderCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("decode golden: %v", err)
	}
	if len(cases) == 0 {
		t.Fatalf("golden %s holds no lanes", vllmRenderGoldenPath)
	}
	for _, c := range cases {
		t.Run(c.Manifest, func(t *testing.T) {
			if got := b.Args(c.spec()); !reflect.DeepEqual(got, c.Args) {
				t.Errorf("args changed: a roll would restart this lane with a new command line.\n got: %q\nwant: %q", got, c.Args)
			}
			if got := b.Env(c.spec()); !reflect.DeepEqual(got, c.Env) {
				t.Errorf("env changed: a roll would restart this lane with a new env.\n got: %v\nwant: %v", got, c.Env)
			}
		})
	}
}

// loadVLLMRenderCases reads every YAML document under dirs and returns one
// case per vLLM Model or ModelExperiment candidate, keyed "<path>#<name>".
func loadVLLMRenderCases(dirs []string) ([]vllmRenderCase, error) {
	var cases []vllmRenderCase
	for _, dir := range dirs {
		paths, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
		if err != nil {
			return nil, err
		}
		sort.Strings(paths)
		for _, path := range paths {
			docs, err := readYAMLDocuments(path)
			if err != nil {
				return nil, err
			}
			for _, doc := range docs {
				var head struct {
					Kind     string `json:"kind"`
					Metadata struct {
						Name string `json:"name"`
					} `json:"metadata"`
				}
				if err := yaml.Unmarshal(doc, &head); err != nil {
					return nil, err
				}
				var spec aiv1alpha2.ModelSpec
				switch head.Kind {
				case "Model":
					var m aiv1alpha2.Model
					if err := yaml.Unmarshal(doc, &m); err != nil {
						return nil, err
					}
					spec = m.Spec
				case "ModelExperiment":
					var e aiv1alpha2.ModelExperiment
					if err := yaml.Unmarshal(doc, &e); err != nil {
						return nil, err
					}
					spec = e.Spec.Candidate
				default:
					continue
				}
				if spec.Backend != NameVLLM {
					continue
				}
				arch := "gfx1100"
				if strings.Contains(filepath.Base(path), "radeonvii") {
					arch = "gfx906"
				}
				cases = append(cases, vllmRenderCase{
					Manifest: strings.TrimPrefix(filepath.ToSlash(path), "../") + "#" + head.Metadata.Name,
					GPUArch:  arch,
					Model:    spec.Source,
					Config:   spec.GetConfigMap(),
				})
			}
		}
	}
	return cases, nil
}

func readYAMLDocuments(path string) ([][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	reader := utilyaml.NewYAMLReader(bufio.NewReader(f))
	var docs [][]byte
	for {
		doc, err := reader.Read()
		if errors.Is(err, io.EOF) {
			return docs, nil
		}
		if err != nil {
			return nil, err
		}
		if len(bytes.TrimSpace(doc)) > 0 {
			docs = append(docs, doc)
		}
	}
}
