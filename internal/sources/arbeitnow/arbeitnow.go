// Package arbeitnow reads the free Arbeitnow job board API
// (https://www.arbeitnow.com/blog/job-board-api). Listings are mostly in
// Germany/EU; the API asks callers not to abuse it and to link back.
package arbeitnow

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources"
)

const DefaultBaseURL = "https://www.arbeitnow.com/api/job-board-api"

type Source struct {
	BaseURL  string
	MaxPages int
	Client   *sources.Client
}

func New(maxPages int, client *sources.Client) *Source {
	if maxPages <= 0 {
		maxPages = 2 // 300 jobs per page
	}
	return &Source{BaseURL: DefaultBaseURL, MaxPages: maxPages, Client: client}
}

func (s *Source) Name() string                  { return "arbeitnow" }
func (s *Source) CacheKey(model.Filters) string { return fmt.Sprintf("pages=%d", s.MaxPages) }
func (s *Source) Rate() rate.Limit              { return rate.Every(2 * time.Second) }

// Supports only searches that could include European jobs, so an India-only
// search doesn't download 4MB of German listings.
func (s *Source) Supports(f model.Filters) bool {
	if len(f.Countries) == 0 {
		return true
	}
	for _, c := range f.Countries {
		if c == model.RemoteGlobal || model.RegionContains(model.RegionEurope, c) {
			return true
		}
	}
	return false
}

type response struct {
	Data []struct {
		Slug        string   `json:"slug"`
		CompanyName string   `json:"company_name"`
		Title       string   `json:"title"`
		Description string   `json:"description"`
		Remote      bool     `json:"remote"`
		URL         string   `json:"url"`
		Tags        []string `json:"tags"`
		JobTypes    []string `json:"job_types"`
		Location    string   `json:"location"`
		CreatedAt   int64    `json:"created_at"`
	} `json:"data"`
	Links struct {
		Next *string `json:"next"`
	} `json:"links"`
}

func (s *Source) Fetch(ctx context.Context, _ model.Filters) ([]model.Job, error) {
	var all []model.Job
	for page := 1; page <= s.MaxPages; page++ {
		var resp response
		if err := s.Client.GetJSON(ctx, fmt.Sprintf("%s?page=%d", s.BaseURL, page), nil, &resp); err != nil {
			if len(all) > 0 {
				return all, &sources.PartialError{Failed: []string{fmt.Sprintf("page %d", page)}, Total: s.MaxPages, Err: err}
			}
			return nil, err
		}
		all = append(all, parse(resp)...)
		if resp.Links.Next == nil || len(resp.Data) == 0 {
			break
		}
	}
	return all, nil
}

func parse(resp response) []model.Job {
	jobs := make([]model.Job, 0, len(resp.Data))
	for _, r := range resp.Data {
		countries, city := model.ParseLocation(r.Location)
		if len(countries) == 0 {
			// German board: unlabeled locations are German towns.
			countries = []string{"DE"}
			if city == "" {
				city = r.Location
			}
		}
		j := model.Job{
			ID:          "arbeitnow:" + r.Slug,
			Source:      "arbeitnow",
			Title:       sources.CleanText(r.Title),
			Company:     sources.CleanText(r.CompanyName),
			URL:         r.URL,
			Countries:   countries,
			City:        city,
			Location:    r.Location,
			WorkMode:    model.ModeUnknown,
			PostedAt:    time.Unix(r.CreatedAt, 0).UTC(),
			Description: sources.Truncate(sources.StripHTML(r.Description), 6000),
			Tags:        r.Tags,
		}
		if r.Remote {
			j.WorkMode = model.ModeRemote
		}
		j.JobType, j.Experience = mapJobTypes(r.JobTypes)
		jobs = append(jobs, j)
	}
	return jobs
}

// mapJobTypes interprets Arbeitnow's mixed English/German job_types labels.
func mapJobTypes(types []string) (model.JobType, model.Experience) {
	t, e := model.TypeUnknown, model.ExpUnknown
	for _, raw := range types {
		s := strings.ToLower(raw)
		switch {
		case strings.Contains(s, "internship") || strings.Contains(s, "praktikum"):
			t = model.TypeInternship
		case strings.Contains(s, "working student") || strings.Contains(s, "werkstudent"):
			if t == model.TypeUnknown {
				t = model.TypePartTime
			}
		case strings.Contains(s, "part time") || strings.Contains(s, "teilzeit"):
			if t == model.TypeUnknown {
				t = model.TypePartTime
			}
		case strings.Contains(s, "contract") && !strings.Contains(s, "arbeitsvertrag") || strings.Contains(s, "freelance"):
			if t == model.TypeUnknown {
				t = model.TypeContract
			}
		case strings.Contains(s, "full time") || strings.Contains(s, "fulltime") || strings.Contains(s, "vollzeit"):
			if t == model.TypeUnknown {
				t = model.TypeFullTime
			}
		}
		switch {
		case strings.Contains(s, "student"):
			e = model.ExpIntern
		case strings.Contains(s, "entry") || strings.Contains(s, "berufseinsteiger") || strings.Contains(s, "junior"):
			e = model.ExpEntry
		case s == "mid":
			e = model.ExpMid
		case strings.Contains(s, "senior"):
			e = model.ExpSenior
		}
	}
	if t == model.TypeInternship {
		e = model.ExpIntern
	}
	return t, e
}

func (s *Source) SearchURL(f model.Filters) string {
	q := strings.Join(f.Keywords, " ")
	if q == "" && len(f.Areas) > 0 {
		q = string(f.Areas[0])
	}
	return "https://www.arbeitnow.com/?search=" + url.QueryEscape(q)
}
