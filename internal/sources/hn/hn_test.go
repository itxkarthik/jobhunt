package hn

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources/sourcetest"
)

func TestFetchParsesFixture(t *testing.T) {
	srv := sourcetest.Serve(t, map[string]string{
		"/api/v1/search_by_date": "testdata/search.json",
		"/api/v1/items/49922569": "testdata/thread.json",
	})
	s := New(sourcetest.Client())
	s.BaseURL = srv.URL + "/api/v1"
	jobs, err := s.Fetch(context.Background(), model.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 4 {
		t.Fatalf("got %d jobs, want 4 (deleted comment skipped)", len(jobs))
	}
	tests := []struct {
		company, titleHas string
		countries         []string
	}{
		{"PrairieLearn", "Full-Stack Software Engineer", []string{"US"}},
		{"Shepherd", "Multiple roles", []string{"US"}},
		{"Sidekick Labs", "R&D Engineer", []string{"US"}},
		{"Open Education Applications / Neon", "DevOps Engineer", []string{"NL"}},
	}
	for i, tt := range tests {
		j := jobs[i]
		if j.Company != tt.company || !strings.Contains(j.Title, tt.titleHas) || !slices.Equal(j.Countries, tt.countries) {
			t.Errorf("job %d = company %q title %q countries %v", i, j.Company, j.Title, j.Countries)
		}
		if !strings.HasPrefix(j.URL, "https://news.ycombinator.com/item?id=") || j.PostedAt.IsZero() {
			t.Errorf("job %d url/posted = %s %v", i, j.URL, j.PostedAt)
		}
	}
	if jobs[1].City != "San Francisco" || jobs[3].City != "Utrecht" {
		t.Errorf("cities = %q, %q", jobs[1].City, jobs[3].City)
	}
}

func TestSplitHeader(t *testing.T) {
	tests := []struct{ in, company, title string }{
		{"Acme | Backend Engineer, SRE | Remote (EU) | €80k | https://acme.io", "Acme", "Backend Engineer, SRE"},
		{"Foo Inc (YC W24) | Founding Engineer | ONSITE | NYC", "Foo Inc", "Founding Engineer"},
		{"Bar | REMOTE | Full-time", "Bar", "Multiple roles (see post)"},
		{"Baz — Software Engineering Interns — Bengaluru", "Baz", "Software Engineering Interns"},
		{"Cologne, Germany | UMH | Product Engineer | ONSITE", "UMH", "Product Engineer"},
		{"Erica Holloway | Talent Sourcer @ Footprint | NYC", "Footprint", "Multiple roles (see post)"},
		{"America First Policy Institute - Office for Fiscal Analysis | ONSITE", "America First Policy Institute", "Multiple roles (see post)"},
	}
	for _, tt := range tests {
		c, ti := splitHeader(tt.in)
		if c != tt.company || ti != tt.title {
			t.Errorf("splitHeader(%q) = %q, %q; want %q, %q", tt.in, c, ti, tt.company, tt.title)
		}
	}
}
