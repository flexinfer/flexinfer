package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

var (
	version       = "0.1.0"
	cfAPIToken    = os.Getenv("CF_API_TOKEN")
	cfAccountID   = os.Getenv("CF_ACCOUNT_ID")
	cfAPIBase     = getEnv("CF_API_BASE", "https://api.cloudflare.com")
	cfHTTPTimeout = getEnvDuration("CF_HTTP_TIMEOUT", 30*time.Second)
)

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return time.Duration(f * float64(time.Second))
		}
	}
	return fallback
}

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

	server := mcp.NewServer("mcp-cloudflare", version)
	server.SetInstructions("Cloudflare API tools")

	// Tools
	server.AddTool(mcp.Tool{
		Name:        "cf_verify_token",
		Description: "Verify Cloudflare API token status and scopes",
		InputSchema: mcp.InputSchema{Type: "object", Properties: map[string]any{}},
	}, handleVerifyToken)

	server.AddTool(mcp.Tool{
		Name:        "cf_list_zones",
		Description: "List Cloudflare DNS zones",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"per_page": map[string]any{"type": "integer", "description": "Items per page (default 50)"},
				"page":     map[string]any{"type": "integer", "description": "Page number (default 1)"},
			},
		},
	}, handleListZones)

	server.AddTool(mcp.Tool{
		Name:        "cf_list_tunnels",
		Description: "List Cloudflare tunnels for the configured account",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"per_page": map[string]any{"type": "integer", "description": "Items per page (default 50)"},
				"page":     map[string]any{"type": "integer", "description": "Page number (default 1)"},
			},
		},
	}, handleListTunnels)

	// DNS Record tools
	server.AddTool(mcp.Tool{
		Name:        "cf_list_dns_records",
		Description: "List DNS records for a zone",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"zone_id": map[string]any{"type": "string", "description": "Zone ID"},
				"type":    map[string]any{"type": "string", "description": "Record type filter (A, AAAA, CNAME, TXT, etc.)"},
				"name":    map[string]any{"type": "string", "description": "Record name filter"},
			},
			Required: []string{"zone_id"},
		},
	}, handleListDNSRecords)

	server.AddTool(mcp.Tool{
		Name:        "cf_create_dns_record",
		Description: "Create a DNS record",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"zone_id": map[string]any{"type": "string", "description": "Zone ID"},
				"type":    map[string]any{"type": "string", "description": "Record type (A, AAAA, CNAME, TXT, MX, etc.)"},
				"name":    map[string]any{"type": "string", "description": "Record name (e.g., 'www' or 'www.example.com')"},
				"content": map[string]any{"type": "string", "description": "Record content (IP, hostname, or text)"},
				"ttl":     map[string]any{"type": "integer", "description": "TTL in seconds (1 = automatic)"},
				"proxied": map[string]any{"type": "boolean", "description": "Enable Cloudflare proxy (default: false)"},
			},
			Required: []string{"zone_id", "type", "name", "content"},
		},
	}, handleCreateDNSRecord)

	server.AddTool(mcp.Tool{
		Name:        "cf_update_dns_record",
		Description: "Update a DNS record",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"zone_id":   map[string]any{"type": "string", "description": "Zone ID"},
				"record_id": map[string]any{"type": "string", "description": "Record ID to update"},
				"type":      map[string]any{"type": "string", "description": "Record type (A, AAAA, CNAME, TXT, MX, etc.)"},
				"name":      map[string]any{"type": "string", "description": "Record name"},
				"content":   map[string]any{"type": "string", "description": "Record content"},
				"ttl":       map[string]any{"type": "integer", "description": "TTL in seconds (1 = automatic)"},
				"proxied":   map[string]any{"type": "boolean", "description": "Enable Cloudflare proxy"},
			},
			Required: []string{"zone_id", "record_id", "type", "name", "content"},
		},
	}, handleUpdateDNSRecord)

	server.AddTool(mcp.Tool{
		Name:        "cf_delete_dns_record",
		Description: "Delete a DNS record",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"zone_id":   map[string]any{"type": "string", "description": "Zone ID"},
				"record_id": map[string]any{"type": "string", "description": "Record ID to delete"},
			},
			Required: []string{"zone_id", "record_id"},
		},
	}, handleDeleteDNSRecord)

	server.AddTool(mcp.Tool{
		Name:        "cf_purge_cache",
		Description: "Purge cache for a zone",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"zone_id":   map[string]any{"type": "string", "description": "Zone ID"},
				"purge_all": map[string]any{"type": "boolean", "description": "Purge everything (use with caution)"},
				"files":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Specific URLs to purge"},
				"tags":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Cache tags to purge"},
				"hosts":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Hostnames to purge"},
				"prefixes":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "URL prefixes to purge"},
			},
			Required: []string{"zone_id"},
		},
	}, handlePurgeCache)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// Cloudflare Client

func cfRequest(method, path string, params map[string]string) (map[string]any, error) {
	return cfRequestWithBody(method, path, params, nil)
}

func cfRequestWithBody(method, path string, params map[string]string, body any) (map[string]any, error) {
	if cfAPIToken == "" {
		return nil, fmt.Errorf("CF_API_TOKEN is required")
	}

	u, err := url.Parse(cfAPIBase + "/client/v4" + path)
	if err != nil {
		return nil, err
	}

	if len(params) > 0 {
		q := u.Query()
		for k, v := range params {
			q.Set(k, v)
		}
		u.RawQuery = q.Encode()
	}

	var bodyReader io.Reader
	if body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequest(method, u.String(), bodyReader)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfAPIToken)
	req.Header.Set("User-Agent", "mcp-cloudflare/0.1")

	client := &http.Client{Timeout: cfHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if success, ok := result["success"].(bool); !ok || !success {
		// Try to extract errors
		if errs, ok := result["errors"].([]any); ok && len(errs) > 0 {
			return nil, fmt.Errorf("api error: %v", errs)
		}
		return nil, fmt.Errorf("api request failed: %s", string(respBody))
	}

	return result, nil
}

// Handlers

func handleVerifyToken(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	res, err := cfRequest("GET", "/user/tokens/verify", nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	data := res["result"].(map[string]any)
	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"token_id": data["id"],
		"status":   data["status"],
		"scopes":   data["policies"],
	})
}

func handleListZones(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	params := make(map[string]string)
	if v, ok := args["per_page"].(float64); ok {
		params["per_page"] = fmt.Sprintf("%d", int(v))
	}
	if v, ok := args["page"].(float64); ok {
		params["page"] = fmt.Sprintf("%d", int(v))
	}

	res, err := cfRequest("GET", "/zones", params)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	zones := res["result"].([]any)
	return mcp.JSONResult(map[string]any{
		"ok":    true,
		"count": len(zones),
		"zones": zones,
	})
}

func handleListTunnels(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if cfAccountID == "" {
		return mcp.ErrorResult(fmt.Errorf("CF_ACCOUNT_ID is required for tunnels")), nil
	}

	params := make(map[string]string)
	if v, ok := args["per_page"].(float64); ok {
		params["per_page"] = fmt.Sprintf("%d", int(v))
	}
	if v, ok := args["page"].(float64); ok {
		params["page"] = fmt.Sprintf("%d", int(v))
	}

	path := fmt.Sprintf("/accounts/%s/tunnels", cfAccountID)
	res, err := cfRequest("GET", path, params)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	tunnels := res["result"].([]any)
	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"count":   len(tunnels),
		"tunnels": tunnels,
	})
}

// DNS Record Handlers

func handleListDNSRecords(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	zoneID := v.Required("zone_id")
	recordType := v.String("type", "")
	name := v.String("name", "")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	params := make(map[string]string)
	if recordType != "" {
		params["type"] = strings.ToUpper(recordType)
	}
	if name != "" {
		params["name"] = name
	}

	path := fmt.Sprintf("/zones/%s/dns_records", zoneID)
	res, err := cfRequest("GET", path, params)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	records := res["result"].([]any)
	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"zone_id": zoneID,
		"count":   len(records),
		"records": records,
	})
}

func handleCreateDNSRecord(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	zoneID := v.Required("zone_id")
	recordType := v.Required("type")
	name := v.Required("name")
	content := v.Required("content")
	ttl := v.Int("ttl", 1) // 1 = automatic
	proxied := v.Bool("proxied", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	body := map[string]any{
		"type":    strings.ToUpper(recordType),
		"name":    name,
		"content": content,
		"ttl":     ttl,
		"proxied": proxied,
	}

	path := fmt.Sprintf("/zones/%s/dns_records", zoneID)
	res, err := cfRequestWithBody("POST", path, nil, body)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	record := res["result"].(map[string]any)
	return mcp.JSONResult(map[string]any{
		"ok":        true,
		"message":   "DNS record created",
		"record_id": record["id"],
		"record":    record,
	})
}

func handleUpdateDNSRecord(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	zoneID := v.Required("zone_id")
	recordID := v.Required("record_id")
	recordType := v.Required("type")
	name := v.Required("name")
	content := v.Required("content")
	ttl := v.Int("ttl", 1)
	proxied := v.Bool("proxied", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	body := map[string]any{
		"type":    strings.ToUpper(recordType),
		"name":    name,
		"content": content,
		"ttl":     ttl,
		"proxied": proxied,
	}

	path := fmt.Sprintf("/zones/%s/dns_records/%s", zoneID, recordID)
	res, err := cfRequestWithBody("PUT", path, nil, body)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	record := res["result"].(map[string]any)
	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"message": "DNS record updated",
		"record":  record,
	})
}

func handleDeleteDNSRecord(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	zoneID := v.Required("zone_id")
	recordID := v.Required("record_id")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := fmt.Sprintf("/zones/%s/dns_records/%s", zoneID, recordID)
	_, err := cfRequest("DELETE", path, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":        true,
		"message":   "DNS record deleted",
		"record_id": recordID,
	})
}

func handlePurgeCache(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	zoneID := v.Required("zone_id")
	purgeAll := v.Bool("purge_all", false)
	files := v.StringSlice("files")
	tags := v.StringSlice("tags")
	hosts := v.StringSlice("hosts")
	prefixes := v.StringSlice("prefixes")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	body := make(map[string]any)
	if purgeAll {
		body["purge_everything"] = true
	} else {
		if len(files) > 0 {
			body["files"] = files
		}
		if len(tags) > 0 {
			body["tags"] = tags
		}
		if len(hosts) > 0 {
			body["hosts"] = hosts
		}
		if len(prefixes) > 0 {
			body["prefixes"] = prefixes
		}
	}

	// Must have at least one purge option
	if len(body) == 0 {
		return mcp.ErrorResult(fmt.Errorf("must specify purge_all, files, tags, hosts, or prefixes")), nil
	}

	path := fmt.Sprintf("/zones/%s/purge_cache", zoneID)
	_, err := cfRequestWithBody("POST", path, nil, body)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"message": "Cache purge initiated",
		"zone_id": zoneID,
	})
}
