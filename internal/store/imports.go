package store

import (
	"context"
	"time"
)

// ImportedItems returns the items source has finished.
func (s *Store) ImportedItems(ctx context.Context, source string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT item FROM import_items WHERE source = ?`, source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var item string
		if err := rows.Scan(&item); err != nil {
			return nil, err
		}
		out[item] = true
	}
	return out, rows.Err()
}

// ForgetImported deletes the given items of source, so later runs do
// them again.
func (s *Store) ForgetImported(ctx context.Context, source string, items []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, it := range items {
		if _, err := tx.ExecContext(ctx, `DELETE FROM import_items WHERE source = ? AND item = ?`, source, it); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// MarkImported records items as finished for source.
func (s *Store) MarkImported(ctx context.Context, source string, items []string, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, it := range items {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO import_items (source, item, at) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, source, it, at.Unix()); err != nil {
			return err
		}
	}
	return tx.Commit()
}
