// Package store persists cached fetches, source health, and (later) tracker
// state in SQLite via the pure-Go modernc.org/sqlite driver.
package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
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
		// 0700: the DB holds your tracker notes and history.
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
	}
	// _txlock=immediate: every transaction here reads then writes. Taking
	// the write lock at BEGIN lets busy_timeout queue concurrent writers
	// (the server, an agent, a watch loop); a deferred transaction would
	// fail with SQLITE_BUSY when it tries to upgrade its read lock.
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_txlock=immediate"
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
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// migrate applies migrations/NNN_*.sql in order, tracked via PRAGMA user_version.
func (s *Store) migrate(ctx context.Context) error {
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
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
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(b)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		// PRAGMA can't take a bound parameter; n is our own loop counter.
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", n)); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

const (
	// StaleCache is how old a cache entry may be and still stand in for a
	// source that fails; Prune deletes older ones.
	StaleCache = 30 * 24 * time.Hour
	// KeepSeen is how long Prune keeps the record of a job that stopped
	// showing up in results (unless it's tracked).
	KeepSeen = 90 * 24 * time.Hour
)

// Prune deletes what searches no longer use: cache entries older than
// StaleCache, and stored jobs and seen records not in results for KeepSeen,
// except tracked ones. A pruned job that comes back counts as new.
func (s *Store) Prune(ctx context.Context, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	seen := now.Add(-KeepSeen).Unix()
	for _, q := range []struct {
		sql string
		arg int64
	}{
		{`DELETE FROM fetch_cache WHERE fetched_at < ?`, now.Add(-StaleCache).Unix()},
		{`DELETE FROM jobs WHERE key NOT IN (SELECT key FROM tracker)
		    AND key NOT IN (SELECT key FROM seen WHERE last_seen >= ?)`, seen},
		{`DELETE FROM seen WHERE last_seen < ? AND key NOT IN (SELECT key FROM tracker)`, seen},
	} {
		if _, err := tx.ExecContext(ctx, q.sql, q.arg); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Cached is a fetch cache entry: a source's jobs for one query, and the
// classification stored with them (see PutCache).
type Cached struct {
	Jobs      []model.Job
	FetchedAt time.Time
	// ClassVersion and Class are what the caller stored with the jobs;
	// ClassVersion is "" for entries written before they were stored.
	ClassVersion string
	Class        []byte
}

// CachedJobs returns the entry cached for (source, key) if it was fetched
// within ttl.
func (s *Store) CachedJobs(ctx context.Context, source, key string, ttl time.Duration) (Cached, bool, error) {
	var c Cached
	// Query with RawBytes, not QueryRow: Row.Scan copies a BLOB into a new
	// slice, and the jobs are tens of MB on the big boards.
	rows, err := s.db.QueryContext(ctx,
		`SELECT fetched_at, class_version, class_json, jobs_json FROM fetch_cache WHERE source = ? AND query_key = ?`,
		source, key)
	if err != nil {
		return c, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return c, false, rows.Err()
	}
	var (
		at   int64
		blob sql.RawBytes
	)
	if err := rows.Scan(&at, &c.ClassVersion, &c.Class, &blob); err != nil {
		return c, false, err
	}
	c.FetchedAt = time.Unix(at, 0)
	if time.Since(c.FetchedAt) > ttl {
		return c, false, nil
	}
	if err := json.Unmarshal(blob, &c.Jobs); err != nil {
		return c, false, nil //nolint:nilerr // a corrupt entry is just a cache miss
	}
	return c, true, nil
}

// PutCache stores jobs fetched now for (source, key), with the caller's
// classification of them (class, made by classifier version classVersion).
func (s *Store) PutCache(ctx context.Context, source, key string, jobs []model.Job, classVersion string, class []byte) error {
	blob, err := json.Marshal(jobs)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO fetch_cache (source, query_key, fetched_at, jobs_json, class_version, class_json) VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (source, query_key) DO UPDATE SET fetched_at = excluded.fetched_at, jobs_json = excluded.jobs_json,
		   class_version = excluded.class_version, class_json = excluded.class_json`,
		source, key, time.Now().Unix(), blob, classVersion, class)
	return err
}

// RedoCache replaces an entry's jobs and classification, e.g. after the
// classifier's rules changed, keeping its fetch time so the entry still
// expires when it would have. It changes the entry only if it's still the
// one read as from: a fetch stored meanwhile is newer and stays.
func (s *Store) RedoCache(ctx context.Context, source, key string, from Cached, jobs []model.Job, classVersion string, class []byte) error {
	blob, err := json.Marshal(jobs)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE fetch_cache SET jobs_json = ?, class_version = ?, class_json = ?
		 WHERE source = ? AND query_key = ? AND fetched_at = ? AND class_version = ?`,
		blob, classVersion, class, source, key, from.FetchedAt.Unix(), from.ClassVersion)
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
	// Read the latest runs fully before the per-source queries below: an
	// in-memory DB has a single connection.
	out, err := s.latestRuns(ctx)
	if err != nil {
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

func (s *Store) latestRuns(ctx context.Context) (map[string]SourceHealth, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT source, run_at, status, count, error, duration_ms FROM source_runs r
		WHERE id = (SELECT MAX(id) FROM source_runs WHERE source = r.source)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]SourceHealth{}
	for rows.Next() {
		var r SourceRun
		var at, ms int64
		if err := rows.Scan(&r.Source, &at, &r.Status, &r.Count, &r.Error, &ms); err != nil {
			return nil, err
		}
		r.RunAt, r.Duration = time.Unix(at, 0), time.Duration(ms)*time.Millisecond
		out[r.Source] = SourceHealth{Last: r}
	}
	return out, rows.Err()
}
