package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/itxkarthik/jobhunt/internal/company"
	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/store"
)

// OpenJobWindow is how recently a job must have been seen in a search to
// count as one of a company's open jobs.
const OpenJobWindow = 14 * 24 * time.Hour

// CompanyQuery selects companies for the CLI, API and MCP tools.
type CompanyQuery struct {
	company.FilterInput
	// Hiring keeps companies with an open job at one of these experience
	// levels (intern, entry, mid, senior); "any" means any open job.
	Hiring []string `json:"hiring,omitempty"`
	// Jobs includes each company's open jobs, not just their count.
	Jobs  bool `json:"jobs,omitempty"`
	Limit int  `json:"limit,omitempty"`
}

// CompanyView is a company with what jobhunt knows about its openings.
type CompanyView struct {
	model.Company
	OpenJobs int         `json:"open_jobs"`
	Jobs     []model.Job `json:"jobs,omitempty"`
}

// UpsertCompany cleans in, merges it into the stored record (creating it
// if new) and returns the result and whether it was created.
func UpsertCompany(ctx context.Context, st *store.Store, in model.Company, rm company.Removal) (model.Company, bool, error) {
	if err := company.Clean(&in); err != nil {
		return model.Company{}, false, &InvalidError{err}
	}
	return st.UpdateCompany(ctx, in.Key, func(old *model.Company) (model.Company, error) {
		return company.Merge(old, in, rm, time.Now()), nil
	})
}

// InvalidError means a company update failed validation; nothing was
// stored. Other errors from UpsertCompany are storage failures.
type InvalidError struct{ Err error }

func (e *InvalidError) Error() string { return e.Err.Error() }
func (e *InvalidError) Unwrap() error { return e.Err }

// AppendNote adds a dated line to a stored company's notes. The read and
// the write happen in one transaction, so concurrent notes don't
// overwrite each other. Notes are limited to company.MaxNotes bytes, the
// limit Clean applies; a note that would be cut short is refused.
func AppendNote(ctx context.Context, st *store.Store, name, note, source string, now time.Time) (model.Company, error) {
	c, err := FindCompany(ctx, st, name)
	if err != nil {
		return model.Company{}, err
	}
	line := now.UTC().Format(time.DateOnly) + ": " + note
	got, _, err := st.UpdateCompany(ctx, c.Key, func(old *model.Company) (model.Company, error) {
		if old == nil {
			return model.Company{}, store.ErrNoCompany
		}
		notes := line
		if old.Notes != "" {
			notes = old.Notes + "\n" + line
		}
		if len(notes) > company.MaxNotes {
			return model.Company{}, &InvalidError{fmt.Errorf("the notes would pass %d bytes; replace them instead of appending", company.MaxNotes)}
		}
		in := model.Company{Name: old.Name, Notes: notes, Sources: []string{source}}
		if err := company.Clean(&in); err != nil {
			return model.Company{}, &InvalidError{err}
		}
		return company.Merge(old, in, company.Removal{}, now), nil
	})
	return got, err
}

// FindCompany resolves a name, key or key prefix to a stored company.
func FindCompany(ctx context.Context, st *store.Store, name string) (*model.Company, error) {
	key := company.Key(name)
	if key == "" {
		return nil, store.ErrNoCompany
	}
	key, err := st.ResolveCompany(ctx, key)
	if err != nil {
		return nil, err
	}
	c, err := st.Company(ctx, key)
	if err == nil && c == nil {
		err = store.ErrNoCompany
	}
	return c, err
}

// Companies lists stored companies matching q, newest funding first.
func Companies(ctx context.Context, st *store.Store, q CompanyQuery) ([]CompanyView, error) {
	f, err := company.ParseFilter(q.FilterInput, ParseAge)
	if err != nil {
		return nil, err
	}
	var levels []model.Experience
	anyJob := false
	for _, h := range q.Hiring {
		if h == Any {
			anyJob = true
			continue
		}
		e, err := model.ParseExperience(h)
		if err != nil {
			return nil, err
		}
		levels = append(levels, e)
	}
	if q.Limit < 0 {
		return nil, errors.New("limit must be 0 or more")
	}

	all, err := st.Companies(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var cs []model.Company
	for i := range all {
		if f.Match(&all[i], now) {
			cs = append(cs, all[i])
		}
	}
	company.Sort(cs)

	keys := make([]string, len(cs))
	for i, c := range cs {
		keys[i] = c.Key
	}
	jobs, err := st.CompanyJobs(ctx, keys, now.Add(-OpenJobWindow))
	if err != nil {
		return nil, err
	}
	out := make([]CompanyView, 0, len(cs))
	for _, c := range cs {
		js := jobs[c.Key]
		if len(levels) > 0 {
			js = slices.DeleteFunc(slices.Clone(js), func(j model.Job) bool { return !slices.Contains(levels, j.Experience) })
		}
		if (anyJob || len(levels) > 0) && len(js) == 0 {
			continue
		}
		v := CompanyView{Company: c, OpenJobs: len(js)}
		if q.Jobs {
			v.Jobs = js
		}
		out = append(out, v)
		if q.Limit > 0 && len(out) == q.Limit {
			break
		}
	}
	return out, nil
}
