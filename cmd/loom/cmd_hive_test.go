package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeOperator returns an httptest.Server emulating the loom-hive-operator
// /api/hive/status surface. Tests configure the body via the `status` arg;
// pass nil to simulate a 500.
func fakeOperator(t *testing.T, status int, body any) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/hive/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if body != nil {
			_ = json.NewEncoder(w).Encode(body)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestHiveStatus_HumanReadable(t *testing.T) {
	srv := fakeOperator(t, http.StatusOK, map[string]any{
		"db_ok":          true,
		"policy_enabled": true,
		"policy_version": 1,
		"slice":          "1.2-skeleton",
	})

	cmd := newHiveCmd()
	cmd.SetArgs([]string{"status", "--operator-url", srv.URL})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(context.Background())

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v\noutput=%q", err, out.String())
	}
	for _, want := range []string{"policy:", "(v1)", "store:", "ok", "queue depth:", "—", "operator slice:", "1.2-skeleton"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q\n%s", want, out.String())
		}
	}
}

func TestHiveStatus_RawJSON(t *testing.T) {
	srv := fakeOperator(t, http.StatusOK, map[string]any{
		"db_ok":          true,
		"policy_enabled": false,
		"policy_version": 1,
	})

	cmd := newHiveCmd()
	cmd.SetArgs([]string{"status", "--operator-url", srv.URL, "--json"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(context.Background())

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), `"db_ok":true`) {
		t.Errorf("expected raw JSON, got: %s", out.String())
	}
}

func TestHiveStatus_PolicyOff(t *testing.T) {
	srv := fakeOperator(t, http.StatusOK, map[string]any{
		"db_ok":          true,
		"policy_enabled": false,
		"policy_version": 2,
	})

	cmd := newHiveCmd()
	cmd.SetArgs([]string{"status", "--operator-url", srv.URL})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(context.Background())

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "policy:           off (v2)") {
		t.Errorf("expected policy off, got: %s", out.String())
	}
}

func TestHiveStatus_DBFailure(t *testing.T) {
	srv := fakeOperator(t, http.StatusOK, map[string]any{
		"db_ok":          false,
		"policy_enabled": true,
		"policy_version": 1,
	})

	cmd := newHiveCmd()
	cmd.SetArgs([]string{"status", "--operator-url", srv.URL})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(context.Background())

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "store:            FAIL") {
		t.Errorf("expected store=FAIL, got: %s", out.String())
	}
}

func TestHiveStatus_OperatorError(t *testing.T) {
	srv := fakeOperator(t, http.StatusInternalServerError, nil)

	cmd := newHiveCmd()
	cmd.SetArgs([]string{"status", "--operator-url", srv.URL})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(context.Background())

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error on 500 response")
	}
	if !strings.Contains(err.Error(), "operator returned 500") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestHiveStatus_ConnectionRefused(t *testing.T) {
	cmd := newHiveCmd()
	// 127.0.0.1:1 is a reserved port; nothing listens there.
	cmd.SetArgs([]string{"status", "--operator-url", "http://127.0.0.1:1", "--timeout", "200ms"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(context.Background())

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected connection error")
	}
	if !strings.Contains(err.Error(), "kubectl port-forward") {
		t.Errorf("expected port-forward hint, got: %v", err)
	}
}

func TestHiveStatus_HonorsEnv(t *testing.T) {
	srv := fakeOperator(t, http.StatusOK, map[string]any{
		"db_ok":          true,
		"policy_enabled": true,
		"policy_version": 1,
	})
	t.Setenv("LOOM_HIVE_OPERATOR_URL", srv.URL)

	cmd := newHiveCmd()
	cmd.SetArgs([]string{"status"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(context.Background())

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "Loom Hive @ "+srv.URL) {
		t.Errorf("env URL not applied; output: %s", out.String())
	}
}

func TestHiveStatus_AuthHeaderForwarded(t *testing.T) {
	var seen string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/hive/status", func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"db_ok":true,"policy_enabled":true,"policy_version":1}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	t.Setenv("LOOM_HIVE_TOKEN", "tok-abc")
	cmd := newHiveCmd()
	cmd.SetArgs([]string{"status", "--operator-url", srv.URL})
	cmd.SetContext(context.Background())
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if seen != "Bearer tok-abc" {
		t.Errorf("Authorization header: got %q want Bearer tok-abc", seen)
	}
}
