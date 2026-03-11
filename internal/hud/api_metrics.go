package hud

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// handleDaemonMetrics proxies and parses the daemon's Prometheus /metrics endpoint,
// returning structured JSON suitable for HUD display (latency percentiles, error
// rates, request counts per server).
func (a *App) handleDaemonMetrics(w http.ResponseWriter, r *http.Request) {
	if a.config.MetricsAddr == "" {
		a.writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "daemon metrics address not configured",
		})
		return
	}

	metricsURL := fmt.Sprintf("http://%s/metrics", a.config.MetricsAddr)
	client := &http.Client{Timeout: 5 * time.Second}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, metricsURL, nil)
	if err != nil {
		a.writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("failed to create metrics request: %v", err),
		})
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		a.writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": fmt.Sprintf("failed to fetch daemon metrics: %v", err),
		})
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		a.writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": fmt.Sprintf("failed to read daemon metrics: %v", err),
		})
		return
	}

	result := parseDaemonMetrics(string(body))
	a.writeJSON(w, http.StatusOK, result)
}

// metricsResponse is the structured output of parsed daemon metrics.
type metricsResponse struct {
	Servers []serverMetrics `json:"servers"`
}

type serverMetrics struct {
	Name         string  `json:"name"`
	RequestCount float64 `json:"request_count"`
	ErrorCount   float64 `json:"error_count"`
	ErrorRate    float64 `json:"error_rate"`
	P50Ms        float64 `json:"p50_ms"`
	P95Ms        float64 `json:"p95_ms"`
	P99Ms        float64 `json:"p99_ms"`
	InFlight     float64 `json:"in_flight"`
}

// histBucket represents a single histogram bucket boundary and cumulative count.
type histBucket struct {
	le    float64
	count float64
}

// parseDaemonMetrics extracts key metrics from Prometheus text format.
func parseDaemonMetrics(body string) metricsResponse {
	// Parse histogram buckets and counters per server.
	type serverData struct {
		requestCount float64
		errorCount   float64
		buckets      []histBucket
		histCount    float64
		inFlight     float64
	}

	servers := map[string]*serverData{}

	getOrCreate := func(name string) *serverData {
		if name == "" {
			return nil
		}
		if s, ok := servers[name]; ok {
			return s
		}
		s := &serverData{}
		servers[name] = s
		return s
	}

	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse: metric_name{labels} value
		metricName, labels, value := parsePrometheusLine(line)
		if metricName == "" {
			continue
		}

		serverName := labels["server"]

		switch metricName {
		case "loom_daemon_requests_total":
			sd := getOrCreate(serverName)
			if sd == nil {
				continue
			}
			sd.requestCount += value
			if labels["status"] == "error" {
				sd.errorCount += value
			}

		case "loom_daemon_request_duration_seconds_bucket":
			sd := getOrCreate(serverName)
			if sd == nil {
				continue
			}
			leStr := labels["le"]
			if leStr == "+Inf" {
				continue
			}
			le, err := strconv.ParseFloat(leStr, 64)
			if err != nil {
				continue
			}
			sd.buckets = append(sd.buckets, histBucket{le: le, count: value})

		case "loom_daemon_request_duration_seconds_count":
			sd := getOrCreate(serverName)
			if sd == nil {
				continue
			}
			sd.histCount += value

		case "loom_daemon_requests_in_flight":
			sd := getOrCreate(serverName)
			if sd == nil {
				continue
			}
			sd.inFlight = value
		}
	}

	// Build sorted output.
	var result []serverMetrics
	for name, sd := range servers {
		sm := serverMetrics{
			Name:         name,
			RequestCount: sd.requestCount,
			ErrorCount:   sd.errorCount,
			InFlight:     sd.inFlight,
		}
		if sd.requestCount > 0 {
			sm.ErrorRate = sd.errorCount / sd.requestCount
		}
		sm.P50Ms = histogramPercentile(sd.buckets, sd.histCount, 0.50) * 1000
		sm.P95Ms = histogramPercentile(sd.buckets, sd.histCount, 0.95) * 1000
		sm.P99Ms = histogramPercentile(sd.buckets, sd.histCount, 0.99) * 1000
		result = append(result, sm)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})

	return metricsResponse{Servers: result}
}

// parsePrometheusLine parses a single Prometheus exposition format line.
// Returns metric name, labels map, and value.
func parsePrometheusLine(line string) (string, map[string]string, float64) {
	labels := map[string]string{}

	// Split at value (last space-separated token).
	braceStart := strings.IndexByte(line, '{')
	braceEnd := strings.IndexByte(line, '}')

	var metricPart, valuePart string
	if braceStart >= 0 && braceEnd > braceStart {
		metricPart = line[:braceStart]
		labelStr := line[braceStart+1 : braceEnd]
		valuePart = strings.TrimSpace(line[braceEnd+1:])

		// Parse labels: key="value",key2="value2"
		for _, kv := range splitLabels(labelStr) {
			eqIdx := strings.IndexByte(kv, '=')
			if eqIdx < 0 {
				continue
			}
			k := kv[:eqIdx]
			v := strings.Trim(kv[eqIdx+1:], `"`)
			labels[k] = v
		}
	} else {
		parts := strings.Fields(line)
		if len(parts) < 2 {
			return "", nil, 0
		}
		metricPart = parts[0]
		valuePart = parts[1]
	}

	val, err := strconv.ParseFloat(valuePart, 64)
	if err != nil {
		return "", nil, 0
	}

	return metricPart, labels, val
}

// splitLabels splits label string on commas, respecting quotes.
func splitLabels(s string) []string {
	var result []string
	var current strings.Builder
	inQuote := false
	for _, c := range s {
		if c == '"' {
			inQuote = !inQuote
			current.WriteRune(c)
		} else if c == ',' && !inQuote {
			result = append(result, current.String())
			current.Reset()
		} else {
			current.WriteRune(c)
		}
	}
	if current.Len() > 0 {
		result = append(result, current.String())
	}
	return result
}

// histogramPercentile estimates a percentile from histogram buckets.
// Uses linear interpolation within the bucket containing the target count.
func histogramPercentile(buckets []histBucket, totalCount float64, percentile float64) float64 {
	if totalCount == 0 || len(buckets) == 0 {
		return 0
	}

	sort.Slice(buckets, func(i, j int) bool {
		return buckets[i].le < buckets[j].le
	})

	target := percentile * totalCount
	prevCount := 0.0
	prevBound := 0.0

	for _, b := range buckets {
		if b.count >= target {
			// Linear interpolation within this bucket.
			frac := 0.0
			bucketWidth := b.count - prevCount
			if bucketWidth > 0 {
				frac = (target - prevCount) / bucketWidth
			}
			return prevBound + frac*(b.le-prevBound)
		}
		prevCount = b.count
		prevBound = b.le
	}

	// Target exceeds all buckets — return last boundary.
	if len(buckets) > 0 {
		return buckets[len(buckets)-1].le
	}
	return math.NaN()
}
