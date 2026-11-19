// Package hn reads the latest Hacker News "Ask HN: Who is hiring?" thread via
// the Algolia HN API (https://hn.algolia.com/api). Each top-level comment is
// one posting; by convention its first line is "Company | Role | Location | …".
package hn

import (
	"context"
	"fmt"
	"html"
	"regexp"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources"
)

const DefaultBaseURL = "https://hn.algolia.com/api/v1"

type Source struct {
	BaseURL string
	Client  *sources.Client
}

func New(client *sources.Client) *Source {
	return &Source{BaseURL: DefaultBaseURL, Client: client}
}

func (s *Source) Name() string                  { return "hn" }
func (s *Source) Supports(model.Filters) bool   { return true }
func (s *Source) CacheKey(model.Filters) string { return "latest" }
func (s *Source) Rate() rate.Limit              { return rate.Limit(2) }
func (s *Source) DefaultTTL() time.Duration     { return 6 * time.Hour }

type searchResp struct {
	Hits []struct {
		ObjectID string `json:"objectID"`
		Title    string `json:"title"`
	} `json:"hits"`
}

type item struct {
	ID        int64  `json:"id"`
	CreatedAt string `json:"created_at"`
	Author    string `json:"author"`
	Text      string `json:"text"`
	Title     string `json:"title"`
	Children  []item `json:"children"`
}

func (s *Source) Fetch(ctx context.Context, _ model.Filters) ([]model.Job, error) {
	t, err := s.Latest(ctx)
	if err != nil {
		return nil, err
	}
	return parse(t.item), nil
}

var (
	headerSplit = regexp.MustCompile(`\s*(?:\||—|–|\s-\s)\s*`)
	parenRe     = regexp.MustCompile(`\s*\(([^)]*)\)`)
	roleRe      = regexp.MustCompile(`(?i)\b(engineer|engineers|engineering|developer|developers|dev|intern|interns|internship|scientist|swe|sre|sde|designer|architect|researcher|analyst|programmer|devops|founding|cto|head of|lead|mlops|ml|ai|backend|frontend|full[- ]?stack|platform|infrastructure|security|data|mobile|ios|android|roles?|positions?)\b`)
	salaryRe    = regexp.MustCompile(`[$€£¥₹]\s?\d`)
	urlRe       = regexp.MustCompile(`^https?://`)
)

func parse(thread item) []model.Job {
	jobs := make([]model.Job, 0, len(thread.Children))
	for _, c := range thread.Children {
		if c.Text == "" || c.Author == "" {
			continue // deleted/dead
		}
		header := c.Text
		if i := strings.Index(header, "<p>"); i >= 0 {
			header = header[:i]
		}
		header = sources.CleanText(html.UnescapeString(header))
		if header == "" {
			continue
		}
		company, title := splitHeader(header)
		countries, city := model.ParseLocation(header)
		j := model.Job{
			ID:          fmt.Sprintf("hn:%d", c.ID),
			Source:      "hn",
			Title:       title,
			Company:     company,
			URL:         fmt.Sprintf("https://news.ycombinator.com/item?id=%d", c.ID),
			Countries:   countries,
			City:        city,
			Location:    sources.Truncate(header, 200),
			WorkMode:    model.ModeUnknown,
			JobType:     model.TypeUnknown,
			Experience:  model.ExpUnknown,
			PostedAt:    sources.ParseTime(c.CreatedAt),
			Description: sources.Truncate(sources.StripHTML(c.Text), 6000),
			Tags:        []string{"hn-who-is-hiring"},
		}
		jobs = append(jobs, j)
	}
	return jobs
}

// splitHeader extracts company and role(s) from a "Company | Role | …" line.
func splitHeader(header string) (company, title string) {
	segs := headerSplit.Split(header, -1)
	company = strings.TrimSpace(parenRe.ReplaceAllString(segs[0], ""))
	var roles []string
	for _, s := range segs[1:] {
		s = strings.TrimSpace(s)
		if s == "" || urlRe.MatchString(s) || salaryRe.MatchString(s) {
			continue
		}
		if roleRe.MatchString(s) && !isModeOnly(s) {
			roles = append(roles, s)
		}
	}
	title = strings.Join(roles, " / ")
	if title == "" {
		title = "Multiple roles (see post)"
	}
	return sources.Truncate(company, 60), sources.Truncate(title, 120)
}

// isModeOnly drops segments like "REMOTE" or "Onsite (SF)" that match no role.
func isModeOnly(s string) bool {
	l := strings.ToLower(s)
	for _, w := range []string{"remote", "onsite", "on-site", "hybrid", "full-time", "full time", "part-time", "visa"} {
		if strings.HasPrefix(l, w) {
			return true
		}
	}
	return false
}

func (s *Source) SearchURL(f model.Filters) string {
	return "https://hn.algolia.com/?dateRange=pastMonth&type=comment&query=" + strings.ReplaceAll(sources.Phrase(f), " ", "%20")
}
