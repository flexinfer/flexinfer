package main

import (
	"context"
	"testing"
)

// ---------------------------------------------------------------------------
// Pure function tests
// ---------------------------------------------------------------------------

func TestParseRedisInfo(t *testing.T) {
	info := "# Server\nredis_version:7.2.0\nredis_git_sha1:00000000\n\n# Clients\nconnected_clients:5\nblocked_clients:0\n"
	result := parseRedisInfo(info)

	if result["Server"]["redis_version"] != "7.2.0" {
		t.Errorf("redis_version = %q, want 7.2.0", result["Server"]["redis_version"])
	}
	if result["Clients"]["connected_clients"] != "5" {
		t.Errorf("connected_clients = %q, want 5", result["Clients"]["connected_clients"])
	}
}

func TestParseRedisInfo_Empty(t *testing.T) {
	result := parseRedisInfo("")
	if len(result) != 0 {
		t.Errorf("expected empty map, got %d sections", len(result))
	}
}

func TestParseRedisInfo_NoSections(t *testing.T) {
	// Lines without a section header are ignored.
	result := parseRedisInfo("some_key:some_value\n")
	if len(result) != 0 {
		t.Errorf("expected empty map for sectionless data, got %d", len(result))
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatBytes(tt.bytes)
			if got != tt.want {
				t.Errorf("formatBytes(%d) = %q, want %q", tt.bytes, got, tt.want)
			}
		})
	}
}

func TestClientTypeFlag(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"normal", "N"},
		{"master", "M"},
		{"replica", "S"},
		{"slave", "S"},
		{"pubsub", "P"},
		{"unknown", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := clientTypeFlag(tt.input); got != tt.want {
				t.Errorf("clientTypeFlag(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseClientList(t *testing.T) {
	t.Run("single client", func(t *testing.T) {
		list := "id=1 addr=127.0.0.1:12345 fd=5 name=myapp db=0 flags=N\n"
		clients := parseClientList(list)
		if len(clients) != 1 {
			t.Fatalf("expected 1 client, got %d", len(clients))
		}
		if clients[0]["id"] != "1" {
			t.Errorf("id = %q, want 1", clients[0]["id"])
		}
		if clients[0]["addr"] != "127.0.0.1:12345" {
			t.Errorf("addr = %q", clients[0]["addr"])
		}
		if clients[0]["flags"] != "N" {
			t.Errorf("flags = %q, want N", clients[0]["flags"])
		}
	})

	t.Run("multiple clients", func(t *testing.T) {
		list := "id=1 addr=127.0.0.1:1111 flags=N\nid=2 addr=127.0.0.1:2222 flags=S\n"
		clients := parseClientList(list)
		if len(clients) != 2 {
			t.Fatalf("expected 2 clients, got %d", len(clients))
		}
	})

	t.Run("empty", func(t *testing.T) {
		clients := parseClientList("")
		if len(clients) != 0 {
			t.Errorf("expected 0 clients, got %d", len(clients))
		}
	})

	t.Run("whitespace lines", func(t *testing.T) {
		clients := parseClientList("  \n  \n")
		if len(clients) != 0 {
			t.Errorf("expected 0 clients, got %d", len(clients))
		}
	})
}

// ---------------------------------------------------------------------------
// Validation error tests (require a redisServer but no live Redis)
// ---------------------------------------------------------------------------

func TestHandleGet_MissingKey(t *testing.T) {
	rs := &redisServer{} // nil client is fine -- validation fails first
	result, err := rs.handleGet(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for missing key")
	}
}

func TestHandleTTL_MissingKey(t *testing.T) {
	rs := &redisServer{}
	result, err := rs.handleTTL(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for missing key")
	}
}
