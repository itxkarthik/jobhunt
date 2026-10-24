// Package remoteok reads the public Remote OK API (https://remoteok.com/api).
//
// Terms (from the API's legal notice): link back to the Remote OK job URL and
// mention Remote OK as the source. The first array element is that notice,
// not a job.
package remoteok

import (
	"context"
	"net/url"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources"
)

const DefaultBaseURL = "https://remoteok.com/api"

type Source struct {
	BaseURL string
	Client  *sources.Client
}

func New(client *sources.Client) *Source {
	return &Source{BaseURL: DefaultBaseURL, Client: client}
}

func (s *Source) Name() string                  { return "remoteok" }
func (s *Source) Supports(f model.Filters) bool { return f.WantsMode(model.ModeRemote) }
func (s *Source) CacheKey(model.Filters) string { return "all" }
func (s *Source) Rate() rate.Limit              { return rate.Every(5 * time.Second) }

type item struct {
	ID          sources.FlexString `json:"id"`
	Epoch       int64              `json:"epoch"`
	Date        string             `json:"date"`
	Company     string             `json:"company"`
	Position    string             `json:"position"`
	Tags        []string           `json:"tags"`
	Description string             `json:"description"`
	Location    string             `json:"location"`
	SalaryMin   sources.FlexFloat  `json:"salary_min"`
	SalaryMax   sources.FlexFloat  `json:"salary_max"`
	URL         string             `json:"url"`
}

func (s *Source) Fetch(ctx context.Context, _ model.Filters) ([]model.Job, error) {
	var items []item
	if err := s.Client.GetJSON(ctx, s.BaseURL, nil, &items); err != nil {
		return nil, err
	}
	return parse(items), nil
}

func parse(items []item) []model.Job {
	jobs := make([]model.Job, 0, len(items))
	for _, it := range items {
		if it.ID == "" || it.Position == "" {
			continue // legal notice / malformed
		}
		countries, city := model.ParseLocation(it.Location)
		j := model.Job{
			ID:          "remoteok:" + string(it.ID),
			Source:      "remoteok",
			Title:       sources.CleanText(it.Position),
			Company:     sources.CleanText(it.Company),
			URL:         it.URL,
			Countries:   countries,
			City:        city,
			Location:    it.Location,
			WorkMode:    model.ModeRemote,
			JobType:     model.TypeUnknown,
			Experience:  model.ExpUnknown,
			Description: sources.Truncate(sources.StripHTML(it.Description), 6000),
			Tags:        it.Tags,
		}
		if it.Epoch > 0 {
			j.PostedAt = time.Unix(it.Epoch, 0).UTC()
		} else {
			j.PostedAt = sources.ParseTime(it.Date)
		}
		if it.SalaryMin > 0 || it.SalaryMax > 0 {
			j.SalaryMin, j.SalaryMax = sources.Float(float64(it.SalaryMin)), sources.Float(float64(it.SalaryMax))
			j.Currency = "USD"
		}
		jobs = append(jobs, j)
	}
	return jobs
}

func (s *Source) SearchURL(f model.Filters) string {
	q := "dev"
	if len(f.Keywords) > 0 {
		q = strings.ToLower(f.Keywords[0])
	} else if len(f.Areas) > 0 {
		q = string(f.Areas[0])
	}
	return "https://remoteok.com/remote-" + url.PathEscape(q) + "-jobs"
}
