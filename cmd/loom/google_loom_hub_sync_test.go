package main

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestUpsertStringDataYAMLPreservesAndAppendsGoogleKeys(t *testing.T) {
	input := []byte(`apiVersion: v1
kind: Secret
metadata:
    name: loom-secrets
stringData:
    EXISTING_KEY: keep-me
    GOOGLE_WORKSPACE_CLIENT_ID: old-client
`)

	values := map[string]string{
		"GOOGLE_WORKSPACE_CLIENT_ID":     "new-client",
		"GOOGLE_WORKSPACE_CLIENT_SECRET": "secret",
		"GOOGLE_WORKSPACE_REFRESH_TOKEN": "",
		"GOOGLE_WORKSPACE_SCOPES":        "openid,email",
		"GOOGLE_WORKSPACE_ACCOUNT_EMAIL": "cody.r.blevins@gmail.com",
	}

	updated, changed, err := upsertStringDataYAML(input, values, googleWorkspaceSecretKeys)
	if err != nil {
		t.Fatalf("upsertStringDataYAML returned error: %v", err)
	}
	if !changed {
		t.Fatal("expected yaml update to report a change")
	}

	text := string(updated)
	for _, want := range []string{
		"EXISTING_KEY: keep-me",
		"GOOGLE_WORKSPACE_CLIENT_ID: new-client",
		"GOOGLE_WORKSPACE_CLIENT_SECRET: secret",
		`GOOGLE_WORKSPACE_REFRESH_TOKEN: ""`,
		"GOOGLE_WORKSPACE_SCOPES: openid,email",
		"GOOGLE_WORKSPACE_ACCOUNT_EMAIL: cody.r.blevins@gmail.com",
	} {
		if !containsLine(text, want) {
			t.Fatalf("updated yaml missing %q:\n%s", want, text)
		}
	}
}

func TestUpsertStringDataYAMLNoChange(t *testing.T) {
	input := []byte(`apiVersion: v1
kind: Secret
stringData:
    GOOGLE_WORKSPACE_CLIENT_ID: same
    GOOGLE_WORKSPACE_CLIENT_SECRET: same-secret
    GOOGLE_WORKSPACE_REFRESH_TOKEN: ""
    GOOGLE_WORKSPACE_SCOPES: openid,email
    GOOGLE_WORKSPACE_ACCOUNT_EMAIL: cody.r.blevins@gmail.com
`)
	values := map[string]string{
		"GOOGLE_WORKSPACE_CLIENT_ID":     "same",
		"GOOGLE_WORKSPACE_CLIENT_SECRET": "same-secret",
		"GOOGLE_WORKSPACE_REFRESH_TOKEN": "",
		"GOOGLE_WORKSPACE_SCOPES":        "openid,email",
		"GOOGLE_WORKSPACE_ACCOUNT_EMAIL": "cody.r.blevins@gmail.com",
	}

	updated, changed, err := upsertStringDataYAML(input, values, googleWorkspaceSecretKeys)
	if err != nil {
		t.Fatalf("upsertStringDataYAML returned error: %v", err)
	}
	if changed {
		t.Fatal("expected yaml update to report no change")
	}
	if updated != nil {
		t.Fatalf("expected nil updated bytes when unchanged, got %q", string(updated))
	}
}

func TestSameStringValues(t *testing.T) {
	left := map[string]string{"A": "1", "B": "2"}
	right := map[string]string{"A": "1", "B": "2"}
	if !sameStringValues(left, right, []string{"A", "B"}) {
		t.Fatal("expected maps to match")
	}
	right["B"] = "3"
	if sameStringValues(left, right, []string{"A", "B"}) {
		t.Fatal("expected maps to differ")
	}
}

func TestGoogleWorkspaceSecretPatchEncoding(t *testing.T) {
	values := map[string]string{
		"GOOGLE_WORKSPACE_CLIENT_ID":     "client",
		"GOOGLE_WORKSPACE_CLIENT_SECRET": "secret",
		"GOOGLE_WORKSPACE_REFRESH_TOKEN": "",
		"GOOGLE_WORKSPACE_SCOPES":        "openid,email",
		"GOOGLE_WORKSPACE_ACCOUNT_EMAIL": "cody.r.blevins@gmail.com",
	}

	data := make(map[string]string, len(googleWorkspaceSecretKeys))
	for _, key := range googleWorkspaceSecretKeys {
		data[key] = base64.StdEncoding.EncodeToString([]byte(values[key]))
	}
	body, err := json.Marshal(map[string]any{"data": data})
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}

	var patch struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(body, &patch); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	for key, want := range values {
		got, err := base64.StdEncoding.DecodeString(patch.Data[key])
		if err != nil {
			t.Fatalf("DecodeString(%s) returned error: %v", key, err)
		}
		if string(got) != want {
			t.Fatalf("decoded %s = %q, want %q", key, string(got), want)
		}
	}
}

func containsLine(text, want string) bool {
	for _, line := range strings.Split(text, "\n") {
		if line == "    "+want || line == want {
			return true
		}
	}
	return false
}
