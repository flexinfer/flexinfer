/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
)

func isMlcModelSource(source string) bool {
	return strings.HasPrefix(source, "HF://mlc-ai/") || strings.Contains(source, "-MLC")
}

type hfDownloadOptions struct {
	allowPatterns  []string
	ignorePatterns []string
	revision       string
}

// hfCacheMarkerKey is the canonical form of the download options an HF cache
// marker vouches for. Patterns are sorted because their order does not change
// the file set snapshot_download selects. Changing what the key holds renames
// every HF cache marker, so each cache downloads again on its next Job.
type hfCacheMarkerKey struct {
	AllowPatterns  []string `json:"allowPatterns"`
	IgnorePatterns []string `json:"ignorePatterns"`
	Revision       string   `json:"revision"`
}

// hfCacheMarkerName names the completion marker an HF download Job writes in
// the model's cache directory: the Local stage on the node's hostPath and the
// SharedPVC prefetch on the cache PVC. It hashes spec.source together with the
// resolved download options, so a ggufFile rename, an allow or ignore pattern
// change, or an hfRevision bump names a marker the cache does not hold yet and
// the Job downloads again instead of trusting the old file set.
func hfCacheMarkerName(source string, opts hfDownloadOptions) (string, error) {
	key, err := json.Marshal(hfCacheMarkerKey{
		AllowPatterns:  slices.Sorted(slices.Values(opts.allowPatterns)),
		IgnorePatterns: slices.Sorted(slices.Values(opts.ignorePatterns)),
		Revision:       opts.revision,
	})
	if err != nil {
		return "", fmt.Errorf("marshal HF cache marker key: %w", err)
	}
	sum := sha256.Sum256([]byte(source + "\n" + string(key)))
	return ".flexinfer_cached_" + hex.EncodeToString(sum[:]), nil
}

func configStringValue(cfg map[string]any, keys ...string) string {
	for _, key := range keys {
		raw, ok := cfg[key]
		if !ok {
			continue
		}
		if s, ok := raw.(string); ok {
			s = strings.TrimSpace(s)
			if s != "" {
				return s
			}
		}
	}
	return ""
}

func configStringListValue(cfg map[string]any, key string) []string {
	raw, ok := cfg[key]
	if !ok || raw == nil {
		return nil
	}

	out := make([]string, 0)
	appendItem := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}

	switch v := raw.(type) {
	case string:
		if strings.Contains(v, ",") {
			for _, item := range strings.Split(v, ",") {
				appendItem(item)
			}
		} else {
			appendItem(v)
		}
	case []string:
		for _, item := range v {
			appendItem(item)
		}
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				appendItem(s)
			}
		}
	}

	return out
}

func sanitizeHFPatterns(patterns []string) []string {
	seen := make(map[string]struct{}, len(patterns))
	out := make([]string, 0, len(patterns))
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		p = strings.TrimLeft(p, "/")
		if p == "" || strings.Contains(p, "..") {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

func resolveHFDownloadOptions(model *aiv1alpha2.Model) hfDownloadOptions {
	cfg := model.Spec.GetConfigMap()
	opts := hfDownloadOptions{
		allowPatterns:  configStringListValue(cfg, "hfAllowPatterns"),
		ignorePatterns: configStringListValue(cfg, "hfIgnorePatterns"),
		revision:       configStringValue(cfg, "hfRevision"),
	}

	backendName := strings.ToLower(strings.TrimSpace(model.Spec.Backend))

	// Backends that load one weights file: filter downloads to just the specified
	// file to avoid downloading every quantization variant from multi-file repos
	// (GGUF repos, and ggerganov/whisper.cpp for whispercpp).
	if singleFileBackend(backendName) {
		ggufFile := configStringValue(cfg, "ggufFile", "modelFile")
		if ggufFile != "" {
			opts.allowPatterns = append(opts.allowPatterns, ggufFile)
		}

		// mmproj is optional for multimodal models and can live in the same repo.
		mmproj := configStringValue(cfg, "mmproj")
		if mmproj != "" && !strings.HasPrefix(mmproj, "/") {
			opts.allowPatterns = append(opts.allowPatterns, mmproj)
		}
	}

	opts.allowPatterns = sanitizeHFPatterns(opts.allowPatterns)
	opts.ignorePatterns = sanitizeHFPatterns(opts.ignorePatterns)
	return opts
}

// expectedHFCacheFiles returns the list of relative paths (under the
// model cache directory) that MUST exist and be non-empty after a
// successful HF prefetch. The cache-stage script uses this to refuse
// to write its success marker when snapshot_download returned without
// actually pulling the required files — the common silent-failure
// modes are (a) allow_patterns matched nothing, (b) the repo is gated
// and HF_TOKEN was missing for a specific file, and (c) a network
// hiccup that huggingface_hub absorbed.
//
// We currently only return non-empty for backends where a single
// concrete filename is in the spec (llamacpp/vllm/whispercpp + ggufFile
// or modelFile). For
// other backends the controller has no way to know up-front which
// specific files MUST be present, so we leave the check disabled and
// let the existing "size of downloaded tree" heuristics catch
// failures at activation time. Tightening this is future work.
func expectedHFCacheFiles(model *aiv1alpha2.Model) []string {
	cfg := model.Spec.GetConfigMap()
	backendName := strings.ToLower(strings.TrimSpace(model.Spec.Backend))
	if !singleFileBackend(backendName) {
		return nil
	}
	var out []string
	if ggufFile := configStringValue(cfg, "ggufFile", "modelFile"); ggufFile != "" {
		out = append(out, ggufFile)
	}
	if mmproj := configStringValue(cfg, "mmproj"); mmproj != "" && !strings.HasPrefix(mmproj, "/") {
		out = append(out, mmproj)
	}
	return out
}

// singleFileBackend reports backends whose HF download is filtered to the
// ggufFile/modelFile named in the spec, and whose cache stage requires it.
func singleFileBackend(backendName string) bool {
	switch backendName {
	case backend.NameLlamaCpp, "llama.cpp", backend.NameVLLM, backend.NameWhisperCpp:
		return true
	default:
		return false
	}
}
