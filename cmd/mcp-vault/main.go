// mcp-vault provides MCP tools for HashiCorp Vault secrets management.
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/validate"
)

var (
	version = "0.1.0"

	vaultAddr  = getEnv("VAULT_ADDR", "http://127.0.0.1:8200")
	vaultToken = os.Getenv("VAULT_TOKEN")
	vaultNS    = os.Getenv("VAULT_NAMESPACE")

	httpClient *http.Client
)

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

func init() {
	transport := &http.Transport{}
	if skipVerify := os.Getenv("VAULT_SKIP_VERIFY"); strings.ToLower(skipVerify) == "true" || skipVerify == "1" {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	httpClient = &http.Client{
		Timeout:   time.Duration(getEnvInt("VAULT_TIMEOUT", 30)) * time.Second,
		Transport: transport,
	}
}

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()
	logger.Info("starting server", "name", "mcp-vault", "version", version, "addr", vaultAddr)

	server := mcp.NewServer("mcp-vault", version)
	server.SetInstructions("HashiCorp Vault secrets management tools. Configure with VAULT_ADDR and VAULT_TOKEN. Optionally set VAULT_NAMESPACE for enterprise namespaces.")

	// Health/Status
	server.AddTool(mcp.Tool{
		Name:        "vault_health",
		Description: "Get Vault server health status",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, handleHealth)

	server.AddTool(mcp.Tool{
		Name:        "vault_status",
		Description: "Get Vault seal status and cluster information",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, handleStatus)

	// Secret operations (KV v2)
	server.AddTool(mcp.Tool{
		Name:        "vault_read",
		Description: "Read a secret from Vault KV v2 secrets engine",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"mount": map[string]any{
					"type":        "string",
					"description": "Secrets engine mount path (default: 'secret')",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the secret within the mount",
				},
				"version": map[string]any{
					"type":        "integer",
					"description": "Specific version to read (default: latest)",
				},
			},
			Required: []string{"path"},
		},
	}, handleRead)

	server.AddTool(mcp.Tool{
		Name:        "vault_list",
		Description: "List secrets at a path in Vault KV v2 secrets engine",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"mount": map[string]any{
					"type":        "string",
					"description": "Secrets engine mount path (default: 'secret')",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Path to list (default: root of mount)",
				},
			},
		},
	}, handleList)

	server.AddTool(mcp.Tool{
		Name:        "vault_metadata",
		Description: "Get metadata for a secret (versions, created time, etc.)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"mount": map[string]any{
					"type":        "string",
					"description": "Secrets engine mount path (default: 'secret')",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the secret",
				},
			},
			Required: []string{"path"},
		},
	}, handleMetadata)

	// Mounts
	server.AddTool(mcp.Tool{
		Name:        "vault_mounts",
		Description: "List all secrets engine mounts",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, handleMounts)

	server.AddTool(mcp.Tool{
		Name:        "vault_auth_methods",
		Description: "List all enabled authentication methods",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, handleAuthMethods)

	// Token info
	server.AddTool(mcp.Tool{
		Name:        "vault_token_lookup",
		Description: "Look up information about the current token",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, handleTokenLookup)

	// Policies
	server.AddTool(mcp.Tool{
		Name:        "vault_policies",
		Description: "List all policies",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, handlePolicies)

	server.AddTool(mcp.Tool{
		Name:        "vault_policy_read",
		Description: "Read a specific policy",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Policy name",
				},
			},
			Required: []string{"name"},
		},
	}, handlePolicyRead)

	return server.Run(ctx)
}

// vaultRequest makes an authenticated request to Vault
func vaultRequest(ctx context.Context, method, path string) (map[string]any, error) {
	url := strings.TrimSuffix(vaultAddr, "/") + "/v1/" + strings.TrimPrefix(path, "/")

	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if vaultToken != "" {
		req.Header.Set("X-Vault-Token", vaultToken)
	}
	if vaultNS != "" {
		req.Header.Set("X-Vault-Namespace", vaultNS)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		// Try to parse error
		var errResp map[string]any
		if json.Unmarshal(body, &errResp) == nil {
			if errors, ok := errResp["errors"].([]any); ok && len(errors) > 0 {
				return nil, fmt.Errorf("vault error (%d): %v", resp.StatusCode, errors)
			}
		}
		return nil, fmt.Errorf("vault error (%d): %s", resp.StatusCode, string(body))
	}

	var result map[string]any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("parse response: %w", err)
		}
	}

	return result, nil
}

func handleHealth(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	// Health endpoint doesn't require auth
	url := strings.TrimSuffix(vaultAddr, "/") + "/v1/sys/health"

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	// Add status interpretation
	result["_http_status"] = resp.StatusCode
	switch resp.StatusCode {
	case 200:
		result["_status"] = "initialized, unsealed, active"
	case 429:
		result["_status"] = "unsealed, standby"
	case 472:
		result["_status"] = "disaster recovery secondary"
	case 473:
		result["_status"] = "performance standby"
	case 501:
		result["_status"] = "not initialized"
	case 503:
		result["_status"] = "sealed"
	}

	return mcp.JSONResult(result)
}

func handleStatus(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	result, err := vaultRequest(ctx, "GET", "sys/seal-status")
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(result)
}

func handleRead(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	mount := v.String("mount", "secret")
	path := v.Required("path")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Build KV v2 data path
	apiPath := mount + "/data/" + strings.TrimPrefix(path, "/")

	// Add version query param if specified
	if v, ok := args["version"].(float64); ok {
		apiPath += fmt.Sprintf("?version=%d", int(v))
	}

	result, err := vaultRequest(ctx, "GET", apiPath)
	if err != nil {
		return nil, err
	}

	// Extract data from KV v2 response
	response := map[string]any{
		"path":  path,
		"mount": mount,
	}
	if data, ok := result["data"].(map[string]any); ok {
		if secretData, ok := data["data"].(map[string]any); ok {
			response["data"] = secretData
		}
		if metadata, ok := data["metadata"].(map[string]any); ok {
			response["metadata"] = metadata
		}
	}

	return mcp.JSONResult(response)
}

func handleList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	mount := v.String("mount", "secret")
	path := v.String("path", "")

	// Build KV v2 metadata list path
	apiPath := mount + "/metadata/" + strings.TrimPrefix(path, "/")

	result, err := vaultRequest(ctx, "LIST", apiPath)
	if err != nil {
		return nil, err
	}

	response := map[string]any{
		"path":  path,
		"mount": mount,
	}
	if data, ok := result["data"].(map[string]any); ok {
		if keys, ok := data["keys"].([]any); ok {
			response["keys"] = keys
			response["count"] = len(keys)
		}
	}

	return mcp.JSONResult(response)
}

func handleMetadata(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	mount := v.String("mount", "secret")
	path := v.Required("path")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Build KV v2 metadata path
	apiPath := mount + "/metadata/" + strings.TrimPrefix(path, "/")

	result, err := vaultRequest(ctx, "GET", apiPath)
	if err != nil {
		return nil, err
	}

	response := map[string]any{
		"path":  path,
		"mount": mount,
	}
	if data, ok := result["data"].(map[string]any); ok {
		response["metadata"] = data
	}

	return mcp.JSONResult(response)
}

func handleMounts(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	result, err := vaultRequest(ctx, "GET", "sys/mounts")
	if err != nil {
		return nil, err
	}

	// Format mounts for readability
	mounts := []map[string]any{}
	if data, ok := result["data"].(map[string]any); ok {
		for path, info := range data {
			if mountInfo, ok := info.(map[string]any); ok {
				mounts = append(mounts, map[string]any{
					"path":        path,
					"type":        mountInfo["type"],
					"description": mountInfo["description"],
					"accessor":    mountInfo["accessor"],
				})
			}
		}
	} else {
		// Older API format
		for path, info := range result {
			if path == "request_id" || path == "lease_id" || path == "renewable" || path == "lease_duration" || path == "wrap_info" || path == "warnings" || path == "auth" {
				continue
			}
			if mountInfo, ok := info.(map[string]any); ok {
				mounts = append(mounts, map[string]any{
					"path":        path,
					"type":        mountInfo["type"],
					"description": mountInfo["description"],
					"accessor":    mountInfo["accessor"],
				})
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"mounts": mounts,
		"count":  len(mounts),
	})
}

func handleAuthMethods(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	result, err := vaultRequest(ctx, "GET", "sys/auth")
	if err != nil {
		return nil, err
	}

	// Format auth methods
	methods := []map[string]any{}
	data := result
	if d, ok := result["data"].(map[string]any); ok {
		data = d
	}

	for path, info := range data {
		if path == "request_id" || path == "lease_id" || path == "renewable" || path == "lease_duration" || path == "wrap_info" || path == "warnings" || path == "auth" {
			continue
		}
		if authInfo, ok := info.(map[string]any); ok {
			methods = append(methods, map[string]any{
				"path":        path,
				"type":        authInfo["type"],
				"description": authInfo["description"],
				"accessor":    authInfo["accessor"],
			})
		}
	}

	return mcp.JSONResult(map[string]any{
		"auth_methods": methods,
		"count":        len(methods),
	})
}

func handleTokenLookup(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	result, err := vaultRequest(ctx, "GET", "auth/token/lookup-self")
	if err != nil {
		return nil, err
	}

	if data, ok := result["data"].(map[string]any); ok {
		return mcp.JSONResult(data)
	}
	return mcp.JSONResult(result)
}

func handlePolicies(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	result, err := vaultRequest(ctx, "LIST", "sys/policies/acl")
	if err != nil {
		return nil, err
	}

	policies := []string{}
	if data, ok := result["data"].(map[string]any); ok {
		if keys, ok := data["keys"].([]any); ok {
			for _, k := range keys {
				if s, ok := k.(string); ok {
					policies = append(policies, s)
				}
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"policies": policies,
		"count":    len(policies),
	})
}

func handlePolicyRead(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := v.Required("name")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	result, err := vaultRequest(ctx, "GET", "sys/policies/acl/"+name)
	if err != nil {
		return nil, err
	}

	response := map[string]any{
		"name": name,
	}
	if data, ok := result["data"].(map[string]any); ok {
		if policy, ok := data["policy"].(string); ok {
			response["policy"] = policy
		}
	}

	return mcp.JSONResult(response)
}
