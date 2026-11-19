package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/itxkarthik/jobhunt/internal/company"
	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources"
	"github.com/itxkarthik/jobhunt/internal/sources/ats"
	"github.com/itxkarthik/jobhunt/internal/sources/edgar"
	"github.com/itxkarthik/jobhunt/internal/sources/hn"
	"github.com/itxkarthik/jobhunt/internal/store"
)

// ImportSummary reports one importer run.
type ImportSummary struct {
	Source   string   `json:"source"`
	Read     int      `json:"read"`    // index pages, threads or records read
	Found    int      `json:"found"`   // items that looked relevant
	Fetched  int      `json:"fetched"` // documents fetched
	Added    []string `json:"added,omitempty"`
	Updated  []string `json:"updated,omitempty"`
	Skipped  []string `json:"skipped,omitempty"` // records that failed validation
	Errors   []string `json:"errors,omitempty"`
	Complete bool     `json:"complete"` // false when a limit cut the run short
	DryRun   bool     `json:"dry_run,omitempty"`
}

// EDGARImporter builds the Form D importer from config, or explains what
// is missing.
func (a *App) EDGARImporter() (*edgar.Importer, error) {
	ua := a.Config.Importers.EDGAR.UserAgent
	if err := edgar.CheckUserAgent(ua); err != nil {
		return nil, err
	}
	return &edgar.Importer{Client: sources.NewClient(ua, edgar.Rate), Base: edgar.Base}, nil
}

// ImportEDGAR runs im and saves the companies it finds. With dryRun it
// saves nothing and records nothing as done.
func ImportEDGAR(ctx context.Context, st *store.Store, im *edgar.Importer, o edgar.Options, dryRun bool) (*ImportSummary, error) {
	// Finished items are scoped to the filters: a broader run (another
	// industry, all entities) reads the same days again.
	src := "edgar:" + o.Scope()
	done, err := st.ImportedItems(ctx, src)
	if err != nil {
		return nil, err
	}
	if o.Redo {
		// Read the window again, e.g. after removing an imported company.
		// Saving is a merge, so companies already stored aren't duplicated.
		// The saved progress is deleted too, so a redo cut short by
		// --max-filings continues on the next plain run. A dry run only
		// ignores it.
		if !dryRun {
			if err := st.ForgetImported(ctx, src, o.RedoItems(done)); err != nil {
				return nil, err
			}
		}
		done = map[string]bool{}
	}
	res, runErr := im.Run(ctx, o, func(item string) bool { return done[item] })
	sum := &ImportSummary{Source: "edgar", DryRun: dryRun}
	if res == nil {
		return sum, runErr
	}
	sum.Read, sum.Found, sum.Fetched, sum.Errors, sum.Complete = res.Days, res.Candidates, res.Fetched, res.Errors, res.Complete && runErr == nil
	if dryRun {
		for _, c := range res.Companies {
			sum.Added = append(sum.Added, c.Name)
		}
		return sum, runErr
	}
	if err := saveCompanies(ctx, st, res.Companies, sum); err != nil {
		return sum, err
	}
	if err := st.MarkImported(ctx, src, res.Done, time.Now()); err != nil {
		return sum, err
	}
	return sum, runErr
}

// saveCompanies upserts imported companies, noting each outcome. Records
// that fail validation are skipped; a storage error stops the import, so
// nothing is marked done and the next run tries again.
func saveCompanies(ctx context.Context, st *store.Store, cs []model.Company, sum *ImportSummary) error {
	for _, c := range cs {
		got, created, err := ImportCompany(ctx, st, c)
		_, invalid := errors.AsType[*InvalidError](err)
		switch {
		case invalid:
			sum.Skipped = append(sum.Skipped, c.Name+": "+err.Error())
		case err != nil:
			return fmt.Errorf("saving %s: %w", c.Name, err)
		case created:
			sum.Added = append(sum.Added, got.Name)
		default:
			sum.Updated = append(sum.Updated, got.Name)
		}
	}
	return nil
}

// HNOptions bound one HN leads import.
type HNOptions struct {
	CheckBoards bool // fetch each new board once and drop dead ones; off keeps no boards
	Redo        bool // read posts already imported again
}

// HNSource builds the HN leads reader.
func (a *App) HNSource() *hn.Source {
	return hn.New(sources.NewClient(a.Config.UserAgent, 2))
}

// ImportHN turns the latest "Who is hiring?" thread into company leads.
// Finished posts are recorded, so a rerun only reads posts added since.
// Boards found in a post are checked with check before they're kept.
func ImportHN(ctx context.Context, st *store.Store, src *hn.Source, check BoardChecker, o HNOptions, dryRun bool) (*ImportSummary, error) {
	sum := &ImportSummary{Source: "hn", DryRun: dryRun, Complete: true}
	th, err := src.Latest(ctx)
	if err != nil {
		return sum, err
	}
	scope := "hn:" + th.ID
	done, err := st.ImportedItems(ctx, scope)
	if err != nil {
		return sum, err
	}
	if o.Redo {
		done = map[string]bool{}
	}
	sum.Read = 1
	leads := th.Leads(func(id int64) bool { return done[postItem(id)] })
	sum.Found = len(leads)
	var cs []model.Company
	var items []string
	for _, l := range leads {
		c := l.Company
		if !dryRun {
			c.Boards = liveBoards(ctx, st, c, check, o.CheckBoards, sum)
			if ctx.Err() != nil {
				return sum, ctx.Err()
			}
		}
		cs = append(cs, c)
		items = append(items, postItem(l.PostID))
	}
	if dryRun {
		for _, c := range cs {
			sum.Added = append(sum.Added, c.Name)
		}
		return sum, nil
	}
	if err := saveCompanies(ctx, st, cs, sum); err != nil {
		return sum, err
	}
	return sum, st.MarkImported(ctx, scope, items, time.Now())
}

func postItem(id int64) string { return "post:" + strconv.FormatInt(id, 10) }

// liveBoards keeps the boards of c that are already stored or pass check.
func liveBoards(ctx context.Context, st *store.Store, c model.Company, check BoardChecker, enabled bool, sum *ImportSummary) []model.Board {
	if len(c.Boards) == 0 || !enabled {
		return nil
	}
	var have []model.Board
	if old, err := st.Company(ctx, company.Key(c.Name)); err == nil && old != nil {
		have = old.Boards
	}
	var keep []model.Board
	for _, b := range c.Boards {
		if slices.Contains(have, b) {
			keep = append(keep, b)
			continue
		}
		sum.Fetched++
		n, err := check(ctx, ats.Company{Name: c.Name, ATS: b.ATS, Slug: b.Slug})
		switch {
		case err != nil:
			sum.Errors = append(sum.Errors, (&BoardError{Board: b, Err: err}).Error())
		case n > 0:
			keep = append(keep, b)
		}
	}
	return keep
}
