package jsearch

import (
	"context"
	"testing"
	"time"

	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources/sourcetest"
)

func TestFetch(t *testing.T) {
	srv := sourcetest.Serve(t, map[string]string{"/search": "testdata/search.json"})
	s := New("test-key", 1, sourcetest.Client())
	s.BaseURL = srv.URL + "/search"
	jobs, err := s.Fetch(context.Background(), model.Filters{Countries: []string{"IN"}, Types: []model.JobType{model.TypeInternship}})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("got %d jobs, want 2", len(jobs))
	}
	j := jobs[0]
	if j.ID != "jsearch:Zk9xX2Jhc2U2NA==" || j.JobType != model.TypeInternship || j.City != "Bengaluru" ||
		j.Countries[0] != "IN" || j.SalaryMin == nil || *j.SalaryMin != 360000 || j.Currency != "INR" || j.Tags[0] != "via LinkedIn" {
		t.Errorf("job 0 = %+v", j)
	}
	if jobs[1].ID != "jsearch:987654" || jobs[1].WorkMode != model.ModeRemote || jobs[1].SalaryMin != nil {
		t.Errorf("job 1 = %+v", jobs[1])
	}
	req := srv.Requests()[0]
	if req.Header.Get("X-RapidAPI-Key") != "test-key" || req.Query["query"] == nil {
		t.Errorf("request = %+v", req)
	}
}

func TestReady(t *testing.T) {
	if New("", 1, sourcetest.Client()).Ready() == nil {
		t.Error("no key: want not ready")
	}
}

func TestKeyKinds(t *testing.T) {
	srv := sourcetest.Serve(t, map[string]string{"/search": "testdata/search.json"})
	tests := []struct {
		key        string
		wantBase   string
		wantHeader string
		absent     string
	}{
		{"ak_test123", OpenWebNinjaBaseURL, "x-api-key", "X-RapidAPI-Key"},
		{"0123456789abcdef", RapidAPIBaseURL, "X-RapidAPI-Key", "x-api-key"},
	}
	for _, tt := range tests {
		s := New(tt.key, 1, sourcetest.Client())
		if s.BaseURL != tt.wantBase {
			t.Errorf("%s: base = %s, want %s", tt.key, s.BaseURL, tt.wantBase)
		}
		s.BaseURL = srv.URL + "/search"
		if _, err := s.Fetch(context.Background(), model.Filters{Countries: []string{"IN"}}); err != nil {
			t.Fatal(err)
		}
		reqs := srv.Requests()
		h := reqs[len(reqs)-1].Header
		if h.Get(tt.wantHeader) != tt.key || h.Get(tt.absent) != "" {
			t.Errorf("%s: headers %v", tt.key, h)
		}
	}
}

func TestPostedAt(t *testing.T) {
	want := time.Date(2026, 10, 22, 6, 30, 0, 0, time.UTC)
	tests := []struct {
		utc  string
		unix int64
		want time.Time
	}{
		{"2026-10-22T06:30:00.000Z", 0, want},
		{"", want.Unix(), want},
		{"", 0, time.Time{}},
	}
	for _, tt := range tests {
		if got := postedAt(tt.utc, tt.unix); !got.Equal(tt.want) {
			t.Errorf("postedAt(%q, %d) = %v, want %v", tt.utc, tt.unix, got, tt.want)
		}
	}
}
