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
	"os"
	"strings"

	"github.com/flexinfer/flexinfer/pkg/envutil"
	"k8s.io/apimachinery/pkg/api/resource"
)

const defaultSHMSizeLimitRaw = "8Gi"

func defaultSHMSizeLimit() resource.Quantity {
	raw := strings.TrimSpace(os.Getenv("DEFAULT_SHM_SIZE_LIMIT"))
	if raw == "" {
		raw = defaultSHMSizeLimitRaw
	}
	if parsed, err := resource.ParseQuantity(raw); err == nil {
		return parsed
	}
	return resource.MustParse(defaultSHMSizeLimitRaw)
}

// Thin wrappers for backward compatibility within the controllers package.
// These delegate to the shared envutil package.

func envStringOrDefault(name, fallback string) string {
	return envutil.StringOrDefault(name, fallback)
}

func envIntOrDefault(name string, fallback int) int {
	return envutil.IntOrDefault(name, fallback)
}

func envBoolOrDefault(name string, fallback bool) bool {
	return envutil.BoolOrDefault(name, fallback)
}

func parseOptionalQuantity(raw string) (*resource.Quantity, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false
	}
	q, err := resource.ParseQuantity(raw)
	if err != nil {
		return nil, false
	}
	return &q, true
}
