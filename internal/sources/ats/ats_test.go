package ats

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources"
	"github.com/itxkarthik/jobhunt/internal/sources/sourcetest"
)

func TestEmbeddedCompaniesValid(t *testing.T) {
	cs, err := LoadCompanies("")
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) < 50 {
		t.Errorf("only %d companies in starter list", len(cs))
	}
	seen := map[string]bool{}
	for _, c := range cs {
		key := c.ATS + "/" + c.Slug
		if seen[key] {
			t.Errorf("duplicate board %s", key)
		}
		seen[key] = true
		if len(c.Countries) == 0 {
			t.Errorf("%s has no countries", c.Name)
		}
	}
}

func TestLoadCompaniesErrors(t *testing.T) {
	for _, tt := range []struct{ name, yaml string }{
		{"bad ats", "companies:\n  - {name: X, ats: workday, slug: x, countries: [IN]}\n"},
		{"bad country", "companies:\n  - {name: X, ats: lever, slug: x, countries: [Atlantis]}\n"},
		{"missing slug", "companies:\n  - {name: X, ats: lever, countries: [IN]}\n"},
	} {
		p := t.TempDir() + "/c.yaml"
		if err := writeFile(p, tt.yaml); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadCompanies(p); err == nil {
			t.Errorf("%s: expected error", tt.name)
		}
	}
}

var testCompanies = []Company{
	{Name: "Stripe", ATS: "greenhouse", Slug: "stripe", Countries: []string{"IN", "US"}},
	{Name: "Gone", ATS: "greenhouse", Slug: "gone", Countries: []string{"IN"}},
	{Name: "USOnly", ATS: "greenhouse", Slug: "usonly", Countries: []string{"US"}},
	{Name: "Paytm", ATS: "lever", Slug: "paytm", Countries: []string{"IN"}},
	{Name: "Notion", ATS: "ashby", Slug: "notion", Countries: []string{"US"}, Remote: true},
}

func TestBoardSelection(t *testing.T) {
	s := New("greenhouse", testCompanies, nil, "")
	slugs := func(f model.Filters) []string {
		var out []string
		for _, c := range s.boards(f) {
			out = append(out, c.Slug)
		}
		return out
	}
	if got := slugs(model.Filters{Countries: []string{"IN"}}); !slices.Equal(got, []string{"stripe", "gone"}) {
		t.Errorf("IN boards = %v", got)
	}
	if got := slugs(model.Filters{Countries: []string{"JP"}}); len(got) != 0 || s.Supports(model.Filters{Countries: []string{"JP"}}) {
		t.Errorf("JP boards = %v", got)
	}
	a := New("ashby", testCompanies, nil, "")
	if !a.Supports(model.Filters{Countries: []string{model.RemoteGlobal}}) {
		t.Error("remote-global should select remote-friendly boards")
	}
}

func TestGreenhouse(t *testing.T) {
	srv := sourcetest.Serve(t, map[string]string{"/stripe/jobs": "testdata/greenhouse.json"})
	s := New("greenhouse", testCompanies, sourcetest.Client(), srv.URL)
	jobs, err := s.Fetch(context.Background(), model.Filters{Countries: []string{"IN"}})
	// "gone" 404s: partial error, Stripe jobs still returned.
	var pe *sources.PartialError
	if !errors.As(err, &pe) || !slices.Equal(pe.Failed, []string{"gone"}) {
		t.Fatalf("err = %v, want partial failure of 'gone'", err)
	}
	if q := srv.Requests()[0].Query; len(q["content"]) == 0 {
		t.Error("must request content=true for descriptions")
	}
	if len(jobs) != 2 {
		t.Fatalf("got %d jobs", len(jobs))
	}
	j := jobs[0]
	if j.ID != "greenhouse:stripe:8031833" || j.Title != "Software Engineer, Intern" || j.Company != "Stripe" ||
		!slices.Equal(j.Countries, []string{"IN"}) || j.City != "Bengaluru" || j.PostedAt.IsZero() || j.Description == "" {
		t.Errorf("job 0 = %+v", j)
	}
	// Location "N/A" falls back to office names.
	if !slices.Equal(jobs[1].Countries, []string{"US"}) {
		t.Errorf("job 1 countries = %v (location %q)", jobs[1].Countries, jobs[1].Location)
	}
}

func TestLever(t *testing.T) {
	srv := sourcetest.Serve(t, map[string]string{"/paytm": "testdata/lever.json"})
	s := New("lever", testCompanies, sourcetest.Client(), srv.URL)
	jobs, err := s.Fetch(context.Background(), model.Filters{Countries: []string{"IN"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("got %d jobs", len(jobs))
	}
	j := jobs[0]
	if j.JobType != model.TypeInternship || j.WorkMode != model.ModeOnsite || j.Countries[0] != "IN" || j.Company != "Paytm" {
		t.Errorf("job 0 = %q %s %s %v", j.Title, j.JobType, j.WorkMode, j.Countries)
	}
	if j.SalaryMax == nil || *j.SalaryMax != 400000 || j.Currency != "INR" {
		t.Errorf("salary = %v %s", j.SalaryMax, j.Currency)
	}
	if jobs[1].WorkMode != model.ModeRemote || jobs[1].JobType != model.TypeFullTime || jobs[1].Countries[0] != "US" {
		t.Errorf("job 1 = %s %s %v", jobs[1].WorkMode, jobs[1].JobType, jobs[1].Countries)
	}
}

func TestAshby(t *testing.T) {
	srv := sourcetest.Serve(t, map[string]string{"/notion": "testdata/ashby.json"})
	s := New("ashby", testCompanies, sourcetest.Client(), srv.URL)
	jobs, err := s.Fetch(context.Background(), model.Filters{Countries: []string{"US"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("got %d jobs, want 2 (unlisted skipped)", len(jobs))
	}
	if j := jobs[0]; j.JobType != model.TypeInternship || j.WorkMode != model.ModeHybrid || !slices.Contains(j.Countries, "US") || j.City != "San Francisco" {
		t.Errorf("job 0 = %q %s %s %v %q", j.Title, j.JobType, j.WorkMode, j.Countries, j.City)
	}
	if j := jobs[1]; j.SalaryMin == nil || *j.SalaryMin != 150000 || j.Currency != "USD" {
		t.Errorf("job 1 salary = %v %s", j.SalaryMin, j.Currency)
	}
}
