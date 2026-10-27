package backend

import (
	"regexp"
	"strconv"
	"strings"
)

// vLLM boot-log parsing shared by the controller (dedicated-Deployment pod
// logs) and the runtime (the backend output it streams), so both serving paths
// read load progress and the KV-cache profile the same way.

// Example: Loading safetensors checkpoint shards:  91% Completed | 31/34 [08:47<07:05, 141.75s/it]
var vllmShardRe = regexp.MustCompile(
	`Loading safetensors checkpoint shards:\s+\d+%\s+Completed\s+\|\s+(\d+)/(\d+)\s+\[([^\]]+)\]`,
)

var (
	vllmKVTokensRe      = regexp.MustCompile(`GPU KV cache size:\s*([0-9][0-9,]*) tokens`)
	vllmKVConcurrencyRe = regexp.MustCompile(`Maximum concurrency for [0-9][0-9,]* tokens per request:\s*([0-9]+(?:\.[0-9]+)?)x`)
)

// ParseVLLMShardProgress extracts (current, total, rate) from a safetensors
// shard progress line. rate is returned verbatim (e.g. "141.75s/it") so
// operators can spot stalls at a glance.
func ParseVLLMShardProgress(line string) (int, int, string, bool) {
	m := vllmShardRe.FindStringSubmatch(line)
	if len(m) != 4 {
		return 0, 0, "", false
	}
	cur, errCur := strconv.Atoi(m[1])
	total, errTotal := strconv.Atoi(m[2])
	if errCur != nil || errTotal != nil || cur <= 0 || total <= 0 {
		return 0, 0, "", false
	}
	// m[3] is the "elapsed<remaining, rate" segment. Extract the rate.
	rate := ""
	if idx := strings.LastIndex(m[3], ","); idx >= 0 {
		rate = strings.TrimSpace(m[3][idx+1:])
	}
	if rate == "" {
		rate = "unknown rate"
	}
	return cur, total, rate, true
}

// ParseVLLMKVPool returns the last profiled KV-cache size (tokens) and maximum
// concurrency in text. Both profile lines are required; a partial profile is
// not usable.
func ParseVLLMKVPool(text string) (int64, string, bool) {
	tokens := vllmKVTokensRe.FindAllStringSubmatch(text, -1)
	concurrency := vllmKVConcurrencyRe.FindAllStringSubmatch(text, -1)
	if len(tokens) == 0 || len(concurrency) == 0 {
		return 0, "", false
	}
	n, ok := parseVLLMKVTokens(tokens[len(tokens)-1][1])
	c := concurrency[len(concurrency)-1][1]
	if _, err := strconv.ParseFloat(c, 64); !ok || err != nil {
		return 0, "", false
	}
	return n, c, true
}

// ParseVLLMKVTokensLine returns the KV-cache size from a single
// "GPU KV cache size: N tokens" line.
func ParseVLLMKVTokensLine(line string) (int64, bool) {
	m := vllmKVTokensRe.FindStringSubmatch(line)
	if len(m) != 2 {
		return 0, false
	}
	return parseVLLMKVTokens(m[1])
}

// ParseVLLMKVConcurrencyLine returns the concurrency from a single
// "Maximum concurrency for N tokens per request: Xx" line.
func ParseVLLMKVConcurrencyLine(line string) (string, bool) {
	m := vllmKVConcurrencyRe.FindStringSubmatch(line)
	if len(m) != 2 {
		return "", false
	}
	if _, err := strconv.ParseFloat(m[1], 64); err != nil {
		return "", false
	}
	return m[1], true
}

func parseVLLMKVTokens(raw string) (int64, bool) {
	n, err := strconv.ParseInt(strings.ReplaceAll(raw, ",", ""), 10, 64)
	return n, err == nil
}
