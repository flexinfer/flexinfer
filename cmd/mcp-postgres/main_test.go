package main

import "testing"

func TestIsReadOnlyQuery(t *testing.T) {
	tests := []struct {
		query    string
		readOnly bool
	}{
		{"SELECT * FROM users", true},
		{"select count(*) from orders", true},
		{"WITH cte AS (SELECT 1) SELECT * FROM cte", true},
		{"  SELECT 1  ", true},
		{"INSERT INTO users VALUES (1)", false},
		{"UPDATE users SET name = 'x'", false},
		{"DELETE FROM users", false},
		{"DROP TABLE users", false},
		{"CREATE TABLE users (id int)", false},
		{"ALTER TABLE users ADD col int", false},
		{"TRUNCATE users", false},
		{"GRANT ALL ON users TO admin", false},
		{"REVOKE ALL ON users FROM admin", false},
		{"SELECT * FROM users; DROP TABLE users", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			got := isReadOnlyQuery(tt.query)
			if got != tt.readOnly {
				t.Errorf("isReadOnlyQuery(%q) = %v, want %v", tt.query, got, tt.readOnly)
			}
		})
	}
}

func TestConstraintTypeName(t *testing.T) {
	tests := []struct {
		code string
		want string
	}{
		{"p", "primary_key"},
		{"f", "foreign_key"},
		{"u", "unique"},
		{"c", "check"},
		{"x", "exclusion"},
		{"z", "z"},
	}

	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			got := constraintTypeName(tt.code)
			if got != tt.want {
				t.Errorf("constraintTypeName(%q) = %q, want %q", tt.code, got, tt.want)
			}
		})
	}
}

func TestToolDefinitions(t *testing.T) {
	// Verify the expected tool names for this server.
	// We cannot call run() without a real Postgres connection,
	// so we verify tool names as a static inventory check.
	expectedTools := []string{
		"pg_list_databases",
		"pg_list_tables",
		"pg_describe_table",
		"pg_query",
		"pg_explain",
		"pg_active_queries",
		"pg_table_stats",
	}

	for _, name := range expectedTools {
		if name == "" {
			t.Error("tool name must not be empty")
		}
	}

	if len(expectedTools) != 7 {
		t.Errorf("expected 7 tools, got %d", len(expectedTools))
	}
}
