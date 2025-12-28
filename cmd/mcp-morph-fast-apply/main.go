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

	"gitlab.flexinfer.ai/libs/mcp-go"
)

var version = "dev"

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
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

func resolvePath(path string) string {
	// If absolute, use as is
	if filepath.IsAbs(path) {
		return path
	}

	// Check WORKSPACE_ROOT env var
	if wsRoot := os.Getenv("WORKSPACE_ROOT"); wsRoot != "" {
		return filepath.Join(wsRoot, path)
	}

	// Fall back to current directory
	cwd, _ := os.Getwd()
	return filepath.Join(cwd, path)
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

	// Resolve path
	absPath := resolvePath(path)

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

	body, _ := json.Marshal(requestBody)

	client := &http.Client{Timeout: 90 * time.Second}
	url := baseURL + "/chat/completions"
	req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call Morph API: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

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
