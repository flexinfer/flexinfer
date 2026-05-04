// Package store is the canonical persistence layer for the Loom Mills.
//
// All mills state — backlog items, council runs, pipeline runs, stage and gate
// outcomes, KPI snapshots, evaluation scores, and a generic event log — lives
// in a single SQLite database with WAL journaling. This package owns the
// schema and exposes typed DAOs; nothing outside the mills package tree should
// open the DB directly.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registered as "sqlite"
)

// Store wraps the SQLite database handle and exposes typed DAOs.
type Store struct {
	db *sql.DB

	Backlog  *BacklogDAO
	Council  *CouncilDAO
	Pipeline *PipelineDAO
	KPI      *KPIDAO
	Eval     *EvalDAO
	Events   *EventDAO
	Roadmap  *RoadmapDAO

	// Mills v2 — Hierarchical Swarm DAOs.
	Squads          *SquadDAO
	Audit           *AuditDAO
	CrossRepo       *CrossRepoDAO
	Debate          *DebateDAO
	PolicyProposals *PolicyProposalDAO
}

// Options controls Store creation.
type Options struct {
	// Path is the filesystem path to the SQLite database. Use ":memory:" for
	// in-process tests.
	Path string

	// SkipMigrations omits the goose migration step. Useful for tests that
	// want to inspect a known-empty file.
	SkipMigrations bool
}

// Open opens (or creates) the SQLite database at opts.Path, sets the required
// PRAGMAs, and applies pending migrations.
func Open(ctx context.Context, opts Options) (*Store, error) {
	if opts.Path == "" {
		return nil, errors.New("store: Options.Path must not be empty")
	}

	// PRAGMAs are embedded in the DSN so every connection in the pool inherits
	// them. ExecContext-only PRAGMAs would only stick on one pooled connection,
	// which causes the next acquired conn to lack busy_timeout / foreign_keys
	// and hit SQLITE_BUSY under concurrent writers.
	dsn := buildDSN(opts.Path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open sqlite: %w", err)
	}

	// One writer + many readers under WAL. busy_timeout retries inside the
	// driver, so concurrent writers serialise without surfacing SQLITE_BUSY.
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(time.Hour)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}

	if !opts.SkipMigrations {
		if err := Migrate(ctx, db); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("store: migrate: %w", err)
		}
	}

	s := &Store{db: db}
	s.Backlog = &BacklogDAO{db: db}
	s.Council = &CouncilDAO{db: db}
	s.Pipeline = &PipelineDAO{db: db}
	s.KPI = &KPIDAO{db: db}
	s.Eval = &EvalDAO{db: db}
	s.Events = &EventDAO{db: db}
	s.Roadmap = &RoadmapDAO{db: db}

	// Mills v2.
	s.Squads = &SquadDAO{db: db}
	s.Audit = &AuditDAO{db: db}
	s.CrossRepo = &CrossRepoDAO{db: db}
	s.Debate = &DebateDAO{db: db}
	s.PolicyProposals = &PolicyProposalDAO{db: db}
	return s, nil
}

// Close releases the underlying database handle.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// DB exposes the raw handle for advanced callers (migrations, ad-hoc reads).
// Most code should use the typed DAOs.
func (s *Store) DB() *sql.DB {
	return s.db
}

// buildDSN composes a modernc.org/sqlite DSN with the PRAGMAs every mills
// connection needs:
//   - journal_mode=WAL: durable + concurrent-read friendly.
//   - synchronous=NORMAL: safe under WAL with negligible durability cost.
//   - foreign_keys=ON: enforce REFERENCES; off-by-default in SQLite.
//   - busy_timeout=5000: in-driver retry on SQLITE_BUSY for up to 5s.
//
// The driver evaluates `_pragma=` query params on every new pooled connection,
// so each acquired conn arrives with the right settings.
func buildDSN(path string) string {
	pragmas := []string{
		"journal_mode(WAL)",
		"synchronous(NORMAL)",
		"foreign_keys(ON)",
		"busy_timeout(5000)",
	}
	q := url.Values{}
	for _, p := range pragmas {
		q.Add("_pragma", p)
	}
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return path + sep + q.Encode()
}
