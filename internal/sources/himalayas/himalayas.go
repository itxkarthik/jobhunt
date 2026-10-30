// Package himalayas queries the Himalayas remote jobs API (https://himalayas.app/api).
//
// Terms: use applicationLink unchanged, show Himalayas as the source, and
// don't redistribute to other boards. Data refreshes daily, so we cache for
// at least 6h.
package himalayas

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources"
)

const DefaultBaseURL = "https://himalayas.app/jobs/api/search"

type Source struct {
	BaseURL  string
	MaxPages int
	Client   *sources.Client
}

func New(maxPages int, client *sources.Client) *Source {
	if maxPages <= 0 {
		maxPages = 5 // 20 jobs per page
	}
	return &Source{BaseURL: DefaultBaseURL, MaxPages: maxPages, Client: client}
}

func (s *Source) Name() string                  { return "himalayas" }
func (s *Source) Supports(f model.Filters) bool { return f.WantsMode(model.ModeRemote) }
func (s *Source) Rate() rate.Limit              { return rate.Every(time.Second) }
func (s *Source) DefaultTTL() time.Duration     { return 6 * time.Hour }

func (s *Source) CacheKey(f model.Filters) string {
	var qs []string
	for _, q := range s.queries(f) {
		qs = append(qs, q.Encode())
	}
	return strings.Join(qs, "|")
}

var employmentTypes = map[model.JobType]string{
	model.TypeInternship: "Intern",
	model.TypeFullTime:   "Full Time",
	model.TypePartTime:   "Part Time",
	model.TypeContract:   "Contractor",
}

// queries returns one query per requested country (Himalayas includes
// worldwide jobs in country searches by default).
func (s *Source) queries(f model.Filters) []url.Values {
	base := url.Values{}
	// Only explicit keywords go server-side: q matches literal text, so
	// q=backend would miss "Java Intern". Areas are classified locally.
	if len(f.Keywords) > 0 {
		base.Set("q", strings.Join(f.Keywords, " "))
	}
	base.Set("sort", "recent")
	var types []string
	for _, t := range f.Types {
		if et, ok := employmentTypes[t]; ok {
			types = append(types, et)
		}
		if t == model.TypeNewGrad {
			types = append(types, "Full Time")
		}
	}
	if len(types) > 0 {
		base.Set("employment_type", strings.Join(types, ","))
	}
	var out []url.Values
	for _, c := range f.Countries {
		q := cloneValues(base)
		if c == model.RemoteGlobal {
			q.Set("worldwide", "true")
		} else {
			q.Set("country", c)
		}
		out = append(out, q)
	}
	if len(out) == 0 {
		out = append(out, base)
	}
	return out
}

func cloneValues(v url.Values) url.Values {
	c := url.Values{}
	for k, vs := range v {
		c[k] = append([]string(nil), vs...)
	}
	return c
}

type response struct {
	TotalCount int   `json:"totalCount"`
	Jobs       []job `json:"jobs"`
}

type job struct {
	Title                string            `json:"title"`
	Excerpt              string            `json:"excerpt"`
	CompanyName          string            `json:"companyName"`
	EmploymentType       string            `json:"employmentType"`
	MinSalary            sources.FlexFloat `json:"minSalary"`
	MaxSalary            sources.FlexFloat `json:"maxSalary"`
	SalaryPeriod         string            `json:"salaryPeriod"`
	Currency             string            `json:"currency"`
	Seniority            []string          `json:"seniority"`
	LocationRestrictions []string          `json:"locationRestrictions"`
	Categories           []string          `json:"categories"`
	Description          string            `json:"description"`
	PubDate              int64             `json:"pubDate"`
	ApplicationLink      string            `json:"applicationLink"`
	GUID                 string            `json:"guid"`
}

func (s *Source) Fetch(ctx context.Context, f model.Filters) ([]model.Job, error) {
	var all []model.Job
	seen := map[string]bool{}
	queries := s.queries(f)
	var firstErr error
	failed := 0
	for _, q := range queries {
		for page := 1; page <= s.MaxPages; page++ {
			q.Set("page", strconv.Itoa(page))
			var resp response
			if err := s.Client.GetJSON(ctx, s.BaseURL+"?"+q.Encode(), nil, &resp); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				failed++
				break
			}
			for _, j := range parse(resp) {
				if !seen[j.ID] {
					seen[j.ID] = true
					all = append(all, j)
				}
			}
			if page*20 >= resp.TotalCount || len(resp.Jobs) == 0 {
				break
			}
		}
	}
	if firstErr != nil {
		if len(all) == 0 {
			return nil, firstErr
		}
		return all, &sources.PartialError{Failed: []string{"some queries"}, Total: len(queries), Err: firstErr}
	}
	return all, nil
}

func parse(resp response) []model.Job {
	jobs := make([]model.Job, 0, len(resp.Jobs))
	for _, r := range resp.Jobs {
		j := model.Job{
			ID:          "himalayas:" + strings.TrimPrefix(r.GUID, "https://himalayas.app/companies/"),
			Source:      "himalayas",
			Title:       sources.CleanText(r.Title),
			Company:     sources.CleanText(r.CompanyName),
			URL:         r.ApplicationLink, // terms: use unchanged
			Location:    strings.Join(r.LocationRestrictions, ", "),
			WorkMode:    model.ModeRemote,
			PostedAt:    time.Unix(r.PubDate, 0).UTC(),
			Description: sources.Truncate(sources.StripHTML(r.Description), 6000),
			Tags:        r.Categories,
		}
		if j.URL == "" {
			j.URL = r.GUID
		}
		// No restrictions means hire-from-anywhere on Himalayas.
		if len(r.LocationRestrictions) == 0 {
			j.Countries = []string{model.RemoteGlobal}
		}
		for _, name := range r.LocationRestrictions {
			if c, ok := model.NormalizeCountry(name); ok {
				j.Countries = append(j.Countries, c)
			} else if cs, _ := model.ParseLocation(name); len(cs) > 0 {
				j.Countries = append(j.Countries, cs...)
			}
		}
		switch r.EmploymentType {
		case "Intern":
			j.JobType = model.TypeInternship
		case "Full Time":
			j.JobType = model.TypeFullTime
		case "Part Time":
			j.JobType = model.TypePartTime
		case "Contractor", "Temporary":
			j.JobType = model.TypeContract
		default:
			j.JobType = model.TypeUnknown
		}
		j.Experience = seniority(r.Seniority, j.JobType)
		if (r.SalaryPeriod == "" || r.SalaryPeriod == "annual") && (r.MinSalary > 0 || r.MaxSalary > 0) {
			j.SalaryMin, j.SalaryMax = sources.Float(float64(r.MinSalary)), sources.Float(float64(r.MaxSalary))
			j.Currency = r.Currency
		}
		jobs = append(jobs, j)
	}
	return jobs
}

func seniority(levels []string, t model.JobType) model.Experience {
	if t == model.TypeInternship {
		return model.ExpIntern
	}
	// With several levels listed, report the lowest.
	best := model.ExpUnknown
	rank := map[model.Experience]int{model.ExpEntry: 1, model.ExpMid: 2, model.ExpSenior: 3, model.ExpUnknown: 9}
	for _, l := range levels {
		var e model.Experience
		switch l {
		case "Entry-level":
			e = model.ExpEntry
		case "Mid-level":
			e = model.ExpMid
		case "Senior", "Manager", "Director", "Executive":
			e = model.ExpSenior
		default:
			continue
		}
		if rank[e] < rank[best] {
			best = e
		}
	}
	return best
}

func (s *Source) SearchURL(f model.Filters) string {
	return "https://himalayas.app/jobs?q=" + url.QueryEscape(sources.Phrase(f))
}
