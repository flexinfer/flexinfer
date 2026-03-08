package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/crb2nu/loom/pkg/mcperror"
)

func validateNonNegative(field string, n int) error {
	if n < 0 {
		return mcperror.InvalidParam(field, "must be >= 0")
	}
	return nil
}

func validateRange(field string, n, min, max int) error {
	if n < min || n > max {
		return mcperror.InvalidParam(field, fmt.Sprintf("must be between %d and %d", min, max))
	}
	return nil
}

func readStringSliceArg(v any) []string {
	var out []string
	switch values := v.(type) {
	case []string:
		for _, value := range values {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				out = append(out, trimmed)
			}
		}
	case []any:
		for _, value := range values {
			if s, ok := value.(string); ok {
				if trimmed := strings.TrimSpace(s); trimmed != "" {
					out = append(out, trimmed)
				}
			}
		}
	}
	return out
}

func paginateAnySlice(values []any, start, count int) []any {
	if len(values) == 0 {
		return []any{}
	}
	if start < 0 {
		start = 0
	}
	if start >= len(values) {
		return []any{}
	}
	if count <= 0 {
		return []any{}
	}
	end := start + count
	if end > len(values) {
		end = len(values)
	}
	return values[start:end]
}

func toAnySlice(v any) []any {
	switch items := v.(type) {
	case []any:
		return items
	default:
		return []any{}
	}
}

func attributedText(v any) string {
	switch typed := v.(type) {
	case string:
		return strings.TrimSpace(typed)
	case map[string]any:
		return strings.TrimSpace(stringValue(typed["text"]))
	default:
		return ""
	}
}

func stringValue(v any) string {
	switch typed := v.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

func stringSliceValue(v any) []string {
	items := toAnySlice(v)
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s := stringValue(item); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func int64Value(v any) int64 {
	switch typed := v.(type) {
	case int:
		return int64(typed)
	case int32:
		return int64(typed)
	case int64:
		return typed
	case float32:
		return int64(typed)
	case float64:
		return int64(typed)
	case json.Number:
		if n, err := typed.Int64(); err == nil {
			return n
		}
	case string:
		if n, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64); err == nil {
			return n
		}
	}
	return 0
}

func intValue(v any) int {
	return int(int64Value(v))
}

func boolValue(v any) bool {
	switch typed := v.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes", "on":
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstNonZero(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func nestedValue(root map[string]any, path ...string) any {
	if len(path) == 0 {
		return nil
	}
	var current any = root
	for _, segment := range path {
		node, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = node[segment]
	}
	return current
}

func nestedString(root map[string]any, path ...string) string {
	return stringValue(nestedValue(root, path...))
}

func nestedInt64(root map[string]any, path ...string) int64 {
	return int64Value(nestedValue(root, path...))
}
