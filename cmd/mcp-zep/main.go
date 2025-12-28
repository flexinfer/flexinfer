// mcp-zep provides a Zep Cloud memory MCP server for managing
// conversation sessions and messages.
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

	server := mcp.NewServer("mcp-zep", version)
	server.SetInstructions("Zep Cloud memory server. Tools: zep_health, zep_add_messages, zep_get_messages")

	// zep_health - Check connectivity
	server.AddTool(mcp.Tool{
		Name:        "zep_health",
		Description: "Check connectivity to Zep Cloud and token validity",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, handleHealth)

	// zep_add_messages - Add messages to session
	server.AddTool(mcp.Tool{
		Name:        "zep_add_messages",
		Description: "Append messages to a Zep session",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Conversation/session identifier",
				},
				"messages": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Message contents; treated as alternating user/assistant if roles omitted",
				},
				"roles": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Optional per-message roles: user|assistant|system (same length as messages)",
				},
			},
			Required: []string{"session_id", "messages"},
		},
	}, handleAddMessages)

	// zep_get_messages - Get messages from session
	server.AddTool(mcp.Tool{
		Name:        "zep_get_messages",
		Description: "Return the last K messages for a Zep session",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Session identifier",
				},
				"last_k": map[string]any{
					"type":        "integer",
					"description": "Number of recent messages to retrieve (1-200, default 10)",
				},
			},
			Required: []string{"session_id"},
		},
	}, handleGetMessages)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func getConfig() (apiURL, apiKey string, err error) {
	apiKey = strings.TrimSpace(os.Getenv("ZEP_API_KEY"))
	apiURL = strings.TrimSpace(os.Getenv("ZEP_API_URL"))
	if apiURL == "" {
		apiURL = "https://api.getzep.com"
	}
	apiURL = strings.TrimSuffix(apiURL, "/")

	if apiKey == "" {
		return "", "", fmt.Errorf("ZEP_API_KEY not set")
	}
	return apiURL, apiKey, nil
}

func handleHealth(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	apiURL, apiKey, err := getConfig()
	if err != nil {
		return mcp.JSONResult(map[string]any{
			"ok":     false,
			"detail": err.Error(),
		})
	}

	client := &http.Client{Timeout: 5 * time.Second}

	// Try various health endpoints
	healthEndpoints := []string{
		apiURL + "/health",
		apiURL + "/v1/health",
		apiURL + "/v2/health",
	}

	for _, url := range healthEndpoints {
		req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
		req.Header.Set("Authorization", "Bearer "+apiKey)

		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		resp.Body.Close()

		if resp.StatusCode < 500 {
			return mcp.JSONResult(map[string]any{
				"ok":     true,
				"detail": fmt.Sprintf("%s: %d", url, resp.StatusCode),
			})
		}
	}

	// Fallback: try to get a session (will validate token)
	req, _ := http.NewRequestWithContext(ctx, "GET", apiURL+"/v2/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return mcp.JSONResult(map[string]any{
			"ok":     false,
			"detail": fmt.Sprintf("connection failed: %v", err),
		})
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 || resp.StatusCode == 404 {
		return mcp.JSONResult(map[string]any{
			"ok":     true,
			"detail": "API token validated",
		})
	}

	return mcp.JSONResult(map[string]any{
		"ok":     false,
		"detail": fmt.Sprintf("API returned: %d", resp.StatusCode),
	})
}

// ZepMessage represents a message in Zep format
type ZepMessage struct {
	Role     string `json:"role"`
	RoleType string `json:"role_type"`
	Content  string `json:"content"`
}

func handleAddMessages(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	apiURL, apiKey, err := getConfig()
	if err != nil {
		return nil, err
	}

	sessionID, _ := args["session_id"].(string)
	if sessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}

	messagesRaw, _ := args["messages"].([]any)
	if len(messagesRaw) == 0 {
		return nil, fmt.Errorf("messages is required")
	}

	var roles []string
	if rolesRaw, ok := args["roles"].([]any); ok {
		for _, r := range rolesRaw {
			if s, ok := r.(string); ok {
				roles = append(roles, s)
			}
		}
	}

	// Build Zep messages
	var messages []ZepMessage
	for i, msgRaw := range messagesRaw {
		content, _ := msgRaw.(string)
		if content == "" {
			continue
		}

		var roleType string
		if i < len(roles) {
			roleType = roles[i]
		} else {
			// Alternate user/assistant
			if i%2 == 0 {
				roleType = "user"
			} else {
				roleType = "assistant"
			}
		}

		roleName := roleType

		messages = append(messages, ZepMessage{
			Role:     roleName,
			RoleType: roleType,
			Content:  content,
		})
	}

	// Send to Zep API
	payload := map[string]any{
		"messages": messages,
	}
	body, _ := json.Marshal(payload)

	client := &http.Client{Timeout: 30 * time.Second}
	url := fmt.Sprintf("%s/v2/sessions/%s/memory", apiURL, sessionID)
	req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to add messages: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("zep API error %d: %s", resp.StatusCode, string(respBody))
	}

	return mcp.JSONResult(map[string]any{
		"ok":    true,
		"count": len(messages),
	})
}

func handleGetMessages(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	apiURL, apiKey, err := getConfig()
	if err != nil {
		return nil, err
	}

	sessionID, _ := args["session_id"].(string)
	if sessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}

	lastK := 10
	if k, ok := args["last_k"].(float64); ok && k > 0 {
		lastK = int(k)
		if lastK > 200 {
			lastK = 200
		}
	}

	client := &http.Client{Timeout: 30 * time.Second}
	url := fmt.Sprintf("%s/v2/sessions/%s/memory?lastK=%d", apiURL, sessionID, lastK)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get messages: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("zep API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Messages []struct {
			Role     string `json:"role"`
			RoleType string `json:"role_type"`
			Content  string `json:"content"`
		} `json:"messages"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	var messages []map[string]string
	for _, m := range result.Messages {
		messages = append(messages, map[string]string{
			"role":      m.Role,
			"role_type": m.RoleType,
			"content":   m.Content,
		})
	}

	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"count":    len(messages),
		"messages": messages,
	})
}
