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

func configStringValue(cfg map[string]interface{}, keys ...string) string {
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

func configStringListValue(cfg map[string]interface{}, key string) []string {
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
	case []interface{}:
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

	// Backends that load GGUF files: filter downloads to just the specified file
	// to avoid downloading all quantization variants from multi-GGUF repos.
	if backendName == backend.NameLlamaCpp || backendName == "llama.cpp" || backendName == backend.NameVLLM {
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
