package httpclient

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	// Save and restore env
	origSkip := os.Getenv("TLS_SKIP_VERIFY")
	origTimeout := os.Getenv("HTTP_TIMEOUT")
	origRetries := os.Getenv("HTTP_RETRIES")
	defer func() {
		os.Setenv("TLS_SKIP_VERIFY", origSkip)
		os.Setenv("HTTP_TIMEOUT", origTimeout)
		os.Setenv("HTTP_RETRIES", origRetries)
	}()

	t.Run("defaults", func(t *testing.T) {
		os.Unsetenv("TLS_SKIP_VERIFY")
		os.Unsetenv("HTTP_TIMEOUT")
		os.Unsetenv("HTTP_RETRIES")

		cfg := DefaultConfig()
		if cfg.Timeout != 30*time.Second {
			t.Errorf("Timeout = %v, want 30s", cfg.Timeout)
		}
		if cfg.TLSSkipVerify {
			t.Error("TLSSkipVerify should be false by default")
		}
		if cfg.MaxRetries != 0 {
			t.Errorf("MaxRetries = %d, want 0", cfg.MaxRetries)
		}
	})

	t.Run("env overrides", func(t *testing.T) {
		os.Setenv("TLS_SKIP_VERIFY", "true")
		os.Setenv("HTTP_TIMEOUT", "60")
		os.Setenv("HTTP_RETRIES", "3")

		cfg := DefaultConfig()
		if !cfg.TLSSkipVerify {
			t.Error("TLSSkipVerify should be true")
		}
		if cfg.Timeout != 60*time.Second {
			t.Errorf("Timeout = %v, want 60s", cfg.Timeout)
		}
		if cfg.MaxRetries != 3 {
			t.Errorf("MaxRetries = %d, want 3", cfg.MaxRetries)
		}
	})

	t.Run("TLS skip variations", func(t *testing.T) {
		for _, val := range []string{"true", "1", "yes", "TRUE", "Yes"} {
			os.Setenv("TLS_SKIP_VERIFY", val)
			cfg := DefaultConfig()
			if !cfg.TLSSkipVerify {
				t.Errorf("TLSSkipVerify should be true for %q", val)
			}
		}

		for _, val := range []string{"false", "0", "no", "invalid"} {
			os.Setenv("TLS_SKIP_VERIFY", val)
			cfg := DefaultConfig()
			if cfg.TLSSkipVerify {
				t.Errorf("TLSSkipVerify should be false for %q", val)
			}
		}
	})

	t.Run("invalid env values", func(t *testing.T) {
		os.Setenv("HTTP_TIMEOUT", "invalid")
		os.Setenv("HTTP_RETRIES", "invalid")

		cfg := DefaultConfig()
		if cfg.Timeout != 30*time.Second {
			t.Errorf("Timeout should fall back to default")
		}
		if cfg.MaxRetries != 0 {
			t.Errorf("MaxRetries should fall back to default")
		}
	})
}

func TestNew(t *testing.T) {
	cfg := Config{
		Timeout:       10 * time.Second,
		TLSSkipVerify: true,
	}

	client := New(cfg)
	if client == nil {
		t.Fatal("expected non-nil client")
		return
	}
	if client.http == nil {
		t.Error("expected http.Client to be set")
	}
}

func TestNewDefault(t *testing.T) {
	client := NewDefault()
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestClient_Do(t *testing.T) {
	t.Run("successful request", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("success"))
		}))
		defer server.Close()

		client := New(Config{Timeout: 5 * time.Second})
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
		}
	})

	t.Run("retry on 5xx", func(t *testing.T) {
		attempts := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts++
			if attempts < 3 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := New(Config{
			Timeout:        5 * time.Second,
			MaxRetries:     3,
			RetryBaseDelay: 1 * time.Millisecond,
			RetryMaxDelay:  10 * time.Millisecond,
		})
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		defer resp.Body.Close()

		if attempts != 3 {
			t.Errorf("attempts = %d, want 3", attempts)
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
		}
	})

	t.Run("retry on 5xx rewinds request body", func(t *testing.T) {
		attempts := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts++
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if string(body) != `{"hello":"world"}` {
				t.Fatalf("body = %q", string(body))
			}
			if attempts == 1 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := New(Config{
			Timeout:        5 * time.Second,
			MaxRetries:     1,
			RetryBaseDelay: 1 * time.Millisecond,
			RetryMaxDelay:  10 * time.Millisecond,
		})
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL, bytes.NewBufferString(`{"hello":"world"}`))
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		defer resp.Body.Close()

		if attempts != 2 {
			t.Fatalf("attempts = %d, want 2", attempts)
		}
	})

	t.Run("5xx returns response on last attempt", func(t *testing.T) {
		// When all retries are exhausted with 5xx, the last response is returned
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		client := New(Config{
			Timeout:        5 * time.Second,
			MaxRetries:     2,
			RetryBaseDelay: 1 * time.Millisecond,
		})
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		defer resp.Body.Close()

		// On final attempt, response is returned even for 5xx
		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("StatusCode = %d, want 500", resp.StatusCode)
		}
	})

	t.Run("max retries exceeded on connection error", func(t *testing.T) {
		// Use an address that will fail to connect
		client := New(Config{
			Timeout:        100 * time.Millisecond,
			MaxRetries:     1,
			RetryBaseDelay: 1 * time.Millisecond,
		})
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://127.0.0.1:1", nil)
		resp, err := client.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		if err == nil {
			t.Error("expected error for connection failure")
			return
		}
		// Should get either connection refused or max retries exceeded
		errStr := err.Error()
		if !strings.Contains(errStr, "refused") && !strings.Contains(errStr, "max retries") {
			t.Errorf("error = %v, want connection error", err)
		}
	})

	t.Run("context cancellation", func(t *testing.T) {
		// Server that delays response to allow cancellation
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(200 * time.Millisecond)
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		client := New(Config{
			Timeout:        5 * time.Second,
			MaxRetries:     10,
			RetryBaseDelay: 100 * time.Millisecond,
		})

		ctx, cancel := context.WithCancel(context.Background())
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)

		// Cancel quickly before server responds
		go func() {
			time.Sleep(20 * time.Millisecond)
			cancel()
		}()

		resp, err := client.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		if err == nil {
			t.Error("expected error for cancelled context")
		}
	})
}

func TestClient_Get(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("Method = %s, want GET", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := New(Config{Timeout: 5 * time.Second})
	resp, err := client.Get(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
}

func TestClient_GetJSON(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"key":"value"}`))
		}))
		defer server.Close()

		client := New(Config{Timeout: 5 * time.Second})
		body, err := client.GetJSON(context.Background(), server.URL)
		if err != nil {
			t.Fatalf("GetJSON() error = %v", err)
		}
		if string(body) != `{"key":"value"}` {
			t.Errorf("body = %s, want {\"key\":\"value\"}", body)
		}
	})

	t.Run("error status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("not found"))
		}))
		defer server.Close()

		client := New(Config{Timeout: 5 * time.Second})
		_, err := client.GetJSON(context.Background(), server.URL)
		if err == nil {
			t.Error("expected error for 404 status")
		}
		if !strings.Contains(err.Error(), "HTTP 404") {
			t.Errorf("error = %v, want HTTP 404", err)
		}
	})
}

func TestClient_Post(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("Method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %s, want application/json", ct)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"test":true}` {
			t.Errorf("body = %s", body)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	client := New(Config{Timeout: 5 * time.Second})
	resp, err := client.Post(context.Background(), server.URL, "application/json", strings.NewReader(`{"test":true}`))
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Errorf("StatusCode = %d, want 201", resp.StatusCode)
	}
}

func TestClient_SetHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "Bearer token" {
			t.Errorf("Authorization = %q, want 'Bearer token'", auth)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := New(Config{Timeout: 5 * time.Second})
	client.SetHeader("Authorization", "Bearer token")

	resp, err := client.Get(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	defer resp.Body.Close()
}

func TestClient_HTTP(t *testing.T) {
	client := New(Config{Timeout: 5 * time.Second})
	if client.HTTP() == nil {
		t.Error("HTTP() returned nil")
	}
}

func TestBackoffDelay(t *testing.T) {
	client := New(Config{
		RetryBaseDelay: 100 * time.Millisecond,
		RetryMaxDelay:  1 * time.Second,
	})

	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 100 * time.Millisecond},
		{2, 200 * time.Millisecond},
		{3, 400 * time.Millisecond},
		{4, 800 * time.Millisecond},
		{5, 1 * time.Second}, // capped at max
		{10, 1 * time.Second},
	}

	for _, tt := range tests {
		got := client.backoffDelay(tt.attempt)
		if got != tt.want {
			t.Errorf("backoffDelay(%d) = %v, want %v", tt.attempt, got, tt.want)
		}
	}
}

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("connection refused"), true},
		{errors.New("connection reset by peer"), true},
		{errors.New("timeout exceeded"), true},
		{errors.New("unexpected EOF"), true},
		{errors.New("some other error"), false},
	}

	for _, tt := range tests {
		got := isRetryableError(tt.err)
		if got != tt.want {
			t.Errorf("isRetryableError(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

func TestHeaderTransport(t *testing.T) {
	t.Run("adds header", func(t *testing.T) {
		transport := &headerTransport{
			base:  http.DefaultTransport,
			key:   "X-Custom",
			value: "test",
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("X-Custom"); got != "test" {
				t.Errorf("X-Custom = %q, want 'test'", got)
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := &http.Client{Transport: transport}
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		resp.Body.Close()
	})

	t.Run("does not override existing", func(t *testing.T) {
		transport := &headerTransport{
			base:  http.DefaultTransport,
			key:   "X-Custom",
			value: "default",
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("X-Custom"); got != "override" {
				t.Errorf("X-Custom = %q, want 'override'", got)
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := &http.Client{Transport: transport}
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
		req.Header.Set("X-Custom", "override")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		resp.Body.Close()
	})

	t.Run("nil base uses default", func(t *testing.T) {
		transport := &headerTransport{
			base:  nil,
			key:   "X-Test",
			value: "value",
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
		resp, err := transport.RoundTrip(req)
		if err != nil {
			t.Fatalf("RoundTrip() error = %v", err)
		}
		resp.Body.Close()
	})
}

func TestReadBodyWithLimit(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		maxBytes      int
		wantBody      string
		wantTruncated bool
	}{
		{
			name:          "within limit",
			input:         "hello",
			maxBytes:      10,
			wantBody:      "hello",
			wantTruncated: false,
		},
		{
			name:          "exceeds limit",
			input:         "hello world, this is a long message",
			maxBytes:      10,
			wantBody:      "hello worl",
			wantTruncated: true,
		},
		{
			name:          "no limit with zero",
			input:         "hello world",
			maxBytes:      0,
			wantBody:      "hello world",
			wantTruncated: false,
		},
		{
			name:          "no limit with negative",
			input:         "hello world",
			maxBytes:      -1,
			wantBody:      "hello world",
			wantTruncated: false,
		},
		{
			name:          "empty reader",
			input:         "",
			maxBytes:      10,
			wantBody:      "",
			wantTruncated: false,
		},
		{
			name:          "exact limit",
			input:         "12345",
			maxBytes:      5,
			wantBody:      "12345",
			wantTruncated: false,
		},
		{
			name:          "one byte over limit",
			input:         "123456",
			maxBytes:      5,
			wantBody:      "12345",
			wantTruncated: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := strings.NewReader(tt.input)
			got, truncated, err := ReadBodyWithLimit(r, tt.maxBytes)
			if err != nil {
				t.Fatalf("ReadBodyWithLimit() error = %v", err)
			}
			if truncated != tt.wantTruncated {
				t.Errorf("ReadBodyWithLimit() truncated = %v, want %v", truncated, tt.wantTruncated)
			}
			if string(got) != tt.wantBody {
				t.Errorf("ReadBodyWithLimit() body = %q, want %q", string(got), tt.wantBody)
			}
		})
	}
}
