// Package store persists cached fetches, source health, and (later) tracker
// state in SQLite via the pure-Go modernc.org/sqlite driver.
package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	_ "modernc.org/sqlite"

	"github.com/itxkarthik/jobhunt/internal/model"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the DB at path and runs migrations.
// Use ":memory:" in tests.
func Open(path string) (*Store, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	if path == ":memory:" {
		dsn = "file::memory:?_pragma=foreign_keys(1)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if path == ":memory:" {
		db.SetMaxOpenConns(1) // each connection would get its own empty DB
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// migrate applies migrations/NNN_*.sql in order, tracked via PRAGMA user_version.
func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for i, name := range names {
		n := i + 1
		if n <= version {
			continue
		}
		b, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(b)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", n)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// CachedJobs returns jobs cached for (source, key) if fetched within ttl.
func (s *Store) CachedJobs(ctx context.Context, source, key string, ttl time.Duration) ([]model.Job, time.Time, bool, error) {
	var at int64
	var blob []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT fetched_at, jobs_json FROM fetch_cache WHERE source = ? AND query_key = ?`,
		source, key).Scan(&at, &blob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, false, nil
	}
	if err != nil {
		return nil, time.Time{}, false, err
	}
	fetched := time.Unix(at, 0)
	if time.Since(fetched) > ttl {
		return nil, fetched, false, nil
	}
	var jobs []model.Job
	if err := json.Unmarshal(blob, &jobs); err != nil {
		return nil, fetched, false, nil // treat a corrupt entry as a miss
	}
	return jobs, fetched, true, nil
}

func (s *Store) PutCache(ctx context.Context, source, key string, jobs []model.Job) error {
	blob, err := json.Marshal(jobs)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO fetch_cache (source, query_key, fetched_at, jobs_json) VALUES (?, ?, ?, ?)
		 ON CONFLICT (source, query_key) DO UPDATE SET fetched_at = excluded.fetched_at, jobs_json = excluded.jobs_json`,
		source, key, time.Now().Unix(), blob)
	return err
}

// SourceRun is one recorded fetch attempt.
type SourceRun struct {
	Source   string
	RunAt    time.Time
	Status   string
	Count    int
	Error    string
	Duration time.Duration
}

func (s *Store) RecordRun(ctx context.Context, r SourceRun) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO source_runs (source, run_at, status, count, error, duration_ms) VALUES (?, ?, ?, ?, ?, ?)`,
		r.Source, r.RunAt.Unix(), r.Status, r.Count, r.Error, r.Duration.Milliseconds())
	return err
}

// SourceHealth summarizes a source's recent history.
type SourceHealth struct {
	Last         SourceRun
	LastSuccess  time.Time // last ok fetch (not cached/skipped)
	RecentErrors int       // errors among the last 10 runs
}

// Health returns the latest run per source plus success/error stats.
func (s *Store) Health(ctx context.Context) (map[string]SourceHealth, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT source, run_at, status, count, error, duration_ms FROM source_runs r
		WHERE id = (SELECT MAX(id) FROM source_runs WHERE source = r.source)`)
	if err != nil {
		return nil, err
	}
	out := map[string]SourceHealth{}
	for rows.Next() {
		var r SourceRun
		var at, ms int64
		if err := rows.Scan(&r.Source, &at, &r.Status, &r.Count, &r.Error, &ms); err != nil {
			rows.Close()
			return nil, err
		}
		r.RunAt, r.Duration = time.Unix(at, 0), time.Duration(ms)*time.Millisecond
		out[r.Source] = SourceHealth{Last: r}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for name, h := range out {
		var at sql.NullInt64
		if err := s.db.QueryRowContext(ctx,
			`SELECT MAX(run_at) FROM source_runs WHERE source = ? AND status = 'ok'`, name).Scan(&at); err != nil {
			return nil, err
		}
		if at.Valid {
			h.LastSuccess = time.Unix(at.Int64, 0)
		}
		if err := s.db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM (SELECT status FROM source_runs WHERE source = ? ORDER BY id DESC LIMIT 10)
			WHERE status = 'error'`, name).Scan(&h.RecentErrors); err != nil {
			return nil, err
		}
		out[name] = h
	}
	return out, nil
}
