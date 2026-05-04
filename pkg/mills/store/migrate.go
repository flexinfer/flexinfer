package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrationsDir is the embed prefix; kept as a constant so tests and
// out-of-package callers don't need to know the layout.
const migrationsDir = "migrations"

// Migrate applies any pending SQL migrations from the embedded migrations
// directory. Safe to call multiple times — it tracks applied versions in a
// schema_migrations table.
//
// Migration files use the convention `NNN_name.sql` where NNN is a positive
// integer applied in ascending order. Each file is executed as a single
// statement batch inside one transaction.
func Migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			name       TEXT NOT NULL,
			applied_at TEXT NOT NULL
		)`); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}

	files, err := loadMigrations()
	if err != nil {
		return err
	}

	applied, err := loadAppliedVersions(ctx, db)
	if err != nil {
		return err
	}

	for _, m := range files {
		if applied[m.version] {
			continue
		}
		if err := applyOne(ctx, db, m); err != nil {
			return err
		}
	}
	return nil
}

// MigrateDown rolls back nothing in v1: we only ship forward migrations and
// rely on backups for recovery. The function is kept as a stub so callers
// that exercise both directions in tests can resolve the symbol.
func MigrateDown(_ context.Context, _ *sql.DB) error {
	return errors.New("mills store: migrate-down is not implemented; restore from backup")
}

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationsFS, migrationsDir)
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}
	var out []migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		idx := strings.IndexByte(e.Name(), '_')
		if idx <= 0 {
			return nil, fmt.Errorf("migration filename %q must be NNN_name.sql", e.Name())
		}
		ver, err := strconv.Atoi(e.Name()[:idx])
		if err != nil {
			return nil, fmt.Errorf("migration version in %q: %w", e.Name(), err)
		}
		body, err := fs.ReadFile(migrationsFS, migrationsDir+"/"+e.Name())
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		out = append(out, migration{
			version: ver,
			name:    strings.TrimSuffix(e.Name()[idx+1:], ".sql"),
			sql:     stripDirectives(string(body)),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// stripDirectives extracts the UP section of a goose-flavored migration file
// and drops the goose marker lines. We keep the file format compatible with
// goose so anyone running goose-cli locally still works, but at runtime we
// only execute the UP body. Format:
//
//	-- +goose Up
//	-- +goose StatementBegin
//	<UP SQL>
//	-- +goose StatementEnd
//
//	-- +goose Down
//	-- +goose StatementBegin
//	<DOWN SQL>     // ignored at runtime
//	-- +goose StatementEnd
//
// Files with no markers are returned unchanged.
func stripDirectives(s string) string {
	const upMarker = "-- +goose Up"
	const downMarker = "-- +goose Down"

	if !strings.Contains(s, upMarker) && !strings.Contains(s, downMarker) {
		return s
	}

	inUp := false
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, upMarker):
			inUp = true
			continue
		case strings.HasPrefix(trimmed, downMarker):
			inUp = false
			continue
		case strings.HasPrefix(trimmed, "-- +goose"):
			// StatementBegin / StatementEnd / other goose directives.
			continue
		}
		if inUp {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func loadAppliedVersions(ctx context.Context, db *sql.DB) (map[int]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()
	out := make(map[int]bool)
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("scan version: %w", err)
		}
		out[v] = true
	}
	return out, rows.Err()
}

func applyOne(ctx context.Context, db *sql.DB, m migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %d: %w", m.version, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return fmt.Errorf("apply migration %03d_%s: %w", m.version, m.name, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
		m.version, m.name, time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		return fmt.Errorf("record migration %d: %w", m.version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %d: %w", m.version, err)
	}
	return nil
}
