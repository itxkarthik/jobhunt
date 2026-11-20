package search

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
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
			if matches(&jobs[i], f, c, classify.NewTermSet(f.Keywords), classify.NewTermSet(f.Exclude), now) {
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

type ttlSource struct {
	fakeSource
	min, def time.Duration
}

func (t *ttlSource) MinTTL() time.Duration     { return t.min }
func (t *ttlSource) DefaultTTL() time.Duration { return t.def }

func TestTTLFor(t *testing.T) {
	plain := &fakeSource{name: "plain"}
	terms := &ttlSource{name: "terms", min: 6 * time.Hour}
	heavy := &ttlSource{name: "heavy", def: 6 * time.Hour}
	tests := []struct {
		name    string
		src     sources.Source
		refresh bool
		cfg     map[string]time.Duration
		want    time.Duration
	}{
		{"global default", plain, false, nil, time.Hour},
		{"refresh plain", plain, true, nil, 0},
		{"terms minimum", terms, false, nil, 6 * time.Hour},
		{"refresh keeps terms minimum", terms, true, nil, 6 * time.Hour},
		{"config can't go below terms", terms, false, map[string]time.Duration{"terms": time.Minute}, 6 * time.Hour},
		{"source default", heavy, false, nil, 6 * time.Hour},
		{"refresh skips source default", heavy, true, nil, 0},
		{"config overrides default", heavy, false, map[string]time.Duration{"heavy": 2 * time.Hour}, 2 * time.Hour},
	}
	for _, tt := range tests {
		e := &Engine{CacheTTL: time.Hour, Refresh: tt.refresh, SourceTTL: tt.cfg}
		if got := e.ttlFor(tt.src); got != tt.want {
			t.Errorf("%s: ttl = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestEngineCompanyFilter(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	for _, c := range []model.Company{
		{Key: "acme", Name: "Acme", Stage: model.StageSeed, Headcount: model.Headcount11to50},
		{Key: "bigco", Name: "BigCo", Stage: model.StageGrowth, Headcount: model.Headcount1kPlus},
	} {
		if _, _, err := st.UpdateCompany(ctx, c.Key, func(*model.Company) (model.Company, error) { return c, nil }); err != nil {
			t.Fatal(err)
		}
	}
	at := func(id, company string) model.Job {
		j := job(id, "Backend Intern", "Bengaluru")
		j.Company = company
		return j
	}
	src := &fakeSource{name: "src", supports: true, jobs: []model.Job{
		at("src:1", "Acme Inc."), at("src:2", "BigCo"), at("src:3", "Unlisted Ltd"),
	}}
	eng := &Engine{Sources: []sources.Source{src}, Store: st, Classifier: classify.Default()}
	tests := []struct {
		name string
		f    model.Filters
		want string
	}{
		{"no company filter", model.Filters{}, "src:1,src:2,src:3"},
		{"stage", model.Filters{CompanyStages: []model.Stage{model.StageSeed, model.StageSeriesA}}, "src:1"},
		{"headcount", model.Filters{CompanyHeadcounts: []model.Headcount{model.Headcount1kPlus}}, "src:2"},
		{"both must match", model.Filters{CompanyStages: []model.Stage{model.StageSeed}, CompanyHeadcounts: []model.Headcount{model.Headcount1kPlus}}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.f.Countries = []string{"IN"}
			res, err := eng.Run(ctx, tt.f)
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, j := range res.Jobs {
				ids = append(ids, j.ID)
			}
			slices.Sort(ids)
			if strings.Join(ids, ",") != tt.want {
				t.Errorf("jobs = %v, want %s", ids, tt.want)
			}
		})
	}
	if _, err := (&Engine{Sources: []sources.Source{src}, Classifier: classify.Default()}).Run(ctx, model.Filters{CompanyStages: []model.Stage{model.StageSeed}}); err == nil {
		t.Error("company filter without a store should fail")
	}
}

func TestEnginePrepare(t *testing.T) {
	src := &fakeSource{name: "src", supports: true, jobs: []model.Job{job("src:1", "Backend Intern", "Pune")}}
	calls := 0
	eng := &Engine{Sources: []sources.Source{src}, Classifier: classify.Default(), Prepare: func(context.Context) error {
		calls++
		return errors.New("db busy") // not fatal
	}}
	res, err := eng.Run(context.Background(), model.Filters{Countries: []string{"IN"}})
	if err != nil || len(res.Jobs) != 1 || calls != 1 {
		t.Errorf("jobs=%v err=%v calls=%d", res, err, calls)
	}
}

func TestFilterProfileSkips(t *testing.T) {
	c := classify.Default()
	now := time.Now()
	mk := func(title, loc, desc string, mode model.WorkMode) model.Job {
		j := job("x", title, loc)
		j.Description, j.WorkMode, j.PostedAt = desc, mode, now
		c.Classify(&j)
		return j
	}
	jobs := []model.Job{
		mk("Backend Intern", "Bengaluru", "Build APIs in Go.", model.ModeOnsite),                     // 0
		mk("Backend Intern", "Bengaluru", "Requires prior internship experience.", model.ModeOnsite), // 1
		mk("Firmware Intern", "Pune", "RTOS and microcontrollers.", model.ModeOnsite),                // 2 embedded only
		mk("Backend and Firmware Intern", "Pune", "", model.ModeOnsite),                              // 3 both
		mk("Backend Intern", "", "Office-based.", model.ModeHybrid),                                  // 4 hybrid, location unknown
	}
	base := model.Filters{Countries: []string{"IN"}, IncludeUnknown: true}
	tests := []struct {
		name string
		mod  func(*model.Filters)
		want []int
	}{
		{"none", func(*model.Filters) {}, []int{0, 1, 2, 3, 4}},
		{"requires experience", func(f *model.Filters) { f.SkipRequiresExperience = true }, []int{0, 2, 3, 4}},
		{"skip embedded", func(f *model.Filters) { f.SkipAreas = []model.Area{model.AreaEmbedded} }, []int{0, 1, 4}},
		{"skip embedded, want backend", func(f *model.Filters) {
			f.SkipAreas, f.Areas = []model.Area{model.AreaEmbedded}, []model.Area{model.AreaBackend}
		}, []int{0, 1, 3, 4}},
		{"onsite abroad", func(f *model.Filters) { f.SkipOnsiteAbroad = true }, []int{0, 1, 2, 3}},
	}
	for _, tt := range tests {
		f := base
		tt.mod(&f)
		var got []int
		for i := range jobs {
			if matches(&jobs[i], f, c, classify.NewTermSet(nil), classify.NewTermSet(nil), now) {
				got = append(got, i)
			}
		}
		if !equalInts(got, tt.want) {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
	// Profile-only fields don't split the source cache.
	src := &fakeSource{name: "s"}
	if cacheKey(src, base) != cacheKey(src, model.Filters{Countries: []string{"IN"}, IncludeUnknown: true, Stack: []string{"go"}, SkipOnsiteAbroad: true}) {
		t.Error("profile fields changed the cache key")
	}
}

// A cached search gives the same classification as a fresh one, and an
// entry from another classifier version is classified again from its stored
// copy: fields the source set survive, and the fetch time stays.
func TestEngineCacheClassification(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	mk := func() []model.Job {
		a := job("src:1", "Backend Engineering Intern", "Bengaluru")
		a.Company, a.Description = "Acme", "Remote team. Go and PostgreSQL. Visa sponsorship available. Prior work experience required."
		b := job("src:2", "Backend Intern", "Pune")
		b.Company, b.Description, b.WorkMode, b.Tags = "Beta", "Fully remote.", model.ModeOnsite, []string{"from-source"}
		return []model.Job{a, b}
	}
	src := &fakeSource{name: "src", supports: true}
	eng := &Engine{Sources: []sources.Source{src}, Store: st, Classifier: classify.Default(), CacheTTL: time.Hour}
	f := model.Filters{Countries: []string{"IN"}, IncludeUnknown: true}
	run := func() []string {
		src.jobs = mk() // a fresh copy, as a real fetch returns
		res, err := eng.Run(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, j := range res.Jobs {
			visa := "nil"
			if j.VisaSponsorship != nil {
				visa = strconv.FormatBool(*j.VisaSponsorship)
			}
			out = append(out, fmt.Sprint(j.ID, j.JobType, j.Experience, j.WorkMode, visa, j.Areas, j.Tags))
		}
		slices.Sort(out)
		return out
	}
	fresh := run()
	if !slices.ContainsFunc(fresh, func(s string) bool { return strings.HasPrefix(s, "src:2") && strings.Contains(s, "onsite") }) ||
		!slices.ContainsFunc(fresh, func(s string) bool { return strings.Contains(s, classify.RequiresExperienceTag) }) {
		t.Fatalf("want the source's work mode and a classifier tag: %v", fresh)
	}
	if cached := run(); !slices.Equal(cached, fresh) || src.calls != 1 {
		t.Fatalf("cached %v\nfresh  %v\ncalls %d", cached, fresh, src.calls)
	}
	key := cacheKey(src, f)
	before, _, _ := st.CachedJobs(ctx, "src", key, time.Hour)
	// Another version's classification, which must not be applied.
	if err := st.RedoCache(ctx, "src", key, before, before.Jobs, "old", []byte(`[{"t":"contract"},{"t":"contract"}]`)); err != nil {
		t.Fatal(err)
	}
	if redone := run(); !slices.Equal(redone, fresh) || src.calls != 1 {
		t.Fatalf("reclassified %v\nfresh       %v\ncalls %d", redone, fresh, src.calls)
	}
	if after, _, _ := st.CachedJobs(ctx, "src", key, time.Hour); after.ClassVersion != eng.prepVersion() || !after.FetchedAt.Equal(before.FetchedAt) {
		t.Errorf("entry after redo: version %q, fetched %v (was %v)", after.ClassVersion, after.FetchedAt, before.FetchedAt)
	}
}
