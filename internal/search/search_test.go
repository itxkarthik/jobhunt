package search

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/itxkarthik/jobhunt/internal/classify"
	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources"
	"github.com/itxkarthik/jobhunt/internal/store"
)

type fakeSource struct {
	name     string
	jobs     []model.Job
	err      error
	delay    time.Duration
	supports bool
	notReady error
	calls    int
}

func (f *fakeSource) Name() string                { return f.name }
func (f *fakeSource) Supports(model.Filters) bool { return f.supports }
func (f *fakeSource) Ready() error                { return f.notReady }
func (f *fakeSource) SearchURL(model.Filters) string {
	return "https://example.com/" + f.name
}
func (f *fakeSource) Fetch(ctx context.Context, _ model.Filters) ([]model.Job, error) {
	f.calls++
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.jobs, f.err
}

func job(id, title, loc string) model.Job {
	countries, city := model.ParseLocation(loc)
	return model.Job{ID: id, Source: "fake", Title: title, Location: loc, Countries: countries, City: city, PostedAt: time.Now()}
}

func TestEngineIsolatesFailures(t *testing.T) {
	good := &fakeSource{name: "good", supports: true, jobs: []model.Job{
		job("good:1", "Backend Intern", "Bengaluru"),
		job("good:2", "Senior Backend Engineer", "Bengaluru"),
		job("good:3", "Backend Intern", "Berlin"),
	}}
	broken := &fakeSource{name: "broken", supports: true, err: errors.New("HTTP 500")}
	slow := &fakeSource{name: "slow", supports: true, delay: time.Second}
	nokey := &fakeSource{name: "nokey", supports: true, notReady: sources.ErrNotConfigured{Msg: "missing key"}}
	nojp := &fakeSource{name: "nojp", supports: false}

	eng := &Engine{
		Sources:    []sources.Source{good, broken, slow, nokey, nojp},
		Classifier: classify.Default(),
		Timeout:    50 * time.Millisecond,
	}
	f := model.Filters{Countries: []string{"IN"}, Types: []model.JobType{model.TypeInternship}, Areas: []model.Area{model.AreaBackend}}
	res, err := eng.Run(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Jobs) != 1 || res.Jobs[0].ID != "good:1" {
		t.Errorf("jobs = %+v", res.Jobs)
	}
	want := map[string]Status{"good": StatusOK, "broken": StatusError, "slow": StatusError, "nokey": StatusSkipped, "nojp": StatusSkipped}
	for _, r := range res.Reports {
		if r.Status != want[r.Source] {
			t.Errorf("%s status = %s (%s), want %s", r.Source, r.Status, r.Err, want[r.Source])
		}
		if r.Link == "" {
			t.Errorf("%s: missing fallback link", r.Source)
		}
	}
	if nokey.calls != 0 || nojp.calls != 0 {
		t.Error("skipped sources must not be fetched")
	}
}

func TestEngineCache(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	src := &fakeSource{name: "src", supports: true, jobs: []model.Job{job("src:1", "Backend Intern", "Pune")}}
	eng := &Engine{Sources: []sources.Source{src}, Store: st, Classifier: classify.Default(), CacheTTL: time.Hour}
	f := model.Filters{Countries: []string{"IN"}}

	for i, want := range []Status{StatusOK, StatusCached} {
		res, err := eng.Run(context.Background(), f)
		if err != nil {
			t.Fatal(err)
		}
		if res.Reports[0].Status != want || len(res.Jobs) != 1 {
			t.Errorf("run %d: status %s, %d jobs", i, res.Reports[0].Status, len(res.Jobs))
		}
	}
	if src.calls != 1 {
		t.Errorf("fetched %d times, want 1", src.calls)
	}

	// Refresh bypasses the cache; on failure we fall back to stale results.
	eng.Refresh = true
	src.err = errors.New("down")
	res, _ := eng.Run(context.Background(), f)
	if res.Reports[0].Status != StatusError || len(res.Jobs) != 1 {
		t.Errorf("stale fallback: status %s, %d jobs", res.Reports[0].Status, len(res.Jobs))
	}
	h, _ := st.Health(context.Background())
	if h["src"].Last.Status != "error" {
		t.Errorf("health not recorded: %+v", h)
	}
}

func TestFilter(t *testing.T) {
	c := classify.Default()
	now := time.Now()
	mk := func(title, loc string, mode model.WorkMode, age time.Duration) model.Job {
		j := job("x", title, loc)
		j.WorkMode, j.PostedAt = mode, now.Add(-age)
		c.Classify(&j)
		return j
	}
	jobs := []model.Job{
		mk("Backend Intern", "Bengaluru", model.ModeOnsite, time.Hour),           // 0
		mk("Backend Intern", "Worldwide", model.ModeRemote, time.Hour),           // 1
		mk("Backend Intern", "USA", model.ModeRemote, time.Hour),                 // 2
		mk("Frontend Intern", "Pune", model.ModeOnsite, time.Hour),               // 3
		mk("Backend Intern", "Mumbai", model.ModeOnsite, 10*24*time.Hour),        // 4
		mk("Software Engineer Intern", "Hyderabad", model.ModeHybrid, time.Hour), // 5 generic
		mk("Backend Engineer", "Chennai", model.ModeOnsite, time.Hour),           // 6 type unknown
		mk("Backend Intern", "", model.ModeRemote, time.Hour),                    // 7 no location
	}
	base := model.Filters{Countries: []string{"IN"}, Types: []model.JobType{model.TypeInternship}, Areas: []model.Area{model.AreaBackend}}
	tests := []struct {
		name string
		mod  func(*model.Filters)
		want []int
	}{
		{"default", func(*model.Filters) {}, []int{0, 1, 4, 5}},
		{"strict country", func(f *model.Filters) { f.StrictCountry = true }, []int{0, 4, 5}},
		{"remote only", func(f *model.Filters) { f.Modes = []model.WorkMode{model.ModeRemote} }, []int{1}},
		{"posted within 7d", func(f *model.Filters) { f.PostedWithin = 7 * 24 * time.Hour }, []int{0, 1, 5}},
		{"include unknown", func(f *model.Filters) { f.IncludeUnknown = true }, []int{0, 1, 4, 5, 6, 7}},
		{"remote-global", func(f *model.Filters) { f.Countries = []string{model.RemoteGlobal} }, []int{1}},
		{"keywords", func(f *model.Filters) { f.Keywords = []string{"software"} }, []int{5}},
		{"exclude", func(f *model.Filters) { f.Exclude = []string{"software"} }, []int{0, 1, 4}},
	}
	for _, tt := range tests {
		f := base
		tt.mod(&f)
		var got []int
		for i := range jobs {
			if matches(&jobs[i], f, c, compileTerms(f.Keywords), compileTerms(f.Exclude), now) {
				got = append(got, i)
			}
		}
		if !equalInts(got, tt.want) {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
