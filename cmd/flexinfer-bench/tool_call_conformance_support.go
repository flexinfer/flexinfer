package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// JSON Schema subset for the tool_call_conformance gate.
//
// The gate validates tool arguments against the schemas it sends, so it only
// needs the keywords those schemas use. tccSupportedSchemaKeywords is that
// set; TestTCCToolSchemasUseSupportedKeywords fails if a tool schema grows a
// keyword the validator would silently ignore.
var tccSupportedSchemaKeywords = map[string]bool{
	"type": true, "description": true, "properties": true, "required": true,
	"additionalProperties": true, "enum": true, "const": true,
	"minimum": true, "maximum": true, "minLength": true, "maxLength": true,
	"pattern": true, "items": true, "minItems": true, "maxItems": true,
}

// decodeJSONNumber decodes raw with json.Number for numbers, so integer
// checks see the literal the model emitted.
func decodeJSONNumber(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing data after JSON value")
	}
	return v, nil
}

// validateTCCSchema reports the first violation of schema by v, naming the
// JSON path, or nil.
func validateTCCSchema(schema map[string]any, v any, path string) error {
	if t, ok := schema["type"].(string); ok && !tccTypeMatches(t, v) {
		return fmt.Errorf("%s: want %s, got %s", path, t, tccTypeName(v))
	}
	if c, ok := schema["const"]; ok && !tccJSONEqual(c, v) {
		return fmt.Errorf("%s: want const %v, got %v", path, c, v)
	}
	if enum, ok := schema["enum"].([]any); ok {
		match := false
		for _, e := range enum {
			if tccJSONEqual(e, v) {
				match = true
				break
			}
		}
		if !match {
			return fmt.Errorf("%s: %v is not one of %v", path, v, enum)
		}
	}
	switch val := v.(type) {
	case map[string]any:
		props, _ := schema["properties"].(map[string]any)
		if req, ok := schema["required"].([]any); ok {
			for _, r := range req {
				name, _ := r.(string)
				if _, present := val[name]; !present {
					return fmt.Errorf("%s: missing required %q", path, name)
				}
			}
		}
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			sub, declared := props[k].(map[string]any)
			if !declared {
				if ap, ok := schema["additionalProperties"].(bool); ok && !ap {
					return fmt.Errorf("%s: unexpected property %q", path, k)
				}
				continue
			}
			if err := validateTCCSchema(sub, val[k], path+"."+k); err != nil {
				return err
			}
		}
	case []any:
		if n, ok := tccSchemaInt(schema, "minItems"); ok && len(val) < n {
			return fmt.Errorf("%s: %d items, want at least %d", path, len(val), n)
		}
		if n, ok := tccSchemaInt(schema, "maxItems"); ok && len(val) > n {
			return fmt.Errorf("%s: %d items, want at most %d", path, len(val), n)
		}
		if items, ok := schema["items"].(map[string]any); ok {
			for i, item := range val {
				if err := validateTCCSchema(items, item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	case string:
		n := utf8.RuneCountInString(val)
		if min, ok := tccSchemaInt(schema, "minLength"); ok && n < min {
			return fmt.Errorf("%s: length %d, want at least %d", path, n, min)
		}
		if max, ok := tccSchemaInt(schema, "maxLength"); ok && n > max {
			return fmt.Errorf("%s: length %d, want at most %d", path, n, max)
		}
		if pattern, ok := schema["pattern"].(string); ok {
			re, err := regexp.Compile(pattern)
			if err != nil {
				return fmt.Errorf("%s: schema pattern %q does not compile: %v", path, pattern, err)
			}
			if !re.MatchString(val) {
				return fmt.Errorf("%s: %q does not match %s", path, val, pattern)
			}
		}
	case json.Number:
		f, err := val.Float64()
		if err != nil {
			return fmt.Errorf("%s: number %q: %v", path, val, err)
		}
		if min, ok := tccSchemaFloat(schema, "minimum"); ok && f < min {
			return fmt.Errorf("%s: %v below minimum %v", path, val, min)
		}
		if max, ok := tccSchemaFloat(schema, "maximum"); ok && f > max {
			return fmt.Errorf("%s: %v above maximum %v", path, val, max)
		}
	}
	return nil
}

func tccTypeMatches(t string, v any) bool {
	switch t {
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "null":
		return v == nil
	case "number":
		_, ok := v.(json.Number)
		return ok
	case "integer":
		n, ok := v.(json.Number)
		if !ok {
			return false
		}
		if _, err := n.Int64(); err == nil {
			return true
		}
		f, err := n.Float64()
		return err == nil && f == math.Trunc(f) && !strings.ContainsAny(n.String(), ".eE")
	}
	return false
}

func tccTypeName(v any) string {
	switch v.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case bool:
		return "boolean"
	case json.Number:
		return "number"
	case nil:
		return "null"
	}
	return fmt.Sprintf("%T", v)
}

// tccJSONEqual compares decoded JSON values; numbers compare by value.
func tccJSONEqual(a, b any) bool {
	an, aNum := tccNumber(a)
	bn, bNum := tccNumber(b)
	if aNum || bNum {
		return aNum && bNum && an == bn
	}
	switch av := a.(type) {
	case string, bool, nil:
		return a == b
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !tccJSONEqual(av[i], bv[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, x := range av {
			if y, ok := bv[k]; !ok || !tccJSONEqual(x, y) {
				return false
			}
		}
		return true
	}
	return false
}

func tccNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case float64:
		return n, true
	case int:
		return float64(n), true
	}
	return 0, false
}

func tccSchemaFloat(schema map[string]any, key string) (float64, bool) {
	return tccNumber(schema[key])
}

func tccSchemaInt(schema map[string]any, key string) (int, bool) {
	f, ok := tccNumber(schema[key])
	return int(f), ok
}

// checkTCCSchemaKeywords reports a keyword the validator does not implement.
func checkTCCSchemaKeywords(schema map[string]any, path string) error {
	for k, v := range schema {
		if !tccSupportedSchemaKeywords[k] {
			return fmt.Errorf("%s: unsupported schema keyword %q", path, k)
		}
		switch k {
		case "properties":
			props, _ := v.(map[string]any)
			for name, sub := range props {
				subSchema, ok := sub.(map[string]any)
				if !ok {
					return fmt.Errorf("%s.properties.%s: not a schema object", path, name)
				}
				if err := checkTCCSchemaKeywords(subSchema, path+".properties."+name); err != nil {
					return err
				}
			}
		case "items":
			items, ok := v.(map[string]any)
			if !ok {
				return fmt.Errorf("%s.items: not a schema object", path)
			}
			if err := checkTCCSchemaKeywords(items, path+".items"); err != nil {
				return err
			}
		}
	}
	return nil
}

// vLLM engine metrics.
//
// The gate reads the lane's own Prometheus counters around each arm, as the
// S3 and KT-QOS harnesses do (scripts/verify-vllm-mtp.py,
// scripts/kt-qos-priority.py): decode throughput is generated tokens over
// decode time, position-0 acceptance is accepted tokens at position 0 over
// drafts. Counters are summed over every label set (engine, model_name,
// finished_reason) and "_total" is dropped, so 0.32 names and older ones
// read the same.

const (
	vllmMetricGenerationTokens = "vllm:generation_tokens"
	vllmMetricRequestSuccess   = "vllm:request_success"
	vllmMetricDrafts           = "vllm:spec_decode_num_drafts"
	vllmMetricDraftTokens      = "vllm:spec_decode_num_draft_tokens"
	vllmMetricAccepted         = "vllm:spec_decode_num_accepted_tokens"
	vllmMetricAcceptedPerPos   = "vllm:spec_decode_num_accepted_tokens_per_pos"
	vllmMetricPreemptions      = "vllm:num_preemptions"
)

// vllmDecodeTimeSums are the histogram sums that measure decode time, in
// preference order: per-request decode time (first token to last), then the
// inter-token latency histogram under its 0.11+ and older names.
var vllmDecodeTimeSums = []string{
	"vllm:request_decode_time_seconds_sum",
	"vllm:inter_token_latency_seconds_sum",
	"vllm:time_per_output_token_seconds_sum",
}

type engineSnapshot struct {
	values map[string]float64
	perPos map[int]float64
}

var (
	promLineRE  = regexp.MustCompile(`^([A-Za-z_:][A-Za-z0-9_:]*)(\{[^}]*\})?\s+(\S+)`)
	promLabelRE = regexp.MustCompile(`(\w+)="([^"]*)"`)
)

func parseEngineMetrics(text string) engineSnapshot {
	snap := engineSnapshot{values: map[string]float64{}, perPos: map[int]float64{}}
	for _, line := range strings.Split(text, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m := promLineRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		value, err := strconv.ParseFloat(m[3], 64)
		if err != nil || math.IsNaN(value) {
			continue
		}
		name := strings.TrimSuffix(m[1], "_total")
		if name == vllmMetricAcceptedPerPos {
			pos := -1
			for _, lm := range promLabelRE.FindAllStringSubmatch(m[2], -1) {
				if lm[1] == "position" {
					if p, err := strconv.Atoi(lm[2]); err == nil {
						pos = p
					}
				}
			}
			snap.perPos[pos] += value
			continue
		}
		snap.values[name] += value
	}
	return snap
}

func scrapeEngineMetrics(ctx context.Context, client *http.Client, url string) (engineSnapshot, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return engineSnapshot{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return engineSnapshot{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return engineSnapshot{}, fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return engineSnapshot{}, err
	}
	snap := parseEngineMetrics(string(body))
	if _, ok := snap.values[vllmMetricGenerationTokens]; !ok {
		return snap, fmt.Errorf("GET %s: no %s counter (not a vLLM lane, or stats logging disabled)", url, vllmMetricGenerationTokens)
	}
	return snap, nil
}

// engineDelta is what one arm did to the lane's counters.
type engineDelta struct {
	GenerationTokens float64 `json:"generation_tokens"`
	Requests         float64 `json:"requests"`
	DecodeSeconds    float64 `json:"decode_seconds"`
	DecodeSource     string  `json:"decode_source,omitempty"`
	// DecodeTokensPerSecond is (generation tokens - requests) / decode
	// seconds: the first token of each request comes from prefill.
	DecodeTokensPerSecond *float64 `json:"decode_tokens_per_s"`
	Drafts                float64  `json:"spec_drafts"`
	DraftTokens           float64  `json:"spec_draft_tokens"`
	AcceptedTokens        float64  `json:"spec_accepted_tokens"`
	Pos0Acceptance        *float64 `json:"pos0_acceptance"`
	AcceptanceRate        *float64 `json:"acceptance_rate"`
	MeanAcceptanceLength  *float64 `json:"mean_acceptance_length"`
	Preemptions           float64  `json:"preemptions"`
}

func diffEngine(before, after engineSnapshot) engineDelta {
	d := func(name string) float64 { return after.values[name] - before.values[name] }
	out := engineDelta{
		GenerationTokens: d(vllmMetricGenerationTokens),
		Requests:         d(vllmMetricRequestSuccess),
		Drafts:           d(vllmMetricDrafts),
		DraftTokens:      d(vllmMetricDraftTokens),
		AcceptedTokens:   d(vllmMetricAccepted),
		Preemptions:      d(vllmMetricPreemptions),
	}
	for _, name := range vllmDecodeTimeSums {
		if _, ok := after.values[name]; ok {
			out.DecodeSeconds = d(name)
			out.DecodeSource = name
			break
		}
	}
	if out.DecodeSeconds > 0 {
		tokens := out.GenerationTokens - out.Requests
		if tokens <= 0 {
			tokens = out.GenerationTokens
		}
		out.DecodeTokensPerSecond = tccRound(tokens / out.DecodeSeconds)
	}
	if out.Drafts > 0 {
		pos0 := after.perPos[0] - before.perPos[0]
		out.Pos0Acceptance = tccRound(pos0 / out.Drafts)
		out.MeanAcceptanceLength = tccRound(1 + out.AcceptedTokens/out.Drafts)
	}
	if out.DraftTokens > 0 {
		out.AcceptanceRate = tccRound(out.AcceptedTokens / out.DraftTokens)
	}
	return out
}

func tccRound(v float64) *float64 {
	r := math.Round(v*10000) / 10000
	return &r
}
