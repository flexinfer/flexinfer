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
	"strconv"
	"strings"

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

func envStringOrDefault(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

func envIntOrDefault(name string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func envBoolOrDefault(name string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
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
