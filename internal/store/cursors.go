package store

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/itxkarthik/jobhunt/internal/model"
)

var cursorName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// ValidCursorName reports whether name can be used as a feed cursor:
// lowercase letters, digits, '.', '_' and '-', up to 64 characters.
func ValidCursorName(name string) error {
	if !cursorName.MatchString(name) {
		return fmt.Errorf("bad cursor name %q (use lowercase letters, digits, '.', '_', '-'; max 64)", name)
	}
	return nil
}

// deliveryRetention is how long a delivered key is remembered. A job still
// open and matching after that is delivered again, which at-least-once
// consumers already handle by deduping on the key.
const deliveryRetention = 180 * 24 * time.Hour

// FeedState returns what the named consumer has received: the keys already
// delivered, and the stored records of jobs that went out in a batch that
// wasn't accepted (oldest first). Pending jobs are resent even after they
// disappear from their source.
func (s *Store) FeedState(ctx context.Context, cursor string) (delivered map[string]bool, pending []model.Job, err error) {
	delivered, err = s.deliveredKeys(ctx, cursor)
	if err != nil {
		return nil, nil, err
	}
	pending, err = s.pendingJobs(ctx, cursor)
	if err != nil {
		return nil, nil, err
	}
	return delivered, pending, nil
}

func (s *Store) deliveredKeys(ctx context.Context, cursor string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT key FROM feed_deliveries WHERE cursor = ? AND state = 'delivered'`, cursor)
	if err != nil {
		return nil, fmt.Errorf("read cursor %s: %w", cursor, err)
	}
	defer rows.Close()
	keys := map[string]bool{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys[k] = true
	}
	return keys, rows.Err()
}

func (s *Store) pendingJobs(ctx context.Context, cursor string) ([]model.Job, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT j.json FROM feed_deliveries d JOIN jobs j ON j.key = d.key
		 WHERE d.cursor = ? AND d.state = 'pending'
		 ORDER BY d.updated_at, d.key`, cursor)
	if err != nil {
		return nil, fmt.Errorf("read pending jobs for %s: %w", cursor, err)
	}
	defer rows.Close()
	var jobs []model.Job
	for rows.Next() {
		var blob []byte
		if err := rows.Scan(&blob); err != nil {
			return nil, err
		}
		var j model.Job
		if err := json.Unmarshal(blob, &j); err != nil {
			return nil, fmt.Errorf("decode pending job: %w", err)
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// MarkPending records that keys are about to be sent to the named
// consumer. Keys already delivered stay delivered.
func (s *Store) MarkPending(ctx context.Context, cursor string, keys []string, now time.Time) error {
	return s.markDeliveries(ctx, cursor, keys, now,
		`INSERT INTO feed_deliveries (cursor, key, state, updated_at) VALUES (?, ?, 'pending', ?)
		 ON CONFLICT (cursor, key) DO NOTHING`)
}

// MarkDelivered records that the named consumer accepted keys, and forgets
// deliveries older than the retention period.
func (s *Store) MarkDelivered(ctx context.Context, cursor string, keys []string, now time.Time) error {
	err := s.markDeliveries(ctx, cursor, keys, now,
		`INSERT INTO feed_deliveries (cursor, key, state, updated_at) VALUES (?, ?, 'delivered', ?)
		 ON CONFLICT (cursor, key) DO UPDATE SET state = 'delivered', updated_at = excluded.updated_at`)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`DELETE FROM feed_deliveries WHERE cursor = ? AND state = 'delivered' AND updated_at < ?`,
		cursor, now.Add(-deliveryRetention).Unix())
	return err
}

func (s *Store) markDeliveries(ctx context.Context, cursor string, keys []string, now time.Time, query string) error {
	if err := ValidCursorName(cursor); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, k := range keys {
		if _, err := tx.ExecContext(ctx, query, cursor, k, now.Unix()); err != nil {
			return fmt.Errorf("update cursor %s: %w", cursor, err)
		}
	}
	return tx.Commit()
}
