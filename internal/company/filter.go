package company

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/itxkarthik/jobhunt/internal/model"
)

// Filter selects companies. Empty fields don't restrict.
type Filter struct {
	Stages       []model.Stage
	Headcounts   []model.Headcount
	Countries    []string
	Tags         []string
	FundedWithin time.Duration // latest round no older than this
	FundedBefore time.Duration // latest round at least this old
	HasContact   bool
	Query        string // case-insensitive substring of the name
}

// FilterInput is the user-facing form of Filter, shared by CLI flags, the
// API and MCP tools.
type FilterInput struct {
	Stages       []string `json:"stages,omitempty"`
	Headcounts   []string `json:"headcounts,omitempty"`
	Countries    []string `json:"countries,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	FundedWithin string   `json:"funded_within,omitempty"` // an age like 270d: raised at most this long ago
	FundedBefore string   `json:"funded_before,omitempty"` // an age like 90d: raised at least this long ago
	HasContact   bool     `json:"has_contact,omitempty"`
	Query        string   `json:"q,omitempty"`
}

// ParseFilter validates in. parseAge reads FundedWithin (app.ParseAge).
func ParseFilter(in FilterInput, parseAge func(string) (time.Duration, error)) (Filter, error) {
	var f Filter
	for _, s := range in.Stages {
		st, err := model.ParseStage(s)
		if err != nil {
			return f, err
		}
		f.Stages = append(f.Stages, st)
	}
	for _, s := range in.Headcounts {
		h, err := model.ParseHeadcount(s)
		if err != nil {
			return f, err
		}
		f.Headcounts = append(f.Headcounts, h)
	}
	for _, c := range in.Countries {
		code, ok := model.NormalizeCountry(c)
		if !ok {
			return f, fmt.Errorf("unknown country %q", c)
		}
		f.Countries = append(f.Countries, code)
	}
	for _, t := range in.Tags {
		if t = strings.ToLower(strings.TrimSpace(t)); t != "" {
			f.Tags = append(f.Tags, t)
		}
	}
	if in.FundedWithin != "" {
		d, err := parseAge(in.FundedWithin)
		if err != nil {
			return f, err
		}
		f.FundedWithin = d
	}
	if in.FundedBefore != "" {
		d, err := parseAge(in.FundedBefore)
		if err != nil {
			return f, err
		}
		f.FundedBefore = d
	}
	if f.FundedWithin > 0 && f.FundedBefore >= f.FundedWithin {
		return f, fmt.Errorf("funded_before (%s) must be shorter than funded_within (%s)", in.FundedBefore, in.FundedWithin)
	}
	f.HasContact, f.Query = in.HasContact, strings.ToLower(strings.TrimSpace(in.Query))
	return f, nil
}

// Match reports whether c passes f at time now.
func (f Filter) Match(c *model.Company, now time.Time) bool {
	switch {
	case len(f.Stages) > 0 && !slices.Contains(f.Stages, c.Stage):
		return false
	case len(f.Headcounts) > 0 && !slices.Contains(f.Headcounts, c.Headcount):
		return false
	case len(f.Countries) > 0 && !slices.Contains(f.Countries, c.Country):
		return false
	case f.HasContact && len(c.Contacts) == 0:
		return false
	case f.Query != "" && !strings.Contains(strings.ToLower(c.Name), f.Query):
		return false
	}
	for _, t := range f.Tags {
		if !slices.Contains(c.Tags, t) {
			return false
		}
	}
	if f.FundedWithin > 0 || f.FundedBefore > 0 {
		r := c.LatestRound()
		if r == nil || r.Date.IsZero() {
			return false
		}
		age := now.Sub(r.Date)
		if (f.FundedWithin > 0 && age > f.FundedWithin) || age < f.FundedBefore {
			return false
		}
	}
	return true
}

// Sort orders companies by latest funding date, newest first, then name.
func Sort(cs []model.Company) {
	slices.SortStableFunc(cs, func(a, b model.Company) int {
		var da, db time.Time
		if r := a.LatestRound(); r != nil {
			da = r.Date
		}
		if r := b.LatestRound(); r != nil {
			db = r.Date
		}
		if c := db.Compare(da); c != 0 {
			return c
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
}
