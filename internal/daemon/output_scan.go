package daemon

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// OutputScanningMode controls how PII/secret scanning results are handled.
type OutputScanningMode string

const (
	// OutputScanOff disables output scanning (default).
	OutputScanOff OutputScanningMode = "off"
	// OutputScanWarn logs a warning but returns the response unmodified.
	OutputScanWarn OutputScanningMode = "warn"
	// OutputScanRedact replaces detected secrets/PII with redaction markers.
	OutputScanRedact OutputScanningMode = "redact"
)

// OutputScanningConfig controls PII/secret scanning of tool responses.
type OutputScanningConfig struct {
	// Mode controls behavior: "off", "warn", "redact". Default: "off".
	Mode OutputScanningMode `yaml:"mode,omitempty"`
}

// scanPattern defines a single PII/secret detection pattern.
type scanPattern struct {
	Name    string
	Pattern *regexp.Regexp
	Redact  string // replacement text
}

// defaultPatterns contains built-in patterns for common secrets and PII.
var defaultPatterns = []scanPattern{
	{Name: "aws_access_key", Pattern: regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), Redact: "[REDACTED:aws_key]"},
	{Name: "aws_secret_key", Pattern: regexp.MustCompile(`(?i)(?:aws_secret|secret_key|secret_access)\s*[:=]\s*["']?[A-Za-z0-9/+=]{40}["']?`), Redact: "[REDACTED:aws_secret]"},
	{Name: "generic_api_key", Pattern: regexp.MustCompile(`(?i)(?:api[_-]?key|apikey)\s*[:=]\s*["']?[A-Za-z0-9_\-]{20,}["']?`), Redact: "[REDACTED:api_key]"},
	{Name: "generic_secret", Pattern: regexp.MustCompile(`(?i)(?:secret|password|passwd|token)\s*[:=]\s*["']?[^\s"']{8,}["']?`), Redact: "[REDACTED:secret]"},
	{Name: "private_key", Pattern: regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH )?PRIVATE KEY-----`), Redact: "[REDACTED:private_key]"},
	{Name: "github_token", Pattern: regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9_]{36,}\b`), Redact: "[REDACTED:github_token]"},
	{Name: "slack_token", Pattern: regexp.MustCompile(`\bxox[bpors]-[A-Za-z0-9\-]{10,}\b`), Redact: "[REDACTED:slack_token]"},
	{Name: "jwt", Pattern: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`), Redact: "[REDACTED:jwt]"},
	{Name: "email", Pattern: regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`), Redact: "[REDACTED:email]"},
	{Name: "ip_address", Pattern: regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`), Redact: "[REDACTED:ip]"},
	{Name: "ssn", Pattern: regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`), Redact: "[REDACTED:ssn]"},
	{Name: "credit_card", Pattern: regexp.MustCompile(`\b(?:\d{4}[\s\-]?){3}\d{4}\b`), Redact: "[REDACTED:cc]"},
}

// scanFinding records a single detected pattern match.
type scanFinding struct {
	Pattern string `json:"pattern"`
	Count   int    `json:"count"`
}

// scanOutputForPII checks the tool response for PII/secret patterns.
// Returns nil to proceed with the original response, or a modified/error response.
func (p *callPipeline) scanOutputForPII(resp *mcp.Message) *mcp.Message {
	mode := p.daemon.fileCfg.OutputScanning.Mode
	if mode == "" || mode == OutputScanOff {
		return nil
	}

	p.stage = stageOutputScan
	span := p.startStageSpan("daemon.pipeline.output_scan")
	defer span.End()

	// Extract text content from response.
	text := extractResponseText(resp)
	if text == "" {
		span.SetAttributes(attribute.String("output_scan.result", "skip_empty"))
		return nil
	}

	// Scan for patterns.
	findings := scanText(text)

	span.SetAttributes(
		attribute.Int("output_scan.findings", len(findings)),
		attribute.String("output_scan.mode", string(mode)),
	)

	if len(findings) == 0 {
		span.SetAttributes(attribute.String("output_scan.result", "clean"))
		return nil
	}

	return p.handleScanFindings(span, mode, resp, text, findings)
}

// handleScanFindings processes scan results based on the configured mode.
func (p *callPipeline) handleScanFindings(span trace.Span, mode OutputScanningMode, resp *mcp.Message, text string, findings []scanFinding) *mcp.Message {
	detail := formatFindings(findings)

	switch mode {
	case OutputScanRedact:
		span.SetAttributes(attribute.String("output_scan.result", "redact"))
		slog.Warn("output scanning: redacting detected patterns",
			"server", p.serverName,
			"tool", p.toolName,
			"findings", detail,
		)
		return redactResponse(resp, text)

	case OutputScanWarn:
		span.SetAttributes(attribute.String("output_scan.result", "warn"))
		slog.Warn("output scanning: potential PII/secrets detected",
			"server", p.serverName,
			"tool", p.toolName,
			"findings", detail,
		)
		return nil // Allow unmodified response to proceed.

	default:
		return nil
	}
}

// extractResponseText extracts text content from an MCP response message.
func extractResponseText(resp *mcp.Message) string {
	if resp == nil || resp.Result == nil {
		return ""
	}

	// Try to extract content from the result.
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(resp.Result, &result); err == nil && len(result.Content) > 0 {
		var parts []string
		for _, c := range result.Content {
			if c.Type == "text" && c.Text != "" {
				parts = append(parts, c.Text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
	}

	// Fallback: treat the entire result as text.
	return string(resp.Result)
}

// scanText checks text against all default patterns and returns findings.
func scanText(text string) []scanFinding {
	var findings []scanFinding
	for _, pat := range defaultPatterns {
		matches := pat.Pattern.FindAllString(text, -1)
		if len(matches) > 0 {
			findings = append(findings, scanFinding{
				Pattern: pat.Name,
				Count:   len(matches),
			})
		}
	}
	return findings
}

// formatFindings produces a human-readable summary of scan findings.
func formatFindings(findings []scanFinding) string {
	parts := make([]string, len(findings))
	for i, f := range findings {
		parts[i] = fmt.Sprintf("%s(%d)", f.Pattern, f.Count)
	}
	return strings.Join(parts, ", ")
}

// redactResponse creates a copy of the response with detected patterns replaced.
func redactResponse(resp *mcp.Message, text string) *mcp.Message {
	redacted := text
	for _, pat := range defaultPatterns {
		redacted = pat.Pattern.ReplaceAllString(redacted, pat.Redact)
	}

	if redacted == text {
		return nil // Nothing actually changed.
	}

	// Build a new result with redacted content.
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(resp.Result, &result); err == nil && len(result.Content) > 0 {
		// Rebuild content with redacted text.
		type contentItem struct {
			Type string `json:"type"`
			Text string `json:"text,omitempty"`
		}
		newContent := make([]contentItem, len(result.Content))
		for i, c := range result.Content {
			newContent[i] = contentItem{Type: c.Type, Text: c.Text}
			if c.Type == "text" {
				for _, pat := range defaultPatterns {
					newContent[i].Text = pat.Pattern.ReplaceAllString(newContent[i].Text, pat.Redact)
				}
			}
		}
		newResult := map[string]any{"content": newContent}
		if data, err := json.Marshal(newResult); err == nil {
			return &mcp.Message{
				JSONRPC: resp.JSONRPC,
				ID:      resp.ID,
				Result:  data,
			}
		}
	}

	// Fallback: replace entire result.
	redactedResult := json.RawMessage(fmt.Sprintf(`{"content":[{"type":"text","text":%s}]}`,
		mustMarshalString(redacted)))
	return &mcp.Message{
		JSONRPC: resp.JSONRPC,
		ID:      resp.ID,
		Result:  redactedResult,
	}
}

func mustMarshalString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
