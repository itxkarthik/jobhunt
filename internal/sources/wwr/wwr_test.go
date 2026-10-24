package wwr

import (
	"context"
	"slices"
	"testing"

	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources/sourcetest"
)

func TestFetchParsesFixture(t *testing.T) {
	srv := sourcetest.Serve(t, map[string]string{"/categories/remote-back-end-programming-jobs.rss": "testdata/backend.rss"})
	s := New(sourcetest.Client())
	s.BaseURL = srv.URL + "/categories"
	jobs, err := s.Fetch(context.Background(), model.Filters{Areas: []model.Area{model.AreaBackend}})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("got %d jobs", len(jobs))
	}
	j := jobs[0]
	if j.Company != "DuckDuckGo" || j.Title != "Senior Backend Engineer" || j.JobType != model.TypeFullTime || j.WorkMode != model.ModeRemote {
		t.Errorf("job 0 = %q at %q, %s %s", j.Title, j.Company, j.JobType, j.WorkMode)
	}
	if !slices.Equal(j.Countries, []string{model.RemoteGlobal}) {
		t.Errorf("countries = %v", j.Countries)
	}
	if j.PostedAt.IsZero() || j.URL == "" || j.Description == "" {
		t.Error("missing posted/url/description")
	}
}

func TestCategories(t *testing.T) {
	tests := []struct {
		areas []model.Area
		want  []string
	}{
		{[]model.Area{model.AreaBackend}, []string{"remote-back-end-programming-jobs"}},
		{[]model.Area{model.AreaSystems, model.AreaMLAI}, []string{"remote-programming-jobs"}},
		{nil, []string{"remote-back-end-programming-jobs", "remote-devops-sysadmin-jobs", "remote-front-end-programming-jobs", "remote-full-stack-programming-jobs", "remote-programming-jobs"}},
	}
	for _, tt := range tests {
		if got := categories(model.Filters{Areas: tt.areas}); !slices.Equal(got, tt.want) {
			t.Errorf("categories(%v) = %v", tt.areas, got)
		}
	}
}

func TestPartialFailure(t *testing.T) {
	// Only the backend feed exists; the others 404.
	srv := sourcetest.Serve(t, map[string]string{"/categories/remote-back-end-programming-jobs.rss": "testdata/backend.rss"})
	s := New(sourcetest.Client())
	s.BaseURL = srv.URL + "/categories"
	jobs, err := s.Fetch(context.Background(), model.Filters{})
	if len(jobs) != 2 || err == nil {
		t.Fatalf("want 2 jobs and a partial error, got %d, %v", len(jobs), err)
	}
}
