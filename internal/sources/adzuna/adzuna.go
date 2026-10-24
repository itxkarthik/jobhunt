// Package adzuna queries the Adzuna job search API (https://developer.adzuna.com).
package adzuna

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/time/rate"

	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources"
)

const DefaultBaseURL = "https://api.adzuna.com/v1/api/jobs"

// countries maps filter codes to Adzuna country paths. Adzuna has no JP or AE.
var countries = map[string]string{
	"IN": "in", "US": "us", "GB": "gb", "DE": "de", "CA": "ca",
	"SG": "sg", "NL": "nl", "AU": "au",
}

// consumer sites, used for browser fallback links.
var sites = map[string]string{
	"in": "www.adzuna.in", "us": "www.adzuna.com", "gb": "www.adzuna.co.uk", "de": "www.adzuna.de",
	"ca": "www.adzuna.ca", "sg": "www.adzuna.sg", "nl": "www.adzuna.nl", "au": "www.adzuna.com.au",
}

type Source struct {
	BaseURL  string
	AppID    string
	AppKey   string
	MaxPages int
	Client   *sources.Client
}

func New(appID, appKey string, maxPages int, client *sources.Client) *Source {
	if maxPages <= 0 {
		maxPages = 2
	}
	return &Source{BaseURL: DefaultBaseURL, AppID: appID, AppKey: appKey, MaxPages: maxPages, Client: client}
}

func (s *Source) Name() string { return "adzuna" }

func (s *Source) Ready() error {
	if s.AppID == "" || s.AppKey == "" {
		return sources.ErrNotConfigured{Msg: "missing adzuna app_id/app_key (free at https://developer.adzuna.com/signup; set JOBHUNT_ADZUNA_APP_ID/APP_KEY)"}
	}
	return nil
}

func (s *Source) Supports(f model.Filters) bool {
	if f.OnlyRemote() {
		return false // Adzuna can't filter to remote; remote boards cover that
	}
	return len(s.targets(f)) > 0
}

func (s *Source) targets(f model.Filters) []string {
	var out []string
	for _, c := range f.Countries {
		if cc, ok := countries[c]; ok {
			out = append(out, cc)
		}
	}
	return out
}

// query builds the free-text part. Adzuna's what_or is OR-ed; we put the
// most selective dimension there and let the central filter do the rest.
func query(f model.Filters) (whatOr string) {
	switch {
	case len(f.Keywords) > 0:
		return strings.Join(f.Keywords, " ")
	case f.WantsType(model.TypeInternship):
		return "intern internship trainee"
	case f.WantsType(model.TypeNewGrad):
		return "graduate fresher junior entry"
	case len(f.Areas) > 0:
		var terms []string
		for _, a := range f.Areas {
			terms = append(terms, areaTerms[a]...)
		}
		return strings.Join(terms, " ")
	}
	return "software developer engineer"
}

var areaTerms = map[model.Area][]string{
	model.AreaBackend:    {"backend", "back-end", "golang", "java", "api"},
	model.AreaFrontend:   {"frontend", "front-end", "react", "javascript"},
	model.AreaFullStack:  {"fullstack", "full-stack"},
	model.AreaMobile:     {"android", "ios", "mobile", "flutter"},
	model.AreaMLAI:       {"machine learning", "ml", "ai", "deep learning"},
	model.AreaDataEng:    {"data engineer", "etl", "spark"},
	model.AreaDataSci:    {"data scientist", "analytics"},
	model.AreaDevOps:     {"devops", "sre", "cloud", "kubernetes"},
	model.AreaSystems:    {"systems", "infrastructure", "distributed"},
	model.AreaHPC:        {"cuda", "gpu", "hpc"},
	model.AreaSecurity:   {"security", "cybersecurity"},
	model.AreaEmbedded:   {"embedded", "firmware"},
	model.AreaQA:         {"qa", "test", "sdet"},
	model.AreaResearch:   {"research"},
	model.AreaBlockchain: {"blockchain", "web3", "solidity"},
	model.AreaGameDev:    {"game", "unity", "unreal"},
}

func (s *Source) CacheKey(f model.Filters) string {
	return fmt.Sprintf("%v|%s|%d", s.targets(f), query(f), int(f.PostedWithin.Hours()/24))
}

func (s *Source) Rate() rate.Limit { return rate.Limit(2) }

func (s *Source) Fetch(ctx context.Context, f model.Filters) ([]model.Job, error) {
	var all []model.Job
	for _, cc := range s.targets(f) {
		for page := 1; page <= s.MaxPages; page++ {
			jobs, total, err := s.fetchPage(ctx, cc, page, f)
			if err != nil {
				if len(all) > 0 {
					return all, nil // keep partial results
				}
				return nil, err
			}
			all = append(all, jobs...)
			if page*50 >= total || len(jobs) == 0 {
				break
			}
		}
	}
	return all, nil
}

func (s *Source) fetchPage(ctx context.Context, cc string, page int, f model.Filters) ([]model.Job, int, error) {
	q := url.Values{}
	q.Set("app_id", s.AppID)
	q.Set("app_key", s.AppKey)
	q.Set("results_per_page", "50")
	q.Set("what_or", query(f))
	q.Set("category", "it-jobs")
	q.Set("sort_by", "date")
	if d := int(f.PostedWithin.Hours() / 24); d > 0 {
		q.Set("max_days_old", strconv.Itoa(d))
	}
	u := fmt.Sprintf("%s/%s/search/%d?%s", s.BaseURL, cc, page, q.Encode())
	var resp response
	if err := s.Client.GetJSON(ctx, u, nil, &resp); err != nil {
		return nil, 0, err
	}
	return parse(resp, cc), resp.Count, nil
}

type response struct {
	Count   int      `json:"count"`
	Results []result `json:"results"`
}

type result struct {
	ID           string  `json:"id"`
	Title        string  `json:"title"`
	Description  string  `json:"description"`
	Created      string  `json:"created"`
	RedirectURL  string  `json:"redirect_url"`
	SalaryMin    float64 `json:"salary_min"`
	SalaryMax    float64 `json:"salary_max"`
	Predicted    string  `json:"salary_is_predicted"`
	ContractTime string  `json:"contract_time"`
	ContractType string  `json:"contract_type"`
	Company      struct {
		DisplayName string `json:"display_name"`
	} `json:"company"`
	Location struct {
		DisplayName string   `json:"display_name"`
		Area        []string `json:"area"`
	} `json:"location"`
	Category struct {
		Tag   string `json:"tag"`
		Label string `json:"label"`
	} `json:"category"`
}

func parse(resp response, cc string) []model.Job {
	country := strings.ToUpper(cc)
	jobs := make([]model.Job, 0, len(resp.Results))
	for _, r := range resp.Results {
		j := model.Job{
			ID:          "adzuna:" + r.ID,
			Source:      "adzuna",
			Title:       sources.CleanText(r.Title),
			Company:     sources.CleanText(r.Company.DisplayName),
			URL:         r.RedirectURL,
			Countries:   []string{country},
			Location:    r.Location.DisplayName,
			WorkMode:    model.ModeUnknown,
			JobType:     model.TypeUnknown,
			Experience:  model.ExpUnknown,
			PostedAt:    sources.ParseTime(r.Created),
			Description: sources.StripHTML(r.Description),
		}
		_, j.City = model.ParseLocation(r.Location.DisplayName)
		if j.City == "" && len(r.Location.Area) > 2 {
			j.City = r.Location.Area[len(r.Location.Area)-1]
		}
		// Adzuna's predicted salaries are estimates; only keep advertised ones.
		if r.Predicted != "1" {
			j.SalaryMin, j.SalaryMax = sources.Float(r.SalaryMin), sources.Float(r.SalaryMax)
			if j.SalaryMin != nil || j.SalaryMax != nil {
				j.Currency = model.CurrencyFor(country)
			}
		}
		switch {
		case r.ContractType == "contract":
			j.JobType = model.TypeContract
		case r.ContractTime == "part_time":
			j.JobType = model.TypePartTime
		case r.ContractTime == "full_time":
			j.JobType = model.TypeFullTime
		}
		if r.Category.Label != "" {
			j.Tags = []string{r.Category.Label}
		}
		jobs = append(jobs, j)
	}
	return jobs
}

func (s *Source) SearchURL(f model.Filters) string {
	t := s.targets(f)
	cc := "in"
	if len(t) > 0 {
		cc = t[0]
	}
	return fmt.Sprintf("https://%s/search?q=%s", sites[cc], url.QueryEscape(query(f)))
}
