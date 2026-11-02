package certify

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// gpuProducts names the consumer card behind each GPUProfile architecture in
// this fleet. The profiles carry no product field; this is the one fleet fact
// the renderer holds, and an unknown architecture renders without a product.
var gpuProducts = map[string]string{
	"gfx1100": "AMD Radeon RX 7900 XTX",
	"gfx906":  "AMD Radeon VII",
	"sm_52":   "NVIDIA GeForce GTX 980 Ti",
}

type profile struct {
	obj    *aiv1alpha2.GPUProfile
	class  string
	source string
}

// loadProfiles reads every GPUProfile document matched by pattern.
func loadProfiles(root, pattern string) ([]profile, error) {
	files, err := filepath.Glob(filepath.Join(root, pattern))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var out []profile
	for _, f := range files {
		docs, err := readDocs(f)
		if err != nil {
			return nil, err
		}
		for _, doc := range docs {
			if kindOf(doc) != "GPUProfile" {
				continue
			}
			p := &aiv1alpha2.GPUProfile{}
			if err := yaml.Unmarshal(doc, p); err != nil {
				return nil, fmt.Errorf("%s: %w", f, err)
			}
			out = append(out, profile{obj: p, class: strings.TrimSpace(p.Spec.Architecture), source: relSlash(root, f)})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no GPUProfile in %s", pattern)
	}
	return out, nil
}

func (p profile) vendor() string { return strings.ToLower(strings.TrimSpace(p.obj.Spec.Vendor)) }

// backendEntries is the profile's declared posture per backend, each pointed
// at the newest validation row for the same GPU class and backend.
func (p profile) backendEntries(rows []validationRow) []Entry {
	names := make([]string, 0, len(p.obj.Spec.Backends))
	for name := range p.obj.Spec.Backends {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []Entry
	for _, name := range names {
		e := Entry{
			ID:           p.class + "/" + name,
			Kind:         KindBackend,
			SupportLevel: declaredSupport(p.obj.Spec.Backends[name].Support),
			Backend:      name,
			GPUClass:     p.class,
			Evidence:     p.source,
			Source:       p.source,
		}
		if r, ok := evidenceRow(rows, func(r validationRow) bool { return r.gpuClass == p.class && r.backend == name }); ok {
			e.Evidenced = r.verdict()
			e.Evidence = r.anchor()
			e.Date = strPtr(r.date)
			e.Decision = r.decision
			e.ValidationRows = []string{r.id}
		}
		out = append(out, e)
	}
	return out
}

func declaredSupport(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "full", "supported":
		return SupportSupported
	case "experimental":
		return SupportExperimental
	case "unsupported":
		return SupportUnsupported
	case "deprecated":
		return SupportDeprecated
	default:
		return SupportNA
	}
}

// Attribute keys that hang off a certificate key besides -since/-evidence
// (gfx1100/gfx906 vLLM MTP certificates record the artifact digest, context
// and memory envelope they were proven at).
var certificateSuffixes = []string{"-since", "-evidence", "-artifact", "-max-model-len", "-gpu-util"}

// certificateEntries lists the backend-capability certificates. A certificate
// key holds a digest-pinned image; a key that only appears through its -since
// or -evidence sibling is a partial triple and renders incomplete.
func (p profile) certificateEntries() []Entry {
	ann := p.obj.GetAnnotations()
	bases := map[string]bool{}
	for k, v := range ann {
		rest, ok := strings.CutPrefix(k, aiv1alpha2.BackendCertificateAnnotationPrefix)
		if !ok || rest == "" {
			continue
		}
		if looksLikeImage(v) {
			bases[rest] = true
			continue
		}
		for _, suffix := range []string{"-since", "-evidence"} {
			if base, ok := strings.CutSuffix(rest, suffix); ok && base != "" {
				bases[base] = true
			}
		}
	}
	// A base that is itself an attribute of a longer certificate key is not a
	// certificate (vllm-qwen35-moe-mtp1-artifact belongs to vllm-qwen35-moe-mtp1).
	for base := range bases {
		for _, suffix := range certificateSuffixes {
			if parent, ok := strings.CutSuffix(base, suffix); ok && bases[parent] {
				delete(bases, base)
			}
		}
	}
	keys := make([]string, 0, len(bases))
	for b := range bases {
		keys = append(keys, b)
	}
	sort.Strings(keys)

	var out []Entry
	for _, key := range keys {
		backend, capability := p.splitCertificateKey(key)
		image, since, evidence := aiv1alpha2.GetBackendCapabilityCertificate(p.obj, backend, capability)
		e := Entry{
			ID:           p.class + "/" + backend + "/" + capability,
			Kind:         KindCertificate,
			SupportLevel: SupportIncomplete,
			Backend:      backend,
			GPUClass:     p.class,
			Capability:   capability,
			Image:        image,
			Evidence:     evidence,
			Source:       p.source,
		}
		if !since.IsZero() {
			e.Date = strPtr(since.UTC().Format("2006-01-02"))
		}
		if image != "" && !since.IsZero() && evidence != "" {
			e.SupportLevel = SupportCertified
			e.Evidenced = true
		}
		out = append(out, e)
	}
	return out
}

// splitCertificateKey splits "<backend>-<capability>" on the longest backend
// name the profile declares, so vllm-omni-x parses as (vllm-omni, x) and
// vllm-qwen35-moe-mtp1 as (vllm, qwen35-moe-mtp1).
func (p profile) splitCertificateKey(key string) (backend, capability string) {
	names := make([]string, 0, len(p.obj.Spec.Backends))
	for name := range p.obj.Spec.Backends {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })
	for _, name := range names {
		if rest, ok := strings.CutPrefix(key, name+"-"); ok && rest != "" {
			return name, rest
		}
	}
	backend, capability, _ = strings.Cut(key, "-")
	return backend, capability
}

func looksLikeImage(v string) bool {
	v = strings.TrimSpace(v)
	return strings.Contains(v, "/") && (strings.Contains(v, "@sha256:") || strings.Contains(v, ":"))
}

// evidenceRow picks the row that stands as evidence among rows matching keep:
// the newest row carrying a verdict (promote, pass, conditional, fail, block,
// parked), else the newest row at all. A pending kill-test does not displace
// the verdict it may later replace. Rows are in file order, newest first, so
// ties keep the earlier row.
func evidenceRow(rows []validationRow, keep func(validationRow) bool) (validationRow, bool) {
	if r, ok := newestRow(rows, func(r validationRow) bool { return keep(r) && r.verdict() }); ok {
		return r, true
	}
	return newestRow(rows, keep)
}

func newestRow(rows []validationRow, keep func(validationRow) bool) (validationRow, bool) {
	var best validationRow
	found := false
	for _, r := range rows {
		if !keep(r) {
			continue
		}
		if !found || r.date > best.date {
			best, found = r, true
		}
	}
	return best, found
}

// gpuClasses lists one class per GPUProfile with the nodes that carry it:
// the chart's runtime profiles, plus the nodes of lanes resolved to it.
func gpuClasses(profiles []profile, nodeClasses map[string]string, lanes []Entry) []GPUClass {
	var out []GPUClass
	for _, p := range profiles {
		var nodes []string
		for node, class := range nodeClasses {
			if class == p.class {
				nodes = append(nodes, node)
			}
		}
		for _, l := range lanes {
			if l.GPUClass == p.class {
				nodes = append(nodes, l.Nodes...)
			}
		}
		out = append(out, GPUClass{
			Class:   p.class,
			Vendor:  p.vendor(),
			Product: gpuProducts[p.class],
			VRAMMB:  p.obj.Spec.VRAMMB,
			Profile: p.obj.Name,
			Nodes:   sortedUnique(nodes),
			Source:  p.source,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Class < out[j].Class })
	return out
}

// loadNodeClasses maps node names to GPU architectures from the chart values'
// runtime.profiles[] (gpuArch + the kubernetes.io/hostname node selector).
func loadNodeClasses(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var values struct {
		Runtime struct {
			Profiles []struct {
				GPUArch      string            `json:"gpuArch"`
				NodeSelector map[string]string `json:"nodeSelector"`
			} `json:"profiles"`
		} `json:"runtime"`
	}
	if err := yaml.Unmarshal(data, &values); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	out := map[string]string{}
	for _, p := range values.Runtime.Profiles {
		if node := p.NodeSelector["kubernetes.io/hostname"]; node != "" && p.GPUArch != "" {
			out[node] = p.GPUArch
		}
	}
	return out, nil
}

// readDocs splits a multi-document YAML file.
func readDocs(path string) ([][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	r := utilyaml.NewYAMLReader(bufio.NewReader(f))
	var docs [][]byte
	for {
		doc, err := r.Read()
		if errors.Is(err, io.EOF) {
			return docs, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if len(bytes.TrimSpace(doc)) > 0 {
			docs = append(docs, doc)
		}
	}
}

func kindOf(doc []byte) string {
	var meta struct {
		Kind string `json:"kind"`
	}
	if err := yaml.Unmarshal(doc, &meta); err != nil {
		return ""
	}
	return meta.Kind
}
