// mcp-postgres is an MCP server for PostgreSQL database operations.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcperror"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/validate"

	_ "github.com/lib/pq"
)

var version = "1.0.0"

type postgresServer struct {
	db           *sql.DB
	queryTimeout time.Duration
}

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()

	pgURL := os.Getenv("POSTGRES_URL")
	if pgURL == "" {
		return mcperror.NotConfigured("POSTGRES_URL", "set POSTGRES_URL environment variable")
	}

	queryTimeout := 30 * time.Second
	if t := os.Getenv("POSTGRES_QUERY_TIMEOUT"); t != "" {
		if d, err := time.ParseDuration(t); err == nil {
			queryTimeout = d
		}
	}

	db, err := sql.Open("postgres", pgURL)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer db.Close()

	// Configure connection pool
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Test connection
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	pg := &postgresServer{
		db:           db,
		queryTimeout: queryTimeout,
	}

	logger.Info("starting server", "name", "mcp-postgres", "version", version)

	server := mcp.NewServer("mcp-postgres", version)
	server.SetInstructions("PostgreSQL MCP server. Inspect schemas and run read-only queries.")

	// pg_list_databases
	server.AddTool(mcp.Tool{
		Name:        "pg_list_databases",
		Description: "List all databases on the server",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, pg.handleListDatabases)

	// pg_list_tables
	server.AddTool(mcp.Tool{
		Name:        "pg_list_tables",
		Description: "List tables in a schema",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"schema": map[string]any{
					"type":        "string",
					"description": "Schema name (default: public)",
				},
			},
		},
	}, pg.handleListTables)

	// pg_describe_table
	server.AddTool(mcp.Tool{
		Name:        "pg_describe_table",
		Description: "Describe table structure including columns, types, constraints, and indexes",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"table": map[string]any{
					"type":        "string",
					"description": "Table name",
				},
				"schema": map[string]any{
					"type":        "string",
					"description": "Schema name (default: public)",
				},
			},
			Required: []string{"table"},
		},
	}, pg.handleDescribeTable)

	// pg_query
	server.AddTool(mcp.Tool{
		Name:        "pg_query",
		Description: "Execute a read-only SQL query (SELECT statements only)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "SQL SELECT query to execute",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum rows to return (default: 100, max: 1000)",
				},
			},
			Required: []string{"query"},
		},
	}, pg.handleQuery)

	// pg_explain
	server.AddTool(mcp.Tool{
		Name:        "pg_explain",
		Description: "Get query execution plan",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "SQL query to explain",
				},
				"analyze": map[string]any{
					"type":        "boolean",
					"description": "Run EXPLAIN ANALYZE (actually executes the query)",
				},
				"format": map[string]any{
					"type":        "string",
					"description": "Output format: text, json, yaml (default: text)",
				},
			},
			Required: []string{"query"},
		},
	}, pg.handleExplain)

	// pg_active_queries
	server.AddTool(mcp.Tool{
		Name:        "pg_active_queries",
		Description: "Show currently running queries",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"min_duration": map[string]any{
					"type":        "string",
					"description": "Minimum query duration to show (e.g., '1s', '100ms')",
				},
			},
		},
	}, pg.handleActiveQueries)

	// pg_table_stats
	server.AddTool(mcp.Tool{
		Name:        "pg_table_stats",
		Description: "Get table statistics including row count, size, and bloat",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"table": map[string]any{
					"type":        "string",
					"description": "Table name",
				},
				"schema": map[string]any{
					"type":        "string",
					"description": "Schema name (default: public)",
				},
			},
			Required: []string{"table"},
		},
	}, pg.handleTableStats)

	return server.Run(ctx)
}

func (s *postgresServer) handleListDatabases(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()

	query := `
		SELECT datname, pg_database_size(datname) as size_bytes,
		       pg_size_pretty(pg_database_size(datname)) as size
		FROM pg_database
		WHERE datistemplate = false
		ORDER BY datname`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	defer rows.Close()

	var databases []map[string]any
	for rows.Next() {
		var name, size string
		var sizeBytes int64
		if err := rows.Scan(&name, &sizeBytes, &size); err != nil {
			return mcp.ErrorResult(err), nil
		}
		databases = append(databases, map[string]any{
			"name":       name,
			"size_bytes": sizeBytes,
			"size":       size,
		})
	}

	return mcp.JSONResult(map[string]any{
		"ok":        true,
		"count":     len(databases),
		"databases": databases,
	})
}

func (s *postgresServer) handleListTables(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()

	v := validate.NewArgs(args)
	schema := v.String("schema", "public")

	query := `
		SELECT table_name, table_type
		FROM information_schema.tables
		WHERE table_schema = $1
		ORDER BY table_name`

	rows, err := s.db.QueryContext(ctx, query, schema)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	defer rows.Close()

	var tables []map[string]any
	for rows.Next() {
		var name, tableType string
		if err := rows.Scan(&name, &tableType); err != nil {
			return mcp.ErrorResult(err), nil
		}
		tables = append(tables, map[string]any{
			"name": name,
			"type": tableType,
		})
	}

	return mcp.JSONResult(map[string]any{
		"ok":     true,
		"schema": schema,
		"count":  len(tables),
		"tables": tables,
	})
}

func (s *postgresServer) handleDescribeTable(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()

	v := validate.NewArgs(args)
	table := v.Required("table")
	schema := v.String("schema", "public")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Get columns
	colQuery := `
		SELECT column_name, data_type, is_nullable, column_default,
		       character_maximum_length, numeric_precision
		FROM information_schema.columns
		WHERE table_schema = $1 AND table_name = $2
		ORDER BY ordinal_position`

	rows, err := s.db.QueryContext(ctx, colQuery, schema, table)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	defer rows.Close()

	var columns []map[string]any
	for rows.Next() {
		var name, dataType, nullable string
		var defaultVal, maxLen, precision sql.NullString
		if err := rows.Scan(&name, &dataType, &nullable, &defaultVal, &maxLen, &precision); err != nil {
			return mcp.ErrorResult(err), nil
		}
		col := map[string]any{
			"name":     name,
			"type":     dataType,
			"nullable": nullable == "YES",
		}
		if defaultVal.Valid {
			col["default"] = defaultVal.String
		}
		if maxLen.Valid {
			col["max_length"] = maxLen.String
		}
		if precision.Valid {
			col["precision"] = precision.String
		}
		columns = append(columns, col)
	}

	// Get indexes
	idxQuery := `
		SELECT indexname, indexdef
		FROM pg_indexes
		WHERE schemaname = $1 AND tablename = $2`

	idxRows, err := s.db.QueryContext(ctx, idxQuery, schema, table)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	defer idxRows.Close()

	var indexes []map[string]any
	for idxRows.Next() {
		var name, definition string
		if err := idxRows.Scan(&name, &definition); err != nil {
			return mcp.ErrorResult(err), nil
		}
		indexes = append(indexes, map[string]any{
			"name":       name,
			"definition": definition,
		})
	}

	// Get constraints
	conQuery := `
		SELECT conname, contype,
		       pg_get_constraintdef(c.oid) as definition
		FROM pg_constraint c
		JOIN pg_class t ON c.conrelid = t.oid
		JOIN pg_namespace n ON t.relnamespace = n.oid
		WHERE n.nspname = $1 AND t.relname = $2`

	conRows, err := s.db.QueryContext(ctx, conQuery, schema, table)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	defer conRows.Close()

	var constraints []map[string]any
	for conRows.Next() {
		var name, conType, definition string
		if err := conRows.Scan(&name, &conType, &definition); err != nil {
			return mcp.ErrorResult(err), nil
		}
		constraints = append(constraints, map[string]any{
			"name":       name,
			"type":       constraintTypeName(conType),
			"definition": definition,
		})
	}

	return mcp.JSONResult(map[string]any{
		"ok":          true,
		"schema":      schema,
		"table":       table,
		"columns":     columns,
		"indexes":     indexes,
		"constraints": constraints,
	})
}

func constraintTypeName(code string) string {
	switch code {
	case "p":
		return "primary_key"
	case "f":
		return "foreign_key"
	case "u":
		return "unique"
	case "c":
		return "check"
	case "x":
		return "exclusion"
	default:
		return code
	}
}

// isReadOnlyQuery checks if a query is read-only (SELECT only)
func isReadOnlyQuery(query string) bool {
	// Normalize query
	q := strings.TrimSpace(strings.ToUpper(query))

	// Must start with SELECT or WITH (for CTEs)
	if !strings.HasPrefix(q, "SELECT") && !strings.HasPrefix(q, "WITH") {
		return false
	}

	// Check for dangerous keywords
	dangerous := regexp.MustCompile(`(?i)\b(INSERT|UPDATE|DELETE|DROP|CREATE|ALTER|TRUNCATE|GRANT|REVOKE|EXECUTE|CALL)\b`)
	return !dangerous.MatchString(query)
}

func (s *postgresServer) handleQuery(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()

	v := validate.NewArgs(args)
	query := v.Required("query")
	limit := v.IntRange("limit", 100, 1, 1000)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Validate read-only
	if !isReadOnlyQuery(query) {
		return mcp.ErrorResult(fmt.Errorf("only SELECT queries are allowed")), nil
	}

	// Add LIMIT if not present
	upperQuery := strings.ToUpper(query)
	if !strings.Contains(upperQuery, "LIMIT") {
		query = fmt.Sprintf("%s LIMIT %d", strings.TrimSuffix(query, ";"), limit)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	var results []map[string]any
	for rows.Next() {
		values := make([]any, len(columns))
		valuePtrs := make([]any, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return mcp.ErrorResult(err), nil
		}

		row := make(map[string]any)
		for i, col := range columns {
			val := values[i]
			// Convert []byte to string
			if b, ok := val.([]byte); ok {
				row[col] = string(b)
			} else {
				row[col] = val
			}
		}
		results = append(results, row)
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"columns": columns,
		"count":   len(results),
		"rows":    results,
	})
}

func (s *postgresServer) handleExplain(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()

	v := validate.NewArgs(args)
	query := v.Required("query")
	analyze := v.Bool("analyze", false)
	format := v.Enum("format", "text", "text", "json", "yaml")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// For ANALYZE, still validate read-only
	if analyze && !isReadOnlyQuery(query) {
		return mcp.ErrorResult(fmt.Errorf("EXPLAIN ANALYZE only allowed for SELECT queries")), nil
	}

	explainQuery := "EXPLAIN"
	if analyze {
		explainQuery += " ANALYZE"
	}
	explainQuery += fmt.Sprintf(" (FORMAT %s) %s", format, query)

	rows, err := s.db.QueryContext(ctx, explainQuery)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	defer rows.Close()

	var plan []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return mcp.ErrorResult(err), nil
		}
		plan = append(plan, line)
	}

	return mcp.JSONResult(map[string]any{
		"ok":     true,
		"format": format,
		"plan":   strings.Join(plan, "\n"),
	})
}

func (s *postgresServer) handleActiveQueries(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()

	v := validate.NewArgs(args)
	minDuration := v.String("min_duration", "0s")

	// Parse duration
	dur, err := time.ParseDuration(minDuration)
	if err != nil {
		dur = 0
	}

	query := `
		SELECT pid, usename, application_name, client_addr,
		       state, query, now() - query_start as duration,
		       wait_event_type, wait_event
		FROM pg_stat_activity
		WHERE state != 'idle'
		  AND pid != pg_backend_pid()
		  AND query NOT LIKE '%pg_stat_activity%'
		  AND now() - query_start > $1::interval
		ORDER BY duration DESC`

	rows, err := s.db.QueryContext(ctx, query, dur.String())
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	defer rows.Close()

	var queries []map[string]any
	for rows.Next() {
		var pid int
		var user, app, state, queryText string
		var clientAddr, waitType, waitEvent sql.NullString
		var duration string

		if err := rows.Scan(&pid, &user, &app, &clientAddr, &state, &queryText, &duration, &waitType, &waitEvent); err != nil {
			return mcp.ErrorResult(err), nil
		}

		q := map[string]any{
			"pid":         pid,
			"user":        user,
			"application": app,
			"state":       state,
			"query":       queryText,
			"duration":    duration,
		}
		if clientAddr.Valid {
			q["client_addr"] = clientAddr.String
		}
		if waitType.Valid {
			q["wait_type"] = waitType.String
		}
		if waitEvent.Valid {
			q["wait_event"] = waitEvent.String
		}
		queries = append(queries, q)
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"count":   len(queries),
		"queries": queries,
	})
}

func (s *postgresServer) handleTableStats(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()

	v := validate.NewArgs(args)
	table := v.Required("table")
	schema := v.String("schema", "public")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	query := `
		SELECT
			pg_total_relation_size(c.oid) as total_bytes,
			pg_size_pretty(pg_total_relation_size(c.oid)) as total_size,
			pg_relation_size(c.oid) as table_bytes,
			pg_size_pretty(pg_relation_size(c.oid)) as table_size,
			pg_indexes_size(c.oid) as index_bytes,
			pg_size_pretty(pg_indexes_size(c.oid)) as index_size,
			COALESCE(s.n_live_tup, 0) as row_estimate,
			COALESCE(s.n_dead_tup, 0) as dead_tuples,
			COALESCE(s.last_vacuum, '1970-01-01'::timestamp) as last_vacuum,
			COALESCE(s.last_autovacuum, '1970-01-01'::timestamp) as last_autovacuum,
			COALESCE(s.last_analyze, '1970-01-01'::timestamp) as last_analyze
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN pg_stat_user_tables s ON s.relid = c.oid
		WHERE n.nspname = $1 AND c.relname = $2`

	var totalBytes, tableBytes, indexBytes, rowEstimate, deadTuples int64
	var totalSize, tableSize, indexSize string
	var lastVacuum, lastAutovacuum, lastAnalyze time.Time

	err := s.db.QueryRowContext(ctx, query, schema, table).Scan(
		&totalBytes, &totalSize, &tableBytes, &tableSize,
		&indexBytes, &indexSize, &rowEstimate, &deadTuples,
		&lastVacuum, &lastAutovacuum, &lastAnalyze,
	)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Calculate bloat ratio
	var bloatRatio float64
	if rowEstimate > 0 {
		bloatRatio = float64(deadTuples) / float64(rowEstimate) * 100
	}

	result := map[string]any{
		"ok":              true,
		"schema":          schema,
		"table":           table,
		"total_bytes":     totalBytes,
		"total_size":      totalSize,
		"table_bytes":     tableBytes,
		"table_size":      tableSize,
		"index_bytes":     indexBytes,
		"index_size":      indexSize,
		"row_estimate":    rowEstimate,
		"dead_tuples":     deadTuples,
		"bloat_ratio_pct": fmt.Sprintf("%.2f", bloatRatio),
	}

	// Only include timestamps if they're meaningful
	epoch := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	if lastVacuum.After(epoch) {
		result["last_vacuum"] = lastVacuum.Format(time.RFC3339)
	}
	if lastAutovacuum.After(epoch) {
		result["last_autovacuum"] = lastAutovacuum.Format(time.RFC3339)
	}
	if lastAnalyze.After(epoch) {
		result["last_analyze"] = lastAnalyze.Format(time.RFC3339)
	}

	return mcp.JSONResult(result)
}
