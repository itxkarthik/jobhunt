package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"time"
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

// Cursor returns the delivery position of the named feed consumer.
// ok is false when the cursor doesn't exist yet.
func (s *Store) Cursor(ctx context.Context, name string) (t time.Time, ok bool, err error) {
	var ts int64
	err = s.db.QueryRowContext(ctx, `SELECT delivered_at FROM feed_cursors WHERE name = ?`, name).Scan(&ts)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return time.Time{}, false, nil
	case err != nil:
		return time.Time{}, false, fmt.Errorf("read cursor %s: %w", name, err)
	}
	return time.Unix(ts, 0), true, nil
}

// AdvanceCursor records that everything first seen up to t was delivered
// to the named consumer. A cursor never moves backwards.
func (s *Store) AdvanceCursor(ctx context.Context, name string, t time.Time) error {
	if err := ValidCursorName(name); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO feed_cursors (name, delivered_at, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT (name) DO UPDATE SET
		   delivered_at = max(delivered_at, excluded.delivered_at),
		   updated_at   = excluded.updated_at`,
		name, t.Unix(), time.Now().Unix())
	if err != nil {
		return fmt.Errorf("advance cursor %s: %w", name, err)
	}
	return nil
}
