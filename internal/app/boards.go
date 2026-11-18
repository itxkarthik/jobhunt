package app

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/itxkarthik/jobhunt/internal/company"
	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources"
	"github.com/itxkarthik/jobhunt/internal/sources/ats"
	"github.com/itxkarthik/jobhunt/internal/store"
)

// RefreshBoards reloads the boards on the companies list into a.Boards.
func (a *App) RefreshBoards(ctx context.Context) error {
	cs, err := a.Store.Companies(ctx)
	if err != nil {
		return fmt.Errorf("loading company boards: %w", err)
	}
	a.Boards.SetAdded(BoardsOf(cs))
	return nil
}

// BoardsOf turns the boards on companies into ATS boards. A company's
// board is searched for its country, and for any non-strict search if it
// hires remotely or its country is unknown.
func BoardsOf(cs []model.Company) []ats.Company {
	var out []ats.Company
	for _, c := range cs {
		for _, b := range c.Boards {
			ac := ats.Company{Name: c.Name, ATS: b.ATS, Slug: b.Slug, Remote: c.Remote || c.Country == ""}
			if c.Country != "" {
				ac.Countries = []string{c.Country}
			}
			out = append(out, ac)
		}
	}
	return out
}

// BoardChecker fetches a board once and returns its job count. Tests swap
// it for an offline fake.
type BoardChecker func(ctx context.Context, b ats.Company) (int, error)

// CheckBoard is the live BoardChecker.
func (a *App) CheckBoard(ctx context.Context, b ats.Company) (int, error) {
	return ats.Check(ctx, sources.NewClient(a.Config.UserAgent, 4), b)
}

// AddBoards checks each new board on in with check and refuses dead or
// empty ones before anything is saved. Boards the company already has
// aren't fetched again.
func AddBoards(ctx context.Context, st *store.Store, in model.Company, check BoardChecker) error {
	if len(in.Boards) == 0 {
		return nil
	}
	var have []model.Board
	if old, err := st.Company(ctx, company.Key(in.Name)); err != nil {
		return err
	} else if old != nil {
		have = old.Boards
	}
	for _, b := range in.Boards {
		if slices.ContainsFunc(have, func(x model.Board) bool { return x.ATS == b.ATS && strings.EqualFold(x.Slug, b.Slug) }) {
			continue
		}
		n, err := check(ctx, ats.Company{Name: in.Name, ATS: b.ATS, Slug: b.Slug})
		switch {
		case err != nil:
			return &BoardError{Board: b, Err: err}
		case n == 0:
			return &BoardError{Board: b}
		}
	}
	return nil
}

// BoardError means a new board failed its check: the fetch failed (Err),
// or it listed no jobs (Err nil).
type BoardError struct {
	Board model.Board
	Err   error
}

func (e *BoardError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("board %s lists no jobs; check the slug (or pass it later when it has openings)", e.Board)
	}
	return fmt.Sprintf("board %s: %v", e.Board, e.Err)
}

func (e *BoardError) Unwrap() error { return e.Err }
