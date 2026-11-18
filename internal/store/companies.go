package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/itxkarthik/jobhunt/internal/model"
)

// ErrNoCompany is returned when no stored company matches.
var ErrNoCompany = errors.New("no company with that name")

// Company returns the company stored under key, or nil.
func (s *Store) Company(ctx context.Context, key string) (*model.Company, error) {
	return getCompany(ctx, s.db, key)
}

type queryer interface {
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
}

func getCompany(ctx context.Context, db queryer, key string) (*model.Company, error) {
	var blob []byte
	err := db.QueryRowContext(ctx, `SELECT json FROM companies WHERE key = ?`, key).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c model.Company
	if err := json.Unmarshal(blob, &c); err != nil {
		return nil, fmt.Errorf("company %s: %w", key, err)
	}
	return &c, nil
}

// Companies returns every stored company, by name.
func (s *Store) Companies(ctx context.Context) ([]model.Company, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT json FROM companies ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Company
	for rows.Next() {
		var blob []byte
		if err := rows.Scan(&blob); err != nil {
			return nil, err
		}
		var c model.Company
		if err := json.Unmarshal(blob, &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpdateCompany reads the company under key (nil if new), passes it to fn
// and stores what fn returns, in one transaction so concurrent updates
// (an agent and the web UI, say) don't lose each other's changes. It
// reports whether the company was created.
func (s *Store) UpdateCompany(ctx context.Context, key string, fn func(old *model.Company) (model.Company, error)) (model.Company, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Company{}, false, err
	}
	defer tx.Rollback()
	old, err := getCompany(ctx, tx, key)
	if err != nil {
		return model.Company{}, false, err
	}
	c, err := fn(old)
	if err != nil {
		return model.Company{}, false, err
	}
	if c.Key != key {
		return model.Company{}, false, fmt.Errorf("company key changed from %q to %q", key, c.Key)
	}
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = time.Now()
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = c.UpdatedAt
	}
	blob, err := json.Marshal(c)
	if err != nil {
		return model.Company{}, false, err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO companies (key, name, json, created_at, updated_at) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (key) DO UPDATE SET name = excluded.name, json = excluded.json, updated_at = excluded.updated_at`,
		key, c.Name, blob, c.CreatedAt.Unix(), c.UpdatedAt.Unix())
	if err != nil {
		return model.Company{}, false, err
	}
	return c, old == nil, tx.Commit()
}

// DeleteCompany removes the company under key, reporting whether it existed.
func (s *Store) DeleteCompany(ctx context.Context, key string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM companies WHERE key = ?`, key)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ResolveCompany finds the key of a stored company from its key or the
// start of it, erroring if none or several match.
func (s *Store) ResolveCompany(ctx context.Context, key string) (string, error) {
	if key == "" {
		return "", ErrNoCompany
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT key FROM companies WHERE key = ?1
		 UNION SELECT key FROM companies WHERE key LIKE ?2 ESCAPE '\' LIMIT 6`,
		key, likePrefix(key))
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
		if k == key {
			return k, nil
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	switch len(keys) {
	case 0:
		return "", fmt.Errorf("%w %q", ErrNoCompany, key)
	case 1:
		return keys[0], nil
	}
	return "", fmt.Errorf("%w %q: matches %s", ErrAmbiguousKey, key, strings.Join(keys, ", "))
}

func likePrefix(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s) + "%"
}
