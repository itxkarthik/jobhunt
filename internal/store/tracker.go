package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/itxkarthik/jobhunt/internal/dedup"
	"github.com/itxkarthik/jobhunt/internal/model"
)

// Status is a tracker state.
type Status string

const (
	Saved     Status = "saved"
	Applied   Status = "applied"
	OA        Status = "oa"
	Interview Status = "interview"
	Offer     Status = "offer"
	Rejected  Status = "rejected"
)

// Statuses lists tracker states in pipeline order.
var Statuses = []Status{Saved, Applied, OA, Interview, Offer, Rejected}

func ParseStatus(s string) (Status, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "assessment" || s == "online-assessment" {
		s = "oa"
	}
	for _, st := range Statuses {
		if string(st) == s {
			return st, nil
		}
	}
	return "", fmt.Errorf("unknown status %q (want saved, applied, oa, interview, offer, rejected)", s)
}

// MarkSeen records that jobs appeared in results at now, stores their
// latest record, and sets FirstSeenAt and New (first seen in this run).
func (s *Store) MarkSeen(ctx context.Context, jobs []model.Job, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ts := now.Unix()
	for i := range jobs {
		j := &jobs[i]
		if j.Key == "" {
			continue
		}
		var first int64
		err := tx.QueryRowContext(ctx,
			`INSERT INTO seen (key, first_seen, last_seen) VALUES (?, ?, ?)
			 ON CONFLICT (key) DO UPDATE SET last_seen = excluded.last_seen
			 RETURNING first_seen`, j.Key, ts, ts).Scan(&first)
		if err != nil {
			return err
		}
		j.FirstSeenAt = time.Unix(first, 0)
		j.New = first == ts
		if err := putJob(ctx, tx, j, ts); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type execer interface {
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
}

func putJob(ctx context.Context, db execer, j *model.Job, ts int64) error {
	c := *j
	c.New = false
	blob, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx,
		`INSERT INTO jobs (key, json, updated_at, company_key) VALUES (?, ?, ?, ?)
		 ON CONFLICT (key) DO UPDATE SET json = excluded.json, updated_at = excluded.updated_at,
		   company_key = excluded.company_key`,
		j.Key, blob, ts, dedup.NormCompany(j.Company))
	return err
}

// CompanyJobs returns the stored jobs of the given companies (keys from
// dedup.NormCompany) last seen at or after since, newest first.
func (s *Store) CompanyJobs(ctx context.Context, keys []string, since time.Time) (map[string][]model.Job, error) {
	out := map[string][]model.Job{}
	if len(keys) == 0 {
		return out, nil
	}
	list, err := json.Marshal(keys) // one parameter, however many companies
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT company_key, json FROM jobs
		 WHERE updated_at >= ? AND company_key IN (SELECT value FROM json_each(?))
		 ORDER BY updated_at DESC`, since.Unix(), string(list))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			key  string
			blob []byte
			j    model.Job
		)
		if err := rows.Scan(&key, &blob); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(blob, &j); err != nil {
			return nil, err
		}
		out[key] = append(out[key], j)
	}
	return out, rows.Err()
}

// SaveJob stores j's latest record (used when tracking a job).
func (s *Store) SaveJob(ctx context.Context, j *model.Job) error {
	return putJob(ctx, s.db, j, time.Now().Unix())
}

// Job returns the stored record for key.
func (s *Store) Job(ctx context.Context, key string) (*model.Job, error) {
	var blob []byte
	err := s.db.QueryRowContext(ctx, `SELECT json FROM jobs WHERE key = ?`, key).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var j model.Job
	if err := json.Unmarshal(blob, &j); err != nil {
		return nil, err
	}
	return &j, nil
}

// Key lookup errors, so callers (e.g. the HTTP API) can tell them apart.
var (
	ErrNoJob        = errors.New("no job with that key")
	ErrAmbiguousKey = errors.New("ambiguous key")
	ErrNotTracked   = errors.New("job is not tracked")
)

// ResolveKey expands a key prefix (as printed in the KEY column) to a full
// key, erroring if it's unknown or ambiguous.
func (s *Store) ResolveKey(ctx context.Context, prefix string) (string, error) {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if prefix == "" {
		return "", errors.New("empty key")
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT key FROM jobs WHERE key LIKE ? || '%'
		 UNION SELECT key FROM tracker WHERE key LIKE ? || '%' LIMIT 5`, prefix, prefix)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return "", err
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	switch len(keys) {
	case 0:
		return "", fmt.Errorf("%w %q (run a search first)", ErrNoJob, prefix)
	case 1:
		return keys[0], nil
	default:
		return "", fmt.Errorf("%w %q: %s", ErrAmbiguousKey, prefix, strings.Join(keys, ", "))
	}
}

// SetStatus moves key to status, appending note to the history.
func (s *Store) SetStatus(ctx context.Context, key string, st Status, note string) error {
	now := time.Now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var applied any
	if st != Saved {
		applied = now
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO tracker (key, status, notes, created_at, updated_at, applied_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET
			status = excluded.status,
			updated_at = excluded.updated_at,
			notes = CASE WHEN excluded.notes = '' THEN tracker.notes
			             WHEN tracker.notes = '' THEN excluded.notes
			             ELSE tracker.notes || char(10) || excluded.notes END,
			applied_at = COALESCE(tracker.applied_at, excluded.applied_at)`,
		key, st, note, now, now, applied); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO tracker_events (key, at, status, note) VALUES (?, ?, ?, ?)`, key, now, st, note); err != nil {
		return err
	}
	return tx.Commit()
}

// SetNotes replaces key's tracker notes. SetStatus appends a note and
// records it in the history; this edits the current notes and leaves the
// history alone.
func (s *Store) SetNotes(ctx context.Context, key, notes string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE tracker SET notes = ?, updated_at = ? WHERE key = ?`,
		notes, time.Now().Unix(), key)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: %s", ErrNotTracked, key)
	}
	return nil
}

// Untrack removes key from the tracker (history is kept).
func (s *Store) Untrack(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM tracker WHERE key = ?`, key)
	return err
}

// Tracked is one tracker row with its job, if known.
type Tracked struct {
	Key       string
	Status    Status
	Notes     string
	CreatedAt time.Time
	UpdatedAt time.Time
	AppliedAt time.Time
	Job       *model.Job
}

// TrackedJobs lists tracker rows, most recently updated first. statuses
// filters when non-empty.
func (s *Store) TrackedJobs(ctx context.Context, statuses ...Status) ([]Tracked, error) {
	q := `SELECT t.key, t.status, t.notes, t.created_at, t.updated_at, t.applied_at, j.json
	      FROM tracker t LEFT JOIN jobs j ON j.key = t.key`
	var args []any
	if len(statuses) > 0 {
		// Only "?" placeholders are concatenated; values are bound below.
		q += ` WHERE t.status IN (?` + strings.Repeat(",?", len(statuses)-1) + `)` //nolint:gosec // G202: placeholders only
		for _, st := range statuses {
			args = append(args, st)
		}
	}
	q += ` ORDER BY t.updated_at DESC`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Tracked
	for rows.Next() {
		var t Tracked
		var created, updated int64
		var applied sql.NullInt64
		var blob []byte
		if err := rows.Scan(&t.Key, &t.Status, &t.Notes, &created, &updated, &applied, &blob); err != nil {
			return nil, err
		}
		t.CreatedAt, t.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
		if applied.Valid {
			t.AppliedAt = time.Unix(applied.Int64, 0)
		}
		if blob != nil {
			var j model.Job
			if json.Unmarshal(blob, &j) == nil {
				t.Job = &j
			}
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// StatusMap returns key -> status for every tracked job.
func (s *Store) StatusMap(ctx context.Context) (map[string]Status, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, status FROM tracker`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]Status{}
	for rows.Next() {
		var k string
		var st Status
		if err := rows.Scan(&k, &st); err != nil {
			return nil, err
		}
		m[k] = st
	}
	return m, rows.Err()
}

// Event is one tracker history entry.
type Event struct {
	At     time.Time
	Status Status
	Note   string
}

func (s *Store) Events(ctx context.Context, key string) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT at, status, note FROM tracker_events WHERE key = ? ORDER BY id`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var at int64
		if err := rows.Scan(&at, &e.Status, &e.Note); err != nil {
			return nil, err
		}
		e.At = time.Unix(at, 0)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) Hide(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO hidden (key, at) VALUES (?, ?)`, key, time.Now().Unix())
	return err
}

func (s *Store) Unhide(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM hidden WHERE key = ?`, key)
	return err
}

// HiddenKeys returns the set of hidden job keys.
func (s *Store) HiddenKeys(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key FROM hidden`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]bool{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		m[k] = true
	}
	return m, rows.Err()
}
