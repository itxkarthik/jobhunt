package app

import (
	"context"
	"fmt"
	"slices"

	"github.com/itxkarthik/jobhunt/internal/company"
	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources/ats"
	"github.com/itxkarthik/jobhunt/internal/sources/edgar"
	"github.com/itxkarthik/jobhunt/internal/sources/mca"
	"github.com/itxkarthik/jobhunt/internal/store"
)

// Importer is one step of a sync.
type Importer struct {
	Name string
	// Skip says why the importer doesn't run (not configured), or "".
	Skip string
	Run  func(ctx context.Context) (*ImportSummary, error)
}

// SyncResult is one importer's outcome.
type SyncResult struct {
	Name    string         `json:"name"`
	Skipped string         `json:"skipped,omitempty"`
	Summary *ImportSummary `json:"summary,omitempty"`
	Error   string         `json:"error,omitempty"`
}

// Sync runs each importer in turn. A failing importer is reported and the
// rest still run; Sync itself fails only when ctx ends.
func Sync(ctx context.Context, importers []Importer) ([]SyncResult, error) {
	out := make([]SyncResult, 0, len(importers))
	for _, im := range importers {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		r := SyncResult{Name: im.Name, Skipped: im.Skip}
		if im.Skip == "" {
			sum, err := im.Run(ctx)
			r.Summary = sum
			if err != nil {
				r.Error = err.Error()
			}
		}
		out = append(out, r)
	}
	return out, ctx.Err()
}

// SyncOptions choose what a sync does.
type SyncOptions struct {
	Only  []string // importer names to run; empty runs every configured one
	Prune bool     // remove added boards the ATS reports as gone
}

// Importers lists the sync steps, each reporting why it's skipped when it
// isn't configured.
func (a *App) Importers(o SyncOptions) []Importer {
	cfg := a.Config.Importers
	ims := []Importer{
		{Name: "edgar", Run: func(ctx context.Context) (*ImportSummary, error) {
			im, err := a.EDGARImporter()
			if err != nil {
				return nil, err
			}
			return ImportEDGAR(ctx, a.Store, im, edgar.Options{Industries: cfg.EDGAR.Industries, AllEntities: cfg.EDGAR.AllEntities}, false)
		}},
		{Name: "mca", Run: func(ctx context.Context) (*ImportSummary, error) {
			c, err := a.MCAClient()
			if err != nil {
				return nil, err
			}
			return ImportMCA(ctx, a.Store, c, mca.Options{States: cfg.MCA.States, NIC: cfg.MCA.NIC}, false)
		}},
		{Name: "hn", Run: func(ctx context.Context) (*ImportSummary, error) {
			return ImportHN(ctx, a.Store, a.HNSource(), a.CheckBoard, HNOptions{CheckBoards: true}, false)
		}},
		{Name: "boards", Run: func(ctx context.Context) (*ImportSummary, error) {
			return CheckAddedBoards(ctx, a.Store, a.Boards.All(), a.CheckBoard, o.Prune)
		}},
	}
	if edgar.CheckUserAgent(cfg.EDGAR.UserAgent) != nil {
		ims[0].Skip = "set importers.edgar.user_agent or JOBHUNT_EDGAR_USER_AGENT"
	}
	if cfg.MCA.APIKey == "" {
		ims[1].Skip = "set importers.mca.api_key or JOBHUNT_DATAGOV_API_KEY"
	}
	if len(o.Only) > 0 {
		for i := range ims {
			if !slices.Contains(o.Only, ims[i].Name) {
				ims[i].Skip = "not selected"
			}
		}
	}
	return ims
}

// CheckAddedBoards fetches every board added to the companies list (not
// companies.yaml) and reports the dead ones. With prune, boards the ATS
// reports as gone (404/410) are removed; other failures may pass and are
// kept.
func CheckAddedBoards(ctx context.Context, st *store.Store, boards []ats.Company, check BoardChecker, prune bool) (*ImportSummary, error) {
	sum := &ImportSummary{Source: "boards", Complete: true}
	for _, b := range boards {
		if !b.Added {
			continue
		}
		if ctx.Err() != nil {
			return sum, ctx.Err()
		}
		sum.Read++
		sum.Fetched++
		n, err := check(ctx, b)
		board := model.Board{ATS: b.ATS, Slug: b.Slug}
		switch {
		case err != nil && ats.Gone(err) && prune:
			if _, _, err := UpsertCompany(ctx, st, model.Company{Name: b.Name}, company.Removal{Boards: []string{board.String()}}); err != nil {
				return sum, fmt.Errorf("pruning %s from %s: %w", board, b.Name, err)
			}
			sum.Updated = append(sum.Updated, b.Name+": removed "+board.String())
		case err != nil:
			sum.Errors = append(sum.Errors, (&BoardError{Board: board, Err: err}).Error())
		case n == 0:
			sum.Errors = append(sum.Errors, (&BoardError{Board: board}).Error())
		default:
			sum.Found++
		}
	}
	return sum, nil
}
