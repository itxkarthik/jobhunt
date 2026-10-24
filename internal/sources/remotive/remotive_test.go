package remotive

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"golang.org/x/time/rate"

	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources"
)

func TestFetchParsesFixture(t *testing.T) {
	fixture, err := os.ReadFile("testdata/remote-jobs.json")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ua := r.Header.Get("User-Agent"); ua != "jobhunt-test" {
			t.Errorf("User-Agent = %q", ua)
		}
		w.Write(fixture)
	}))
	defer srv.Close()

	s := New(sources.NewClient("jobhunt-test", rate.Inf))
	s.BaseURL = srv.URL
	jobs, err := s.Fetch(context.Background(), model.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 3 {
		t.Fatalf("got %d jobs", len(jobs))
	}
	tests := []struct {
		i         int
		id, title string
		jobType   model.JobType
		countries []string
	}{
		{0, "remotive:2091161", "AI Trainer - Video Annotation", model.TypeContract, []string{model.RemoteGlobal}},
		{1, "remotive:2091158", "Software Engineer, AI Coding Tasks", model.TypeContract, []string{model.RemoteGlobal}},
		{2, "remotive:2091133", "Senior React Full-stack Developer", model.TypeFullTime, []string{"CA", model.RegionEurope, model.RegionAPAC, "US"}},
	}
	for _, tt := range tests {
		j := jobs[tt.i]
		if j.ID != tt.id || j.Title != tt.title || j.JobType != tt.jobType || j.WorkMode != model.ModeRemote {
			t.Errorf("job %d = %s %q %s %s", tt.i, j.ID, j.Title, j.JobType, j.WorkMode)
		}
		if !sameSet(j.Countries, tt.countries) {
			t.Errorf("job %d countries = %v, want %v", tt.i, j.Countries, tt.countries)
		}
		if j.PostedAt.IsZero() || j.URL == "" || j.Description == "" {
			t.Errorf("job %d missing posted/url/description", tt.i)
		}
	}
}

func TestTermsLimits(t *testing.T) {
	s := New(nil)
	if s.MinTTL().Hours() < 6 {
		t.Error("Remotive asks for <= 4 fetches a day; MinTTL must be >= 6h")
	}
	if s.CacheKey(model.Filters{Keywords: []string{"a"}}) != s.CacheKey(model.Filters{}) {
		t.Error("cache key must not depend on filters (feed is filtered locally)")
	}
	if s.Supports(model.Filters{Modes: []model.WorkMode{model.ModeOnsite}}) {
		t.Error("remote-only board should not serve onsite searches")
	}
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]int{}
	for _, x := range a {
		m[x]++
	}
	for _, x := range b {
		m[x]--
	}
	for _, v := range m {
		if v != 0 {
			return false
		}
	}
	return true
}
