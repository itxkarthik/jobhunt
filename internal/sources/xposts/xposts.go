// Package xposts searches recent X (Twitter) posts announcing openings via
// the official API v2 recent-search endpoint. X bills per post read
// (pay-per-use, ~$0.005/post as of 2026), so this source is off by default,
// caps results per run, and caches for 6h.
package xposts

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

const DefaultBaseURL = "https://api.x.com/2/tweets/search/recent"

type Source struct {
	BaseURL     string
	BearerToken string
	MaxResults  int
	Client      *sources.Client
}

func New(token string, maxResults int, client *sources.Client) *Source {
	if maxResults <= 0 {
		maxResults = 25
	}
	// The API accepts 10..100.
	maxResults = min(max(maxResults, 10), 100)
	return &Source{BaseURL: DefaultBaseURL, BearerToken: token, MaxResults: maxResults, Client: client}
}

func (s *Source) Name() string                    { return "x" }
func (s *Source) Supports(model.Filters) bool     { return true }
func (s *Source) Rate() rate.Limit                { return rate.Every(2 * time.Second) }
func (s *Source) DefaultTTL() time.Duration       { return 6 * time.Hour }
func (s *Source) CacheKey(f model.Filters) string { return Query(f) }

func (s *Source) Ready() error {
	if s.BearerToken == "" {
		return sources.ErrNotConfigured{Msg: "missing x bearer_token (developer.x.com, pay-per-use; set JOBHUNT_X_BEARER_TOKEN)"}
	}
	return nil
}

// Query builds an X search query, e.g.
// (hiring OR "we're hiring") (intern OR internship) backend (India OR Bengaluru) -is:retweet lang:en has:links
func Query(f model.Filters) string {
	parts := []string{`(hiring OR "we're hiring" OR "now hiring" OR "join our team")`}
	switch {
	case f.WantsType(model.TypeInternship):
		parts = append(parts, "(intern OR internship OR interns)")
	case f.WantsType(model.TypeNewGrad):
		parts = append(parts, `("new grad" OR graduate OR fresher OR freshers)`)
	}
	if t := sources.Topic(f); t != "" {
		if strings.Contains(t, " ") {
			t = `"` + t + `"`
		}
		parts = append(parts, t)
	} else {
		parts = append(parts, "(engineer OR developer)")
	}
	var places []string
	remote := false
	for _, c := range f.Countries {
		switch c {
		case model.RemoteGlobal:
			remote = true
		case "IN":
			places = append(places, "India", "Bengaluru", "Bangalore")
		default:
			places = append(places, `"`+sources.CountryName(c)+`"`)
		}
	}
	if remote {
		places = append(places, "remote")
	}
	if len(places) > 0 {
		parts = append(parts, "("+strings.Join(places, " OR ")+")")
	}
	parts = append(parts, "-is:retweet -is:reply lang:en has:links")
	q := strings.Join(parts, " ")
	if len(q) > 512 { // API limit for non-enterprise
		q = q[:512]
	}
	return q
}

type response struct {
	Data []struct {
		ID        string `json:"id"`
		Text      string `json:"text"`
		AuthorID  string `json:"author_id"`
		CreatedAt string `json:"created_at"`
		Entities  struct {
			URLs []struct {
				ExpandedURL string `json:"expanded_url"`
			} `json:"urls"`
		} `json:"entities"`
	} `json:"data"`
	Includes struct {
		Users []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Username string `json:"username"`
		} `json:"users"`
	} `json:"includes"`
}

func (s *Source) Fetch(ctx context.Context, f model.Filters) ([]model.Job, error) {
	q := url.Values{}
	q.Set("query", Query(f))
	q.Set("max_results", fmt.Sprint(s.MaxResults))
	q.Set("tweet.fields", "created_at,entities,author_id")
	q.Set("expansions", "author_id")
	q.Set("user.fields", "name,username")
	var resp response
	if err := s.Client.GetJSON(ctx, s.BaseURL+"?"+q.Encode(), map[string]string{"Authorization": "Bearer " + s.BearerToken}, &resp); err != nil {
		return nil, err
	}
	return parse(resp), nil
}

func parse(resp response) []model.Job {
	type user struct{ name, username string }
	users := map[string]user{}
	for _, u := range resp.Includes.Users {
		users[u.ID] = user{u.Name, u.Username}
	}
	jobs := make([]model.Job, 0, len(resp.Data))
	for _, p := range resp.Data {
		u := users[p.AuthorID]
		link := fmt.Sprintf("https://x.com/%s/status/%s", u.username, p.ID)
		for _, e := range p.Entities.URLs {
			if e.ExpandedURL != "" && !strings.Contains(e.ExpandedURL, "x.com/") && !strings.Contains(e.ExpandedURL, "twitter.com/") {
				link = e.ExpandedURL // the posting itself, not the tweet
				break
			}
		}
		title, _, _ := strings.Cut(strings.TrimSpace(p.Text), "\n")
		countries, city := model.ParseLocation(p.Text)
		jobs = append(jobs, model.Job{
			ID:          "x:" + p.ID,
			Source:      "x",
			Title:       sources.Truncate(title, 100),
			Company:     u.name,
			URL:         link,
			Countries:   countries,
			City:        city,
			WorkMode:    model.ModeUnknown,
			JobType:     model.TypeUnknown,
			Experience:  model.ExpUnknown,
			PostedAt:    sources.ParseTime(p.CreatedAt),
			Description: p.Text + "\n\nPost: " + fmt.Sprintf("https://x.com/%s/status/%s", u.username, p.ID),
			Tags:        []string{"x-post", "@" + u.username},
		})
	}
	return jobs
}

// SearchURL is a free browser search on x.com with the same query.
func (s *Source) SearchURL(f model.Filters) string {
	return SearchURL(f)
}

func SearchURL(f model.Filters) string {
	q := strings.TrimSuffix(Query(f), " has:links")
	return "https://x.com/search?f=live&q=" + url.QueryEscape(q)
}
