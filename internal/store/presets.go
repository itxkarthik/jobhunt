package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/itxkarthik/jobhunt/internal/model"
)

// Preset is a named, saved set of filters.
type Preset struct {
	Name      string
	Filters   model.Filters
	UpdatedAt time.Time
}

func (s *Store) SavePreset(ctx context.Context, name string, f model.Filters) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO presets (name, filters_json, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT (name) DO UPDATE SET filters_json = excluded.filters_json, updated_at = excluded.updated_at`,
		name, string(b), time.Now().Unix())
	return err
}

// ErrNoPreset is returned for unknown preset names.
var ErrNoPreset = errors.New("no such preset")

func (s *Store) Preset(ctx context.Context, name string) (Preset, error) {
	var raw string
	var at int64
	err := s.db.QueryRowContext(ctx, `SELECT filters_json, updated_at FROM presets WHERE name = ?`, name).Scan(&raw, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return Preset{}, fmt.Errorf("%w %q (see `jobhunt preset list`)", ErrNoPreset, name)
	}
	if err != nil {
		return Preset{}, err
	}
	p := Preset{Name: name, UpdatedAt: time.Unix(at, 0)}
	return p, json.Unmarshal([]byte(raw), &p.Filters)
}

func (s *Store) Presets(ctx context.Context) ([]Preset, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, filters_json, updated_at FROM presets ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Preset
	for rows.Next() {
		var p Preset
		var raw string
		var at int64
		if err := rows.Scan(&p.Name, &raw, &at); err != nil {
			return nil, err
		}
		p.UpdatedAt = time.Unix(at, 0)
		if err := json.Unmarshal([]byte(raw), &p.Filters); err != nil {
			return nil, fmt.Errorf("preset %s: %w", p.Name, err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) DeletePreset(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM presets WHERE name = ?`, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w %q", ErrNoPreset, name)
	}
	return nil
}
