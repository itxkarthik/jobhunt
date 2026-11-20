package company

import (
	"errors"
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
	// Cities narrows Countries to these cities (lower-case).
	Cities []string
	// RemoteOK also passes companies that hire remotely beyond their
	// country, wherever they're based.
	RemoteOK bool
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
	Cities       []string `json:"cities,omitempty"`
	RemoteOK     bool     `json:"remote_ok,omitempty"`
}

// ParseFilter validates in. parseAge reads FundedWithin (app.ParseAge).
func ParseFilter(in FilterInput, parseAge func(string) (time.Duration, error)) (Filter, error) {
	var f Filter
	for _, list := range []*[]string{&in.Stages, &in.Headcounts, &in.Countries, &in.Cities} {
		var err error
		if *list, err = liftAny(*list); err != nil {
			return f, err
		}
	}
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
	for _, c := range in.Cities {
		if c = strings.ToLower(strings.TrimSpace(c)); c != "" {
			f.Cities = append(f.Cities, c)
		}
	}
	f.HasContact, f.Query, f.RemoteOK = in.HasContact, strings.ToLower(strings.TrimSpace(in.Query)), in.RemoteOK
	return f, nil
}

// liftAny turns a list that is just "any" into no restriction, so a flag
// can lift a value the profile would otherwise fill in.
func liftAny(xs []string) ([]string, error) {
	if !slices.ContainsFunc(xs, func(s string) bool { return strings.EqualFold(strings.TrimSpace(s), "any") }) {
		return xs, nil
	}
	if len(xs) > 1 {
		return nil, errors.New(`"any" can't be combined with other values`)
	}
	return nil, nil
}

// place checks Countries and Cities; RemoteOK lets remote hirers through.
func (f Filter) place(c *model.Company) bool {
	if f.RemoteOK && c.Remote {
		return true
	}
	if len(f.Countries) > 0 && !slices.Contains(f.Countries, c.Country) {
		return false
	}
	return len(f.Cities) == 0 || slices.Contains(f.Cities, strings.ToLower(c.City))
}

// Match reports whether c passes f at time now.
func (f Filter) Match(c *model.Company, now time.Time) bool {
	switch {
	case len(f.Stages) > 0 && !slices.Contains(f.Stages, c.Stage):
		return false
	case len(f.Headcounts) > 0 && !slices.Contains(f.Headcounts, c.Headcount):
		return false
	case !f.place(c):
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

// Fit says how a company compares with the criteria of a filter, such as
// the companies part of the user's profile. Unknown facts are listed
// apart from known mismatches: they're worth finding out, not a reason to
// drop the company.
type Fit struct {
	Fits    bool     `json:"fits" jsonschema:"true when nothing known is outside the criteria and nothing is unknown"`
	Misses  []string `json:"misses,omitempty" jsonschema:"known facts outside the criteria"`
	Unknown []string `json:"unknown,omitempty" jsonschema:"facts the criteria need that the record lacks"`
}

// Fit compares c with f's stage, headcount, place and funding criteria
// at time now. With nothing unknown, Fits equals Match for those criteria.
func (f Filter) Fit(c *model.Company, now time.Time) Fit {
	var fit Fit
	want := func(xs []string) string { return strings.Join(xs, ", ") }
	check := func(name string, unknown bool, ok bool, got string, wanted []string) {
		switch {
		case len(wanted) == 0:
		case unknown:
			fit.Unknown = append(fit.Unknown, name)
		case !ok:
			fit.Misses = append(fit.Misses, fmt.Sprintf("%s %s; wanted: %s", name, got, want(wanted)))
		}
	}
	check("stage", c.Stage == "" || c.Stage == model.StageUnknown, slices.Contains(f.Stages, c.Stage), string(c.Stage), strs(f.Stages))
	check("headcount", c.Headcount == "" || c.Headcount == model.HeadcountUnknown, slices.Contains(f.Headcounts, c.Headcount),
		string(c.Headcount), strs(f.Headcounts))
	if !(f.RemoteOK && c.Remote) {
		remote := ""
		if f.RemoteOK {
			remote = " (or hiring remotely)"
		}
		// Country and city separately: a known city can miss even when
		// the country isn't recorded.
		switch {
		case len(f.Countries) == 0:
		case c.Country == "":
			fit.Unknown = append(fit.Unknown, "country")
		case !slices.Contains(f.Countries, c.Country):
			fit.Misses = append(fit.Misses, fmt.Sprintf("based in %s; wanted: %s%s", c.Country, want(f.Countries), remote))
		}
		switch {
		case len(f.Cities) == 0:
		case c.City == "":
			fit.Unknown = append(fit.Unknown, "city")
		case !slices.Contains(f.Cities, strings.ToLower(c.City)):
			fit.Misses = append(fit.Misses, fmt.Sprintf("city %s; wanted: %s%s", c.City, want(f.Cities), remote))
		}
	}
	if f.FundedWithin > 0 || f.FundedBefore > 0 {
		if r := c.LatestRound(); r == nil || r.Date.IsZero() {
			fit.Unknown = append(fit.Unknown, "latest funding date")
		} else if age := now.Sub(r.Date); (f.FundedWithin > 0 && age > f.FundedWithin) || age < f.FundedBefore {
			fit.Misses = append(fit.Misses, fmt.Sprintf("latest round %s, %d days ago; wanted: %s",
				r.Date.Format("2006-01-02"), int(age.Hours()/24), fundingWindow(f)))
		}
	}
	fit.Fits = len(fit.Misses) == 0 && len(fit.Unknown) == 0
	return fit
}

func fundingWindow(f Filter) string {
	days := func(d time.Duration) int { return int(d.Hours() / 24) }
	switch {
	case f.FundedWithin > 0 && f.FundedBefore > 0:
		return fmt.Sprintf("%d to %d days ago", days(f.FundedBefore), days(f.FundedWithin))
	case f.FundedWithin > 0:
		return fmt.Sprintf("within %d days", days(f.FundedWithin))
	default:
		return fmt.Sprintf("at least %d days ago", days(f.FundedBefore))
	}
}

func strs[T ~string](xs []T) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = string(x)
	}
	return out
}
