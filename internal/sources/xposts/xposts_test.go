package xposts

import (
	"context"
	"strings"
	"testing"

	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources/sourcetest"
)

func TestFetch(t *testing.T) {
	srv := sourcetest.Serve(t, map[string]string{"/recent": "testdata/recent.json"})
	s := New("tok", 10, sourcetest.Client())
	s.BaseURL = srv.URL + "/recent"
	jobs, err := s.Fetch(context.Background(), model.Filters{Countries: []string{"IN"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("got %d jobs", len(jobs))
	}
	if jobs[0].URL != "https://jobs.example.in/backend-intern" || jobs[0].Company != "Acme Engineering" || jobs[0].City != "Bengaluru" {
		t.Errorf("job 0 = %+v", jobs[0])
	}
	// An x.com link is the thread, not a posting: fall back to the post URL.
	if jobs[1].URL != "https://x.com/janef/status/1849000000000000002" {
		t.Errorf("job 1 URL = %s", jobs[1].URL)
	}
	if got := srv.Requests()[0].Header.Get("Authorization"); got != "Bearer tok" {
		t.Errorf("auth = %q", got)
	}
}

func TestQuery(t *testing.T) {
	tests := []struct {
		f    model.Filters
		want []string
	}{
		{model.Filters{Countries: []string{"IN"}, Types: []model.JobType{model.TypeInternship}, Areas: []model.Area{model.AreaBackend}},
			[]string{"hiring", "intern", "backend", "India", "-is:retweet"}},
		{model.Filters{Keywords: []string{"Rust"}}, []string{"Rust"}},
	}
	for _, tt := range tests {
		q := Query(tt.f)
		for _, w := range tt.want {
			if !strings.Contains(q, w) {
				t.Errorf("Query(%+v) = %q, missing %q", tt.f, q, w)
			}
		}
		if len(q) > 512 {
			t.Errorf("query over 512 chars: %d", len(q))
		}
	}
	long := model.Filters{Keywords: strings.Fields(strings.Repeat("kubernetes ", 80))}
	if q := Query(long); len(q) > 512 {
		t.Errorf("long query = %d chars, want ≤512", len(q))
	}
}
