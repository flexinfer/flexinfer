package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/mcperror"
)

func TestMain(m *testing.M) {
	os.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")
	os.Exit(m.Run())
}

// mustParseJSON extracts the JSON from a CallToolResult's first content block.
func mustParseJSON(t *testing.T, result any) map[string]any {
	t.Helper()
	b, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var wrapper struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(b, &wrapper); err != nil {
		t.Fatalf("unmarshal wrapper: %v", err)
	}
	if len(wrapper.Content) == 0 {
		t.Fatal("no content blocks in result")
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(wrapper.Content[0].Text), &out); err != nil {
		t.Fatalf("unmarshal result JSON: %v (raw: %s)", err, wrapper.Content[0].Text)
	}
	return out
}

// setVaultGlobals overrides the package-level globals to point at a test server
// and restores them when the test finishes.
func setVaultGlobals(t *testing.T, tsURL string) {
	t.Helper()
	origAddr := vaultAddr
	origToken := vaultToken
	origNS := vaultNS
	origClient := httpClient

	vaultAddr = tsURL
	vaultToken = "test-vault-token"
	vaultNS = ""
	httpClient = httpclient.NewDefault()

	t.Cleanup(func() {
		vaultAddr = origAddr
		vaultToken = origToken
		vaultNS = origNS
		httpClient = origClient
	})
}

// =====================================================================
// vaultRequest tests
// =====================================================================

func TestVaultRequest_SetsHeaders(t *testing.T) {
	var gotToken, gotNS string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Vault-Token")
		gotNS = r.Header.Get("X-Vault-Namespace")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"data":{}}`)
	}))
	defer ts.Close()

	setVaultGlobals(t, ts.URL)
	vaultNS = "test-ns"

	_, err := vaultRequest(context.Background(), "GET", "sys/health")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotToken != "test-vault-token" {
		t.Fatalf("expected X-Vault-Token header, got %q", gotToken)
	}
	if gotNS != "test-ns" {
		t.Fatalf("expected X-Vault-Namespace header, got %q", gotNS)
	}
}

func TestVaultRequest_BuildsURL(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{}`)
	}))
	defer ts.Close()

	setVaultGlobals(t, ts.URL)

	_, err := vaultRequest(context.Background(), "GET", "secret/data/myapp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPath != "/v1/secret/data/myapp" {
		t.Fatalf("expected /v1/secret/data/myapp, got %q", gotPath)
	}
}

func TestVaultRequest_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `{"errors":["permission denied"]}`)
	}))
	defer ts.Close()

	setVaultGlobals(t, ts.URL)

	_, err := vaultRequest(context.Background(), "GET", "secret/data/forbidden")
	if err == nil {
		t.Fatal("expected error for 403 response")
	}

	mcpErr, ok := err.(*mcperror.Error)
	if !ok {
		t.Fatalf("expected *mcperror.Error, got %T: %v", err, err)
	}
	if mcpErr.Code != mcperror.CodeForbidden {
		t.Fatalf("expected code %q, got %q", mcperror.CodeForbidden, mcpErr.Code)
	}
	if !strings.Contains(mcpErr.Message, "Vault") {
		t.Fatalf("error should mention Vault: %q", mcpErr.Message)
	}
}

func TestVaultRequest_HTTPError_NoErrorsField(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `internal server error`)
	}))
	defer ts.Close()

	setVaultGlobals(t, ts.URL)

	_, err := vaultRequest(context.Background(), "GET", "sys/health")
	if err == nil {
		t.Fatal("expected error for 500 response")
	}

	mcpErr, ok := err.(*mcperror.Error)
	if !ok {
		t.Fatalf("expected *mcperror.Error, got %T: %v", err, err)
	}
	if mcpErr.Code != mcperror.CodeServerError {
		t.Fatalf("expected code %q, got %q", mcperror.CodeServerError, mcpErr.Code)
	}
}

func TestVaultRequest_EmptyBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	setVaultGlobals(t, ts.URL)

	result, err := vaultRequest(context.Background(), "GET", "sys/health")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil result for empty body, got %v", result)
	}
}

// =====================================================================
// handleHealth tests
// =====================================================================

func TestHandleHealth_Active(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sys/health" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{
			"initialized": true,
			"sealed":      false,
			"standby":     false,
		})
	}))
	defer ts.Close()

	setVaultGlobals(t, ts.URL)

	result, err := handleHealth(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data := mustParseJSON(t, result)
	if data["_status"] != "initialized, unsealed, active" {
		t.Fatalf("expected active status, got %q", data["_status"])
	}
	if data["_http_status"] != float64(200) {
		t.Fatalf("expected http_status=200, got %v", data["_http_status"])
	}
}

func TestHandleHealth_Sealed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(503)
		json.NewEncoder(w).Encode(map[string]any{
			"initialized": true,
			"sealed":      true,
		})
	}))
	defer ts.Close()

	setVaultGlobals(t, ts.URL)

	result, err := handleHealth(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data := mustParseJSON(t, result)
	if data["_status"] != "sealed" {
		t.Fatalf("expected sealed status, got %q", data["_status"])
	}
}

// =====================================================================
// handleRead tests
// =====================================================================

func TestHandleRead_MissingPath(t *testing.T) {
	result, err := handleRead(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("expected nil error for validation failure, got: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected IsError=true for missing required path")
	}
}

func TestHandleRead_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/secret/data/myapp/config" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"data": map[string]any{
					"username": "admin",
					"password": "s3cret",
				},
				"metadata": map[string]any{
					"version":    1,
					"created_at": "2024-01-01T00:00:00Z",
				},
			},
		})
	}))
	defer ts.Close()

	setVaultGlobals(t, ts.URL)

	result, err := handleRead(context.Background(), map[string]any{
		"path": "myapp/config",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data := mustParseJSON(t, result)
	if data["path"] != "myapp/config" {
		t.Fatalf("expected path in response, got %v", data["path"])
	}
	if data["mount"] != "secret" {
		t.Fatalf("expected default mount=secret, got %v", data["mount"])
	}
	secretData, ok := data["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected data field, got %v", data["data"])
	}
	if secretData["username"] != "admin" {
		t.Fatalf("expected username=admin, got %v", secretData["username"])
	}
}

func TestHandleRead_CustomMount(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"data": map[string]any{}},
		})
	}))
	defer ts.Close()

	setVaultGlobals(t, ts.URL)

	_, err := handleRead(context.Background(), map[string]any{
		"mount": "kv",
		"path":  "app/db",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPath != "/v1/kv/data/app/db" {
		t.Fatalf("expected /v1/kv/data/app/db, got %q", gotPath)
	}
}

func TestHandleRead_WithVersion(t *testing.T) {
	var gotQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"data": map[string]any{}},
		})
	}))
	defer ts.Close()

	setVaultGlobals(t, ts.URL)

	_, err := handleRead(context.Background(), map[string]any{
		"path":    "myapp",
		"version": float64(3),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotQuery, "version=3") {
		t.Fatalf("expected version=3 in query, got %q", gotQuery)
	}
}

// =====================================================================
// handleList tests
// =====================================================================

func TestHandleList_Success(t *testing.T) {
	var gotMethod string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"keys": []string{"app1/", "app2/", "shared/"},
			},
		})
	}))
	defer ts.Close()

	setVaultGlobals(t, ts.URL)

	result, err := handleList(context.Background(), map[string]any{
		"path": "apps",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != "LIST" {
		t.Fatalf("expected LIST method, got %q", gotMethod)
	}

	data := mustParseJSON(t, result)
	if data["count"] != float64(3) {
		t.Fatalf("expected count=3, got %v", data["count"])
	}
}

// =====================================================================
// handleMounts tests
// =====================================================================

func TestHandleMounts_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"secret/": map[string]any{
					"type":        "kv",
					"description": "key/value secret storage",
					"accessor":    "kv_abc123",
				},
				"sys/": map[string]any{
					"type":        "system",
					"description": "system endpoints",
					"accessor":    "sys_abc123",
				},
			},
		})
	}))
	defer ts.Close()

	setVaultGlobals(t, ts.URL)

	result, err := handleMounts(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data := mustParseJSON(t, result)
	if data["count"] != float64(2) {
		t.Fatalf("expected count=2, got %v", data["count"])
	}
}

// =====================================================================
// handlePolicies tests
// =====================================================================

func TestHandlePolicies_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"keys": []string{"default", "root", "admin"},
			},
		})
	}))
	defer ts.Close()

	setVaultGlobals(t, ts.URL)

	result, err := handlePolicies(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data := mustParseJSON(t, result)
	if data["count"] != float64(3) {
		t.Fatalf("expected count=3, got %v", data["count"])
	}
}

// =====================================================================
// handlePolicyRead tests
// =====================================================================

func TestHandlePolicyRead_MissingName(t *testing.T) {
	result, err := handlePolicyRead(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("expected nil error for validation failure, got: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected IsError=true for missing required name")
	}
}

func TestHandlePolicyRead_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sys/policies/acl/admin" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"policy": `path "secret/*" { capabilities = ["read"] }`,
			},
		})
	}))
	defer ts.Close()

	setVaultGlobals(t, ts.URL)

	result, err := handlePolicyRead(context.Background(), map[string]any{
		"name": "admin",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data := mustParseJSON(t, result)
	if data["name"] != "admin" {
		t.Fatalf("expected name=admin, got %v", data["name"])
	}
	policy, ok := data["policy"].(string)
	if !ok || !strings.Contains(policy, "secret/*") {
		t.Fatalf("expected policy content, got %v", data["policy"])
	}
}

// =====================================================================
// handleMetadata tests
// =====================================================================

func TestHandleMetadata_MissingPath(t *testing.T) {
	result, err := handleMetadata(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("expected nil error for validation failure, got: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected IsError=true for missing required path")
	}
}

func TestHandleMetadata_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/secret/metadata/myapp" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"current_version": 3,
				"max_versions":    10,
			},
		})
	}))
	defer ts.Close()

	setVaultGlobals(t, ts.URL)

	result, err := handleMetadata(context.Background(), map[string]any{
		"path": "myapp",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data := mustParseJSON(t, result)
	metadata, ok := data["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("expected metadata field, got %v", data["metadata"])
	}
	if metadata["current_version"] != float64(3) {
		t.Fatalf("expected current_version=3, got %v", metadata["current_version"])
	}
}

// =====================================================================
// handleTokenLookup tests
// =====================================================================

func TestHandleTokenLookup_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"display_name": "root",
				"policies":     []string{"root"},
			},
		})
	}))
	defer ts.Close()

	setVaultGlobals(t, ts.URL)

	result, err := handleTokenLookup(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data := mustParseJSON(t, result)
	if data["display_name"] != "root" {
		t.Fatalf("expected display_name=root, got %v", data["display_name"])
	}
}

// =====================================================================
// handleAuthMethods tests
// =====================================================================

func TestHandleAuthMethods_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"token/": map[string]any{
					"type":        "token",
					"description": "token based credentials",
					"accessor":    "auth_token_abc",
				},
			},
		})
	}))
	defer ts.Close()

	setVaultGlobals(t, ts.URL)

	result, err := handleAuthMethods(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data := mustParseJSON(t, result)
	if data["count"] != float64(1) {
		t.Fatalf("expected count=1, got %v", data["count"])
	}
}
