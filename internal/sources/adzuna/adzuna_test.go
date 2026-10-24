package adzuna

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources"
)

func TestFetchParsesFixture(t *testing.T) {
	fixture, err := os.ReadFile("testdata/search-in.json")
	if err != nil {
		t.Fatal(err)
	}
	var gotPath string
	var gotQuery map[string][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.Query()
		w.Write(fixture)
	}))
	defer srv.Close()

	s := New("id", "key", 2, sources.NewClient("test", rate.Inf))
	s.BaseURL = srv.URL
	f := model.Filters{Countries: []string{"IN"}, Types: []model.JobType{model.TypeInternship}, PostedWithin: 7 * 24 * time.Hour}
	jobs, err := s.Fetch(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/in/search/1" {
		t.Errorf("path = %q", gotPath)
	}
	for k, want := range map[string]string{"what_or": "intern internship trainee", "category": "it-jobs", "max_days_old": "7", "app_id": "id"} {
		if got := gotQuery[k]; len(got) != 1 || got[0] != want {
			t.Errorf("query %s = %v, want %q", k, got, want)
		}
	}
	if len(jobs) != 3 {
		t.Fatalf("got %d jobs, want 3 (count=3 should stop pagination)", len(jobs))
	}

	tests := []struct {
		i                        int
		id, title, company, city string
		jobType                  model.JobType
		salary                   bool
	}{
		{0, "adzuna:4911223344", "Backend Developer Intern (Golang)", "Acme Payments Pvt Ltd", "Bengaluru", model.TypeFullTime, true},
		{1, "adzuna:4911223345", "Senior Data Engineer", "DataCo", "Pune", model.TypeUnknown, false}, // predicted salary dropped
		{2, "adzuna:4911223346", "QA Contractor", "TestHouse", "", model.TypeContract, false},
	}
	for _, tt := range tests {
		j := jobs[tt.i]
		if j.ID != tt.id || j.Title != tt.title || j.Company != tt.company || j.City != tt.city || j.JobType != tt.jobType {
			t.Errorf("job %d = %+v", tt.i, j)
		}
		if (j.SalaryMin != nil) != tt.salary {
			t.Errorf("job %d salary present = %v, want %v", tt.i, j.SalaryMin != nil, tt.salary)
		}
		if len(j.Countries) != 1 || j.Countries[0] != "IN" {
			t.Errorf("job %d countries = %v", tt.i, j.Countries)
		}
	}
	if jobs[0].Currency != "INR" || *jobs[0].SalaryMax != 360000 {
		t.Errorf("salary = %v %v", jobs[0].Currency, jobs[0].SalaryMax)
	}
	if want := time.Date(2026, 10, 21, 9, 15, 0, 0, time.UTC); !jobs[0].PostedAt.Equal(want) {
		t.Errorf("posted = %v", jobs[0].PostedAt)
	}
}

func TestSupportsAndReady(t *testing.T) {
	s := New("", "", 0, nil)
	if s.Ready() == nil {
		t.Error("Ready() should fail without keys")
	}
	tests := []struct {
		name string
		f    model.Filters
		want bool
	}{
		{"india", model.Filters{Countries: []string{"IN"}}, true},
		{"japan only", model.Filters{Countries: []string{"JP"}}, false},
		{"uae only", model.Filters{Countries: []string{"AE"}}, false},
		{"japan+us", model.Filters{Countries: []string{"JP", "US"}}, true},
		{"remote only", model.Filters{Countries: []string{"IN"}, Modes: []model.WorkMode{model.ModeRemote}}, false},
	}
	for _, tt := range tests {
		if got := s.Supports(tt.f); got != tt.want {
			t.Errorf("%s: Supports = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestQuery(t *testing.T) {
	tests := []struct {
		f    model.Filters
		want string
	}{
		{model.Filters{Keywords: []string{"Go", "Kubernetes"}}, "Go Kubernetes"},
		{model.Filters{Types: []model.JobType{model.TypeNewGrad}}, "graduate fresher junior entry"},
		{model.Filters{Areas: []model.Area{model.AreaHPC}}, "cuda gpu hpc"},
		{model.Filters{}, "software developer engineer"},
	}
	for _, tt := range tests {
		if got := query(tt.f); got != tt.want {
			t.Errorf("query(%+v) = %q, want %q", tt.f, got, tt.want)
		}
	}
}
