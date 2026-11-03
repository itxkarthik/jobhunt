// Package jsearch queries the JSearch API, which returns Google for Jobs
// results — including LinkedIn, Indeed, and Glassdoor listings — without
// scraping those sites. Freemium; off by default.
//
// It works with either kind of key: an OpenWeb Ninja key ("ak_…", sent
// directly to api.openwebninja.com) or a RapidAPI key (sent through the
// RapidAPI gateway). Both return the same response.
package jsearch

import (
	"context"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources"
)

const (
	RapidAPIBaseURL     = "https://jsearch.p.rapidapi.com/search"
	OpenWebNinjaBaseURL = "https://api.openwebninja.com/jsearch/search"
	rapidAPIHost        = "jsearch.p.rapidapi.com"
	// openWebNinjaPrefix marks keys issued by OpenWeb Ninja itself.
	openWebNinjaPrefix = "ak_"
)

type Source struct {
	BaseURL  string
	APIKey   string
	Direct   bool // OpenWeb Ninja key: x-api-key header instead of RapidAPI's
	NumPages int
	Client   *sources.Client
}

func New(apiKey string, numPages int, client *sources.Client) *Source {
	if numPages <= 0 {
		numPages = 1 // each page costs one request against the free quota
	}
	s := &Source{BaseURL: RapidAPIBaseURL, APIKey: apiKey, NumPages: numPages, Client: client}
	if strings.HasPrefix(apiKey, openWebNinjaPrefix) {
		s.BaseURL, s.Direct = OpenWebNinjaBaseURL, true
	}
	return s
}

func (s *Source) headers() map[string]string {
	if s.Direct {
		return map[string]string{"x-api-key": s.APIKey}
	}
	return map[string]string{"X-RapidAPI-Key": s.APIKey, "X-RapidAPI-Host": rapidAPIHost}
}

func (s *Source) Name() string                { return "jsearch" }
func (s *Source) Supports(model.Filters) bool { return true }
func (s *Source) Rate() rate.Limit            { return rate.Every(time.Second) }
func (s *Source) DefaultTTL() time.Duration   { return 6 * time.Hour }
func (s *Source) CacheKey(f model.Filters) string {
	var qs []string
	for _, q := range s.queries(f) {
		qs = append(qs, q.Encode())
	}
	return strings.Join(qs, "|")
}

func (s *Source) Ready() error {
	if s.APIKey == "" {
		return sources.ErrNotConfigured{Msg: "missing jsearch api_key (OpenWeb Ninja or RapidAPI; set JOBHUNT_JSEARCH_API_KEY)"}
	}
	return nil
}

var empTypes = map[model.JobType]string{
	model.TypeInternship: "INTERN",
	model.TypeFullTime:   "FULLTIME",
	model.TypePartTime:   "PARTTIME",
	model.TypeContract:   "CONTRACTOR",
	model.TypeNewGrad:    "FULLTIME",
}

func (s *Source) queries(f model.Filters) []url.Values {
	base := url.Values{}
	base.Set("page", "1")
	base.Set("num_pages", strconv.Itoa(s.NumPages))
	var types []string
	for _, t := range f.Types {
		if et, ok := empTypes[t]; ok && !contains(types, et) {
			types = append(types, et)
		}
	}
	if len(types) > 0 {
		base.Set("employment_types", strings.Join(types, ","))
	}
	if f.OnlyRemote() {
		base.Set("work_from_home", "true")
	}
	if d := datePosted(f.PostedWithin); d != "" {
		base.Set("date_posted", d)
	}
	var out []url.Values
	for _, c := range f.Countries {
		q := cloneValues(base)
		if c == model.RemoteGlobal {
			q.Set("query", sources.Phrase(f)+" remote")
			q.Set("work_from_home", "true")
		} else {
			q.Set("query", sources.Phrase(f)+" in "+sources.CountryName(c))
			q.Set("country", strings.ToLower(c))
		}
		out = append(out, q)
	}
	if len(out) == 0 {
		q := cloneValues(base)
		q.Set("query", sources.Phrase(f))
		out = append(out, q)
	}
	return out
}

func datePosted(d time.Duration) string {
	switch {
	case d <= 0:
		return ""
	case d <= 24*time.Hour:
		return "today"
	case d <= 3*24*time.Hour:
		return "3days"
	case d <= 7*24*time.Hour:
		return "week"
	case d <= 31*24*time.Hour:
		return "month"
	}
	return ""
}

type response struct {
	Status string `json:"status"`
	Data   []struct {
		JobID          sources.FlexString `json:"job_id"`
		EmployerName   string             `json:"employer_name"`
		Publisher      string             `json:"job_publisher"`
		EmploymentType string             `json:"job_employment_type"`
		Title          string             `json:"job_title"`
		ApplyLink      string             `json:"job_apply_link"`
		Description    string             `json:"job_description"`
		IsRemote       bool               `json:"job_is_remote"`
		PostedAtUTC    string             `json:"job_posted_at_datetime_utc"`
		PostedAtUnix   int64              `json:"job_posted_at_timestamp"`
		City           string             `json:"job_city"`
		State          string             `json:"job_state"`
		Country        string             `json:"job_country"`
		MinSalary      sources.FlexFloat  `json:"job_min_salary"`
		MaxSalary      sources.FlexFloat  `json:"job_max_salary"`
		SalaryCurrency string             `json:"job_salary_currency"`
		SalaryPeriod   string             `json:"job_salary_period"`
	} `json:"data"`
}

func (s *Source) Fetch(ctx context.Context, f model.Filters) ([]model.Job, error) {
	headers := s.headers()
	var all []model.Job
	seen := map[string]bool{}
	queries := s.queries(f)
	for _, q := range queries {
		var resp response
		if err := s.Client.GetJSON(ctx, s.BaseURL+"?"+q.Encode(), headers, &resp); err != nil {
			if len(all) > 0 {
				return all, &sources.PartialError{Failed: []string{q.Get("query")}, Total: len(queries), Err: err}
			}
			return nil, err
		}
		for _, j := range parse(resp) {
			if !seen[j.ID] {
				seen[j.ID] = true
				all = append(all, j)
			}
		}
	}
	return all, nil
}

func parse(resp response) []model.Job {
	jobs := make([]model.Job, 0, len(resp.Data))
	for _, r := range resp.Data {
		loc := strings.Join(nonEmpty(r.City, r.State, r.Country), ", ")
		countries, city := model.ParseLocation(loc)
		if c := strings.ToUpper(r.Country); len(c) == 2 && !contains(countries, c) {
			countries = append([]string{c}, countries...)
		}
		if city == "" {
			city = r.City
		}
		j := model.Job{
			ID:          "jsearch:" + string(r.JobID),
			Source:      "jsearch",
			Title:       sources.CleanText(r.Title),
			Company:     sources.CleanText(r.EmployerName),
			URL:         r.ApplyLink,
			Countries:   countries,
			City:        city,
			Location:    loc,
			WorkMode:    model.ModeUnknown,
			JobType:     empType(r.EmploymentType),
			Experience:  model.ExpUnknown,
			PostedAt:    postedAt(r.PostedAtUTC, r.PostedAtUnix),
			Description: sources.Truncate(r.Description, 6000),
		}
		if r.IsRemote {
			j.WorkMode = model.ModeRemote
		}
		if r.Publisher != "" {
			j.Tags = []string{"via " + r.Publisher}
		}
		if strings.EqualFold(r.SalaryPeriod, "YEAR") && (r.MinSalary > 0 || r.MaxSalary > 0) {
			j.SalaryMin, j.SalaryMax = sources.Float(float64(r.MinSalary)), sources.Float(float64(r.MaxSalary))
			j.Currency = r.SalaryCurrency
		}
		jobs = append(jobs, j)
	}
	return jobs
}

// empType handles both "INTERN" and "Internship"-style values.
func empType(s string) model.JobType {
	l := strings.ToLower(strings.ReplaceAll(s, "-", ""))
	switch {
	case strings.Contains(l, "intern"):
		return model.TypeInternship
	case strings.Contains(l, "parttime") || strings.Contains(l, "part time"):
		return model.TypePartTime
	case strings.Contains(l, "contract"):
		return model.TypeContract
	case strings.Contains(l, "fulltime") || strings.Contains(l, "full time"):
		return model.TypeFullTime
	}
	return model.TypeUnknown
}

func nonEmpty(xs ...string) []string {
	var out []string
	for _, x := range xs {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

func contains(xs []string, x string) bool {
	return slices.Contains(xs, x)
}

func cloneValues(v url.Values) url.Values {
	c := url.Values{}
	for k, vs := range v {
		c[k] = append([]string(nil), vs...)
	}
	return c
}

func (s *Source) SearchURL(f model.Filters) string {
	q := sources.Phrase(f) + " jobs"
	if len(f.Countries) > 0 && f.Countries[0] != model.RemoteGlobal {
		q += " " + sources.CountryName(f.Countries[0])
	}
	return "https://www.google.com/search?ibp=htl;jobs&q=" + url.QueryEscape(q)
}

// postedAt prefers the datetime string and falls back to the Unix
// timestamp, which JSearch sometimes sends alone.
func postedAt(utc string, unix int64) time.Time {
	if t := sources.ParseTime(utc); !t.IsZero() {
		return t
	}
	if unix > 0 {
		return time.Unix(unix, 0).UTC()
	}
	return time.Time{}
}
