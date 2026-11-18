package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/itxkarthik/jobhunt/internal/company"
	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources"
	"github.com/itxkarthik/jobhunt/internal/sources/edgar"
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
		got, created, err := UpsertCompany(ctx, st, c, company.Removal{})
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
