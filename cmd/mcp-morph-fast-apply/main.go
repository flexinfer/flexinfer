// mcp-morph-fast-apply provides Morph's fast code edit application via MCP.
// It uses the Morph API to intelligently merge code changes into files.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/crb2nu/loom/pkg/pathsec"
	"gitlab.flexinfer.ai/libs/mcp-go"
)

const (
	maxFileSize     = 10 * 1024 * 1024 // 10MB max input file
	maxResponseSize = 20 * 1024 * 1024 // 20MB max response
)

// Shared HTTP client for connection reuse
var httpClient = &http.Client{Timeout: 90 * time.Second}

var version = "dev"

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
			return
		}
	}()

	server := mcp.NewServer("mcp-morph-fast-apply", version)
	server.SetInstructions("Morph Fast Apply server for intelligent code editing. Use edit_file to apply code changes.")

	// edit_file - Apply code edits to a file
	server.AddTool(mcp.Tool{
		Name:        "edit_file",
		Description: "Apply code edits to a file using Morph's fast apply model",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the file to edit (absolute or relative to workspace)",
				},
				"instruction": map[string]any{
					"type":        "string",
					"description": "Description of the changes to make",
				},
				"update": map[string]any{
					"type":        "string",
					"description": "Code snippet showing the changes (use '// ... existing code ...' for unchanged sections)",
				},
			},
			Required: []string{"path", "instruction", "update"},
		},
	}, handleEditFile)

	// Also register as morph_edit_file for compatibility
	server.AddTool(mcp.Tool{
		Name:        "morph_edit_file",
		Description: "Apply code edits to a file using Morph's fast apply model (alias)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the file to edit (absolute or relative to workspace)",
				},
				"instruction": map[string]any{
					"type":        "string",
					"description": "Description of the changes to make",
				},
				"update": map[string]any{
					"type":        "string",
					"description": "Code snippet showing the changes (use '// ... existing code ...' for unchanged sections)",
				},
			},
			Required: []string{"path", "instruction", "update"},
		},
	}, handleEditFile)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func getConfig() (baseURL, apiKey, model string, err error) {
	apiKey = strings.TrimSpace(os.Getenv("MORPH_API_KEY"))
	baseURL = strings.TrimSpace(os.Getenv("MORPH_BASE_URL"))
	model = strings.TrimSpace(os.Getenv("MORPH_MODEL"))

	if baseURL == "" {
		baseURL = "https://api.morphllm.com/v1"
	}
	baseURL = strings.TrimSuffix(baseURL, "/")

	if model == "" {
		model = "morph-v3-large"
	}

	if apiKey == "" {
		return "", "", "", fmt.Errorf("MORPH_API_KEY not set")
	}

	return baseURL, apiKey, model, nil
}

// getWorkspaceRoot returns the workspace root directory.
func getWorkspaceRoot() (string, error) {
	// Check MORPH_WORKSPACE_ROOT first (more specific)
	if wsRoot := os.Getenv("MORPH_WORKSPACE_ROOT"); wsRoot != "" {
		return filepath.Abs(wsRoot)
	}
	// Check WORKSPACE_ROOT (general)
	if wsRoot := os.Getenv("WORKSPACE_ROOT"); wsRoot != "" {
		return filepath.Abs(wsRoot)
	}
	// Fall back to current directory
	return os.Getwd()
}

// resolvePath resolves a path and validates it is within the workspace.
func resolvePath(path string) (string, error) {
	workspaceRoot, err := getWorkspaceRoot()
	if err != nil {
		return "", fmt.Errorf("get workspace root: %w", err)
	}

	// Clean and make absolute
	absPath, err := pathsec.CleanPath(path)
	if err != nil {
		return "", fmt.Errorf("clean path: %w", err)
	}

	// If relative, join with workspace root
	if !filepath.IsAbs(path) {
		absPath = filepath.Join(workspaceRoot, path)
	}

	// Validate path is within workspace
	if err := pathsec.ValidatePath(absPath, workspaceRoot); err != nil {
		return "", fmt.Errorf("path validation: %w", err)
	}

	return absPath, nil
}

func handleEditFile(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	baseURL, apiKey, model, err := getConfig()
	if err != nil {
		return nil, err
	}

	path, _ := args["path"].(string)
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}

	instruction, _ := args["instruction"].(string)
	if instruction == "" {
		return nil, fmt.Errorf("instruction is required")
	}

	update, _ := args["update"].(string)
	if update == "" {
		return nil, fmt.Errorf("update is required")
	}

	// Resolve and validate path
	absPath, err := resolvePath(path)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}

	// Validate file size before reading
	if err := pathsec.ValidateFileSize(absPath, maxFileSize); err != nil {
		return nil, fmt.Errorf("file validation: %w", err)
	}

	// Read original file
	originalCode, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", absPath, err)
	}

	// Build Morph API request
	content := fmt.Sprintf(`<instruction>%s</instruction>
<code>%s</code>
<update>%s</update>`, instruction, string(originalCode), update)

	requestBody := map[string]any{
		"model": model,
		"messages": []map[string]any{
			{
				"role":    "user",
				"content": content,
			},
		},
		"stream": false,
	}

	body, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := baseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call Morph API: %w", err)
	}
	defer resp.Body.Close()

	// Limit response size to prevent memory exhaustion
	limitedReader := io.LimitReader(resp.Body, maxResponseSize+1)
	respBody, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if len(respBody) > maxResponseSize {
		return nil, fmt.Errorf("response too large: exceeds %d bytes", maxResponseSize)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("morph API error %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse response
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("no response from Morph API")
	}

	newCode := result.Choices[0].Message.Content

	// Validate response size before writing
	if len(newCode) > maxResponseSize {
		return nil, fmt.Errorf("edited content too large: %d bytes", len(newCode))
	}

	// Write the updated file
	if err := os.WriteFile(absPath, []byte(newCode), 0644); err != nil {
		return nil, fmt.Errorf("failed to write file: %w", err)
	}

	return mcp.JSONResult(map[string]any{
		"success": true,
		"path":    absPath,
		"bytes":   len(newCode),
		"tokens":  result.Usage.TotalTokens,
		"model":   model,
		"message": fmt.Sprintf("Applied edits to %s", path),
	})
}
