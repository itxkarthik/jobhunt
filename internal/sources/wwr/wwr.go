// Package wwr reads We Work Remotely's public category RSS feeds
// (https://weworkremotely.com, robots.txt allows them).
package wwr

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources"
)

const DefaultBaseURL = "https://weworkremotely.com/categories"

// feeds maps areas to WWR category feeds.
var feeds = map[model.Area]string{
	model.AreaBackend:   "remote-back-end-programming-jobs",
	model.AreaFrontend:  "remote-front-end-programming-jobs",
	model.AreaFullStack: "remote-full-stack-programming-jobs",
	model.AreaDevOps:    "remote-devops-sysadmin-jobs",
}

const otherProgramming = "remote-programming-jobs"

type Source struct {
	BaseURL string
	Client  *sources.Client
}

func New(client *sources.Client) *Source {
	return &Source{BaseURL: DefaultBaseURL, Client: client}
}

func (s *Source) Name() string                    { return "wwr" }
func (s *Source) Supports(f model.Filters) bool   { return f.WantsMode(model.ModeRemote) }
func (s *Source) Rate() rate.Limit                { return rate.Every(2 * time.Second) }
func (s *Source) CacheKey(f model.Filters) string { return strings.Join(categories(f), ",") }

// categories picks the feeds for the requested areas. Areas without a
// dedicated feed (systems, ML, …) fall under "all other programming".
func categories(f model.Filters) []string {
	set := map[string]bool{}
	if len(f.Areas) == 0 {
		for _, c := range feeds {
			set[c] = true
		}
		set[otherProgramming] = true
	}
	for _, a := range f.Areas {
		if c, ok := feeds[a]; ok {
			set[c] = true
		} else {
			set[otherProgramming] = true
		}
	}
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

type rss struct {
	Items []item `xml:"channel>item"`
}

type item struct {
	Title       string `xml:"title"`
	Region      string `xml:"region"`
	Country     string `xml:"country"`
	State       string `xml:"state"`
	Skills      string `xml:"skills"`
	Category    string `xml:"category"`
	Type        string `xml:"type"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
	GUID        string `xml:"guid"`
	Link        string `xml:"link"`
}

func (s *Source) Fetch(ctx context.Context, f model.Filters) ([]model.Job, error) {
	var all []model.Job
	var failed []string
	var firstErr error
	cats := categories(f)
	for _, c := range cats {
		b, err := s.Client.Get(ctx, fmt.Sprintf("%s/%s.rss", s.BaseURL, c), nil)
		if err == nil {
			var jobs []model.Job
			jobs, err = parse(b)
			all = append(all, jobs...)
		}
		if err != nil {
			failed = append(failed, c)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	if firstErr != nil {
		if len(all) == 0 {
			return nil, firstErr
		}
		return all, &sources.PartialError{Failed: failed, Total: len(cats), Err: firstErr}
	}
	return all, nil
}

func parse(b []byte) ([]model.Job, error) {
	var feed rss
	if err := xml.Unmarshal(b, &feed); err != nil {
		return nil, fmt.Errorf("wwr rss: %w", err)
	}
	jobs := make([]model.Job, 0, len(feed.Items))
	for _, it := range feed.Items {
		company, title, ok := strings.Cut(it.Title, ": ")
		if !ok {
			company, title = "", it.Title
		}
		loc := strings.Join(nonEmpty(it.Region, it.Country), ", ")
		countries, city := model.ParseLocation(loc)
		link := it.Link
		if link == "" {
			link = it.GUID
		}
		j := model.Job{
			ID:          "wwr:" + strings.TrimPrefix(link, "https://weworkremotely.com/remote-jobs/"),
			Source:      "wwr",
			Title:       sources.CleanText(title),
			Company:     sources.CleanText(company),
			URL:         link,
			Countries:   countries,
			City:        city,
			Location:    loc,
			WorkMode:    model.ModeRemote,
			Experience:  model.ExpUnknown,
			PostedAt:    sources.ParseTime(it.PubDate),
			Description: sources.Truncate(sources.StripHTML(it.Description), 6000),
		}
		if it.Category != "" {
			j.Tags = append(j.Tags, it.Category)
		}
		for _, sk := range strings.Split(it.Skills, ",") {
			if sk = strings.TrimSpace(sk); sk != "" {
				j.Tags = append(j.Tags, sk)
			}
		}
		switch strings.ToLower(it.Type) {
		case "full-time":
			j.JobType = model.TypeFullTime
		case "contract":
			j.JobType = model.TypeContract
		case "part-time":
			j.JobType = model.TypePartTime
		case "internship":
			j.JobType = model.TypeInternship
		default:
			j.JobType = model.TypeUnknown
		}
		jobs = append(jobs, j)
	}
	return jobs, nil
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

func (s *Source) SearchURL(f model.Filters) string {
	return "https://weworkremotely.com/remote-jobs/search?term=" + url.QueryEscape(sources.Phrase(f))
}
