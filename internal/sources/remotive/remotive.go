// Package remotive reads the public Remotive API (https://remotive.com/api-documentation).
//
// Terms: link back to the Remotive URL and credit Remotive as the source;
// poll at most ~4 times a day. The public feed is delayed 24h and, as of
// Oct 2026, returns only the latest handful of jobs regardless of the
// category/search parameters, so we fetch it once and filter locally.
package remotive

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

const DefaultBaseURL = "https://remotive.com/api/remote-jobs"

type Source struct {
	BaseURL string
	Client  *sources.Client
}

func New(client *sources.Client) *Source {
	return &Source{BaseURL: DefaultBaseURL, Client: client}
}

func (s *Source) Name() string { return "remotive" }

func (s *Source) Supports(f model.Filters) bool { return f.WantsMode(model.ModeRemote) }

func (s *Source) MinTTL() time.Duration { return 6 * time.Hour }

func (s *Source) Rate() rate.Limit { return rate.Every(10 * time.Second) }

func (s *Source) CacheKey(model.Filters) string { return "all" }

func (s *Source) Fetch(ctx context.Context, _ model.Filters) ([]model.Job, error) {
	var resp response
	if err := s.Client.GetJSON(ctx, s.BaseURL, nil, &resp); err != nil {
		return nil, err
	}
	return parse(resp), nil
}

type response struct {
	Jobs []struct {
		ID                        int64    `json:"id"`
		URL                       string   `json:"url"`
		Title                     string   `json:"title"`
		CompanyName               string   `json:"company_name"`
		Category                  string   `json:"category"`
		Tags                      []string `json:"tags"`
		JobType                   string   `json:"job_type"`
		PublicationDate           string   `json:"publication_date"`
		CandidateRequiredLocation string   `json:"candidate_required_location"`
		Salary                    string   `json:"salary"`
		Description               string   `json:"description"`
	} `json:"jobs"`
}

func parse(resp response) []model.Job {
	jobs := make([]model.Job, 0, len(resp.Jobs))
	for _, r := range resp.Jobs {
		countries, _ := model.ParseLocation(r.CandidateRequiredLocation)
		j := model.Job{
			ID:          "remotive:" + strconv.FormatInt(r.ID, 10),
			Source:      "remotive",
			Title:       sources.CleanText(r.Title),
			Company:     sources.CleanText(r.CompanyName),
			URL:         r.URL,
			Countries:   countries,
			Location:    r.CandidateRequiredLocation,
			WorkMode:    model.ModeRemote,
			JobType:     jobType(r.JobType),
			Experience:  model.ExpUnknown,
			PostedAt:    sources.ParseTime(r.PublicationDate),
			Description: sources.StripHTML(r.Description),
			Tags:        r.Tags,
		}
		if r.Category != "" {
			j.Tags = append([]string{r.Category}, j.Tags...)
		}
		if r.Salary != "" {
			j.Tags = append(j.Tags, "salary: "+r.Salary)
		}
		jobs = append(jobs, j)
	}
	return jobs
}

func jobType(s string) model.JobType {
	switch strings.ToLower(s) {
	case "full_time":
		return model.TypeFullTime
	case "part_time":
		return model.TypePartTime
	case "contract", "freelance":
		return model.TypeContract
	case "internship":
		return model.TypeInternship
	}
	return model.TypeUnknown
}

func (s *Source) SearchURL(f model.Filters) string {
	q := strings.Join(f.Keywords, " ")
	if q == "" && len(f.Areas) > 0 {
		q = string(f.Areas[0])
	}
	return "https://remotive.com/remote-jobs/software-development?query=" + url.QueryEscape(q)
}
