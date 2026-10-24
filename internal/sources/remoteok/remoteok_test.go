package remoteok

import (
	"context"
	"testing"

	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources/sourcetest"
)

func TestFetchParsesFixture(t *testing.T) {
	srv := sourcetest.Serve(t, map[string]string{"/api": "testdata/api.json"})
	s := New(sourcetest.Client())
	s.BaseURL = srv.URL + "/api"
	jobs, err := s.Fetch(context.Background(), model.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 3 {
		t.Fatalf("got %d jobs, want 3 (legal notice must be skipped)", len(jobs))
	}
	tests := []struct {
		i         int
		id, title string
		countries []string
		salary    bool
	}{
		{0, "remoteok:1137557", "Senior DevOps Engineer", nil, true},
		{1, "remoteok:1137559", "Travel Support Specialist", []string{"US"}, false},
		{2, "remoteok:1137563", "Sales Development Representative German Speaking", nil, false},
	}
	for _, tt := range tests {
		j := jobs[tt.i]
		if j.ID != tt.id || j.Title != tt.title || j.WorkMode != model.ModeRemote || j.PostedAt.IsZero() || j.URL == "" {
			t.Errorf("job %d = %s %q %s posted=%v", tt.i, j.ID, j.Title, j.WorkMode, j.PostedAt)
		}
		if len(j.Countries) != len(tt.countries) || (len(tt.countries) > 0 && j.Countries[0] != tt.countries[0]) {
			t.Errorf("job %d countries = %v, want %v", tt.i, j.Countries, tt.countries)
		}
		if (j.SalaryMin != nil) != tt.salary || (tt.salary && j.Currency != "USD") {
			t.Errorf("job %d salary = %v %s", tt.i, j.SalaryMin, j.Currency)
		}
	}
	if jobs[1].City != "Seattle" {
		t.Errorf("city = %q", jobs[1].City)
	}
}
