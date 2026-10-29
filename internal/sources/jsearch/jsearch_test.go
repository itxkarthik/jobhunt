package jsearch

import (
	"context"
	"testing"

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
