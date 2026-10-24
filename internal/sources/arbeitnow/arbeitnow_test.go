package arbeitnow

import (
	"context"
	"testing"

	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources/sourcetest"
)

func TestFetchParsesFixture(t *testing.T) {
	srv := sourcetest.Serve(t, map[string]string{"/api": "testdata/page1.json"})
	s := New(3, sourcetest.Client())
	s.BaseURL = srv.URL + "/api"
	jobs, err := s.Fetch(context.Background(), model.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(srv.Requests()); n != 1 {
		t.Errorf("made %d requests; links.next=null should stop pagination", n)
	}
	if len(jobs) != 3 {
		t.Fatalf("got %d jobs", len(jobs))
	}
	tests := []struct {
		i       int
		jobType model.JobType
		exp     model.Experience
		mode    model.WorkMode
		country string
	}{
		{0, model.TypeInternship, model.ExpIntern, model.ModeUnknown, "DE"}, // "Praktikum", Berlin
		{1, model.TypeUnknown, model.ExpUnknown, model.ModeRemote, "DE"},
		{2, model.TypeFullTime, model.ExpUnknown, model.ModeRemote, "DE"}, // location "Remote": German board default
	}
	for _, tt := range tests {
		j := jobs[tt.i]
		if j.JobType != tt.jobType || j.Experience != tt.exp || j.WorkMode != tt.mode || len(j.Countries) == 0 || j.Countries[0] != tt.country {
			t.Errorf("job %d %q: type=%s exp=%s mode=%s countries=%v", tt.i, j.Title, j.JobType, j.Experience, j.WorkMode, j.Countries)
		}
	}
	if jobs[0].City != "Berlin" {
		t.Errorf("city = %q", jobs[0].City)
	}
}

func TestSupports(t *testing.T) {
	s := New(1, nil)
	for _, tt := range []struct {
		countries []string
		want      bool
	}{
		{[]string{"IN"}, false},
		{[]string{"DE"}, true},
		{[]string{"NL"}, true},
		{[]string{"IN", model.RemoteGlobal}, true},
		{nil, true},
	} {
		if got := s.Supports(model.Filters{Countries: tt.countries}); got != tt.want {
			t.Errorf("Supports(%v) = %v", tt.countries, got)
		}
	}
}
