package daemon

import (
	"encoding/json"
	"testing"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScanText_Clean(t *testing.T) {
	findings := scanText("hello world, no secrets here")
	assert.Empty(t, findings)
}

func TestScanText_AWSKey(t *testing.T) {
	findings := scanText("Found key: AKIAIOSFODNN7EXAMPLE in config")
	require.Len(t, findings, 1)
	assert.Equal(t, "aws_access_key", findings[0].Pattern)
	assert.Equal(t, 1, findings[0].Count)
}

func TestScanText_GitHubToken(t *testing.T) {
	findings := scanText("token: ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmn")
	require.NotEmpty(t, findings)
	found := false
	for _, f := range findings {
		if f.Pattern == "github_token" {
			found = true
		}
	}
	assert.True(t, found, "should detect github_token pattern")
}

func TestScanText_PrivateKey(t *testing.T) {
	// Split marker to avoid pre-commit private-key detection.
	marker := "-----BEGIN RSA " + "PRIVATE KEY-----"
	findings := scanText(marker + "\nMIIEowIBAAK...")
	require.Len(t, findings, 1)
	assert.Equal(t, "private_key", findings[0].Pattern)
}

func TestScanText_SSN(t *testing.T) {
	findings := scanText("SSN: 123-45-6789")
	require.Len(t, findings, 1)
	assert.Equal(t, "ssn", findings[0].Pattern)
}

func TestScanText_MultipleFindings(t *testing.T) {
	text := "key=AKIAIOSFODNN7EXAMPLE ssn=123-45-6789"
	findings := scanText(text)
	assert.GreaterOrEqual(t, len(findings), 2)
}

func TestExtractResponseText_Nil(t *testing.T) {
	assert.Equal(t, "", extractResponseText(nil))
}

func TestExtractResponseText_Content(t *testing.T) {
	resp := &mcp.Message{
		Result: json.RawMessage(`{"content":[{"type":"text","text":"hello world"}]}`),
	}
	assert.Equal(t, "hello world", extractResponseText(resp))
}

func TestExtractResponseText_FallbackRaw(t *testing.T) {
	resp := &mcp.Message{
		Result: json.RawMessage(`{"data":"raw result"}`),
	}
	assert.Equal(t, `{"data":"raw result"}`, extractResponseText(resp))
}

func TestFormatFindings(t *testing.T) {
	findings := []scanFinding{
		{Pattern: "aws_access_key", Count: 1},
		{Pattern: "email", Count: 3},
	}
	assert.Equal(t, "aws_access_key(1), email(3)", formatFindings(findings))
}

func TestRedactResponse(t *testing.T) {
	resp := &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      json.RawMessage(`1`),
		Result:  json.RawMessage(`{"content":[{"type":"text","text":"key=AKIAIOSFODNN7EXAMPLE found"}]}`),
	}
	text := extractResponseText(resp)
	redacted := redactResponse(resp, text)
	require.NotNil(t, redacted)

	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	require.NoError(t, json.Unmarshal(redacted.Result, &result))
	assert.Contains(t, result.Content[0].Text, "[REDACTED:aws_key]")
	assert.NotContains(t, result.Content[0].Text, "AKIAIOSFODNN7EXAMPLE")
}

func TestScanOutputForPII_Off(t *testing.T) {
	d := &Daemon{
		fileCfg:   FileConfig{OutputScanning: OutputScanningConfig{Mode: OutputScanOff}},
		toolCache: &ToolCache{},
	}
	p := &callPipeline{
		daemon:     d,
		msg:        testMsg(),
		serverName: "test",
		toolName:   "read",
	}
	resp := p.scanOutputForPII(&mcp.Message{
		Result: json.RawMessage(`{"content":[{"type":"text","text":"AKIAIOSFODNN7EXAMPLE"}]}`),
	})
	assert.Nil(t, resp, "off mode should skip scanning")
}

func TestScanOutputForPII_Warn_Clean(t *testing.T) {
	d := &Daemon{
		fileCfg:   FileConfig{OutputScanning: OutputScanningConfig{Mode: OutputScanWarn}},
		toolCache: &ToolCache{},
	}
	p := &callPipeline{
		daemon:     d,
		msg:        testMsg(),
		serverName: "test",
		toolName:   "read",
	}
	resp := p.scanOutputForPII(&mcp.Message{
		Result: json.RawMessage(`{"content":[{"type":"text","text":"clean output"}]}`),
	})
	assert.Nil(t, resp, "clean output should pass")
}

func TestScanOutputForPII_Warn_Dirty(t *testing.T) {
	d := &Daemon{
		fileCfg:   FileConfig{OutputScanning: OutputScanningConfig{Mode: OutputScanWarn}},
		toolCache: &ToolCache{},
	}
	p := &callPipeline{
		daemon:     d,
		msg:        testMsg(),
		serverName: "test",
		toolName:   "read",
	}
	resp := p.scanOutputForPII(&mcp.Message{
		Result: json.RawMessage(`{"content":[{"type":"text","text":"key=AKIAIOSFODNN7EXAMPLE"}]}`),
	})
	assert.Nil(t, resp, "warn mode should allow response through")
}

func TestScanOutputForPII_Redact(t *testing.T) {
	d := &Daemon{
		fileCfg:   FileConfig{OutputScanning: OutputScanningConfig{Mode: OutputScanRedact}},
		toolCache: &ToolCache{},
	}
	p := &callPipeline{
		daemon:     d,
		msg:        testMsg(),
		serverName: "test",
		toolName:   "read",
	}
	original := &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      json.RawMessage(`1`),
		Result:  json.RawMessage(`{"content":[{"type":"text","text":"key=AKIAIOSFODNN7EXAMPLE found"}]}`),
	}
	resp := p.scanOutputForPII(original)
	require.NotNil(t, resp, "redact mode should return modified response")

	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	require.NoError(t, json.Unmarshal(resp.Result, &result))
	assert.Contains(t, result.Content[0].Text, "[REDACTED:aws_key]")
}

func TestScanText_JWT(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4iLCJpYXQiOjE1MTYyMzkwMjJ9.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	findings := scanText("Bearer " + jwt)
	found := false
	for _, f := range findings {
		if f.Pattern == "jwt" {
			found = true
		}
	}
	assert.True(t, found, "should detect JWT")
}
