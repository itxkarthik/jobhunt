package himalayas

import (
	"context"
	"slices"
	"testing"

	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources/sourcetest"
)

func TestFetchParsesFixture(t *testing.T) {
	srv := sourcetest.Serve(t, map[string]string{"/jobs/api/search": "testdata/search.json"})
	s := New(5, sourcetest.Client())
	s.BaseURL = srv.URL + "/jobs/api/search"
	f := model.Filters{
		Countries: []string{"IN", model.RemoteGlobal},
		Types:     []model.JobType{model.TypeInternship},
		Areas:     []model.Area{model.AreaBackend},
	}
	jobs, err := s.Fetch(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	reqs := srv.Requests()
	if len(reqs) != 2 {
		t.Fatalf("want one request per country (totalCount=3 stops paging), got %d", len(reqs))
	}
	if q := reqs[0].Query; q["country"][0] != "IN" || q["employment_type"][0] != "Intern" || q["q"] != nil {
		t.Errorf("IN query = %v (areas must not be sent as q)", q)
	}
	if q := reqs[1].Query; q["worldwide"][0] != "true" || q["country"] != nil {
		t.Errorf("remote-global query = %v", q)
	}
	if len(jobs) != 3 {
		t.Fatalf("got %d jobs, want 3 (deduped across queries)", len(jobs))
	}
	tests := []struct {
		i         int
		title     string
		jobType   model.JobType
		exp       model.Experience
		countries []string
	}{
		{0, "Junior Java Developer", model.TypeFullTime, model.ExpEntry, []string{"IN"}},
		{1, "Senior Back-End Engineer Intern", model.TypeInternship, model.ExpIntern, []string{model.RemoteGlobal}},
		{2, "Back-end Developer intern - Python", model.TypeInternship, model.ExpIntern, []string{"US"}},
	}
	for _, tt := range tests {
		j := jobs[tt.i]
		if j.Title != tt.title || j.JobType != tt.jobType || j.Experience != tt.exp || !slices.Equal(j.Countries, tt.countries) || j.WorkMode != model.ModeRemote {
			t.Errorf("job %d = %q %s %s %v %s", tt.i, j.Title, j.JobType, j.Experience, j.Countries, j.WorkMode)
		}
		if j.URL == "" || j.PostedAt.IsZero() {
			t.Errorf("job %d missing url/posted", tt.i)
		}
	}
}
