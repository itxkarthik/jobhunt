package feed

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/search"
)

var update = flag.Bool("update", false, "rewrite testdata golden files")

var (
	runAt = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	older = runAt.Add(-48 * time.Hour)
)

func result() *search.Result {
	ist := time.FixedZone("IST", 5*3600+1800)
	res := &search.Result{
		RunAt: runAt,
		Total: 120,
		Reports: []search.Report{
			{Source: "greenhouse", Status: search.StatusOK, Count: 100, Duration: 1500 * time.Millisecond},
			{Source: "remotive", Status: search.StatusCached, Count: 20},
			{Source: "hn", Status: search.StatusError, Err: "timeout after 20s"},
		},
		Jobs: []model.Job{
			{ID: "greenhouse:1", Source: "greenhouse", Experience: model.ExpIntern, PostedAt: older, Key: "aaaa1111", Title: "Backend Intern", Company: "Acme", URL: "https://acme.example/jobs/1",
				Score: 91, New: true, FirstSeenAt: runAt, Description: "Go and Postgres",
				ScoreParts: []model.ScorePart{{Factor: "match", Points: 35, Max: 35}, {Factor: "recency", Points: 20.4, Max: 25},
					{Factor: "level", Points: 25, Max: 25}, {Factor: "fit", Points: 10, Max: 10}, {Factor: "info", Points: 0.6, Max: 5}},
				JobType: model.TypeInternship, WorkMode: model.ModeRemote, Sources: []string{"greenhouse"},
				SalaryMin: new(15000.0), SalaryMax: new(20000.0), Currency: "INR", PayPeriod: model.PerMonth},
			{ID: "remotive:2", Source: "remotive", Experience: model.ExpIntern, Key: "bbbb2222", Title: "SDE Intern", Company: "Beta", URL: "https://beta.example/2",
				Score: 72, FirstSeenAt: older, Description: "line one\nline two",
				JobType: model.TypeInternship, WorkMode: model.ModeOnsite, Sources: []string{"remotive"}},
			{ID: "greenhouse:3", Source: "greenhouse", Experience: model.ExpUnknown, Key: "cccc3333", Title: "QA Intern", Company: "Gamma", URL: "https://gamma.example/3",
				Score: 40, FirstSeenAt: runAt, JobType: model.TypeInternship, WorkMode: model.ModeUnknown, Paid: new(false)},
		},
	}
	res.Jobs[1].FirstSeenAt = older.In(ist) // the store returns local time; events must be UTC
	return res
}

func keys(events []Event) []string {
	var ks []string
	for _, e := range events {
		if e.Type == TypeJob {
			ks = append(ks, e.Job.Key)
		}
	}
	return ks
}

func TestBuildSelection(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		want []string
	}{
		{"all", Options{}, []string{"aaaa1111", "bbbb2222", "cccc3333"}},
		{"min score", Options{MinScore: 60}, []string{"aaaa1111", "bbbb2222"}},
		{"min score is inclusive", Options{MinScore: 72}, []string{"aaaa1111", "bbbb2222"}},
		{"since", Options{Since: runAt}, []string{"aaaa1111", "cccc3333"}},
		{"since and min score", Options{Since: runAt, MinScore: 50}, []string{"aaaa1111"}},
		{"since after everything", Options{Since: runAt.Add(time.Second)}, nil},
		{"limit", Options{Limit: 2}, []string{"aaaa1111", "bbbb2222"}},
		{"limit after selection", Options{MinScore: 50, Limit: 1}, []string{"aaaa1111"}},
		{"limit above matches", Options{Limit: 10}, []string{"aaaa1111", "bbbb2222", "cccc3333"}},
		{"delivered keys are skipped", Options{Delivered: map[string]bool{"aaaa1111": true}}, []string{"bbbb2222", "cccc3333"}},
		{"pending gone from source comes first", Options{Pending: []model.Job{{Key: "gone", Score: 50}}},
			[]string{"gone", "aaaa1111", "bbbb2222", "cccc3333"}},
		{"pending still in results is sent once, first", Options{Pending: []model.Job{{Key: "bbbb2222", Title: "stale"}}},
			[]string{"bbbb2222", "aaaa1111", "cccc3333"}},
		// Pending jobs were already selected; re-filtering could strand them.
		{"pending ignores min score", Options{MinScore: 60, Pending: []model.Job{{Key: "gone", Score: 50}}},
			[]string{"gone", "aaaa1111", "bbbb2222"}},
		{"pending in results ignores a lower fresh score", Options{MinScore: 60, Pending: []model.Job{{Key: "cccc3333", Score: 95}}},
			[]string{"cccc3333", "aaaa1111", "bbbb2222"}},
		{"pending ignores since", Options{Since: runAt.Add(time.Hour), Pending: []model.Job{{Key: "gone"}}},
			[]string{"gone"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := Build(result(), "run1", tt.opts)
			if got := keys(events); !slices.Equal(got, tt.want) {
				t.Fatalf("jobs = %v, want %v", got, tt.want)
			}
			last := events[len(events)-1]
			if last.Type != TypeRun || last.Run.Emitted != len(tt.want) {
				t.Fatalf("last event = %+v, want run with emitted=%d", last, len(tt.want))
			}
		})
	}
}

func TestBuildEnvelope(t *testing.T) {
	res := result()
	events := Build(res, "run1", Options{})

	wantTypes := []Type{TypeSource, TypeSource, TypeSource, TypeJob, TypeJob, TypeJob, TypeRun}
	var got []Type
	for _, e := range events {
		got = append(got, e.Type)
		if e.V != Version || e.RunID != "run1" || !e.At.Equal(runAt) {
			t.Errorf("envelope = v%d %q %v", e.V, e.RunID, e.At)
		}
		set := 0
		for _, p := range []bool{e.Source != nil, e.Job != nil, e.Run != nil} {
			if p {
				set++
			}
		}
		if set != 1 {
			t.Errorf("%s event has %d payloads, want 1", e.Type, set)
		}
	}
	if !slices.Equal(got, wantTypes) {
		t.Fatalf("types = %v, want %v", got, wantTypes)
	}
	run := events[len(events)-1].Run
	if *run != (RunEvent{Fetched: 120, Matched: 3, Emitted: 3, SourceErrors: 1}) {
		t.Errorf("run = %+v", run)
	}
	if s := events[0].Source; s.DurationMS != 1500 || s.Count != 100 {
		t.Errorf("source = %+v", s)
	}
}

func TestBuildDoesNotModifyResult(t *testing.T) {
	res := result()
	events := Build(res, "run1", Options{NoDescription: true})
	for _, e := range events {
		if e.Type == TypeJob && e.Job.Description != "" {
			t.Errorf("%s: description kept with NoDescription", e.Job.Key)
		}
	}
	if res.Jobs[0].Description == "" {
		t.Error("Build cleared the description on the caller's result")
	}
}

func TestEncodeOneObjectPerLine(t *testing.T) {
	b, err := Marshal(Build(result(), "run1", Options{}))
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(nil, 1<<20)
	lines := 0
	for sc.Scan() {
		lines++
		var e Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("line %d is not one JSON object: %v", lines, err)
		}
	}
	if lines != 7 {
		t.Fatalf("lines = %d, want 7 (a newline in a description must not split an event)", lines)
	}
}

// goldenCompanies are the company events in the golden batch: one new,
// one changed.
func goldenCompanies() []model.Company {
	created := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	src := "https://example.com/news/acme-seed"
	return []model.Company{
		{Key: "acme ai", Name: "Acme AI", Website: "https://acme.ai", Country: "IN", City: "Bengaluru", Stage: model.StageSeed,
			Rounds:    []model.Round{{Stage: model.StageSeed, Amount: 3e6, Currency: "USD", Date: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), SourceURL: src}},
			Headcount: model.Headcount11to50, HeadcountSource: "estimate",
			Boards:   []model.Board{{ATS: "ashby", Slug: "acme"}},
			Contacts: []model.Contact{{Name: "Asha Rao", Role: "CEO", URL: "https://x.com/asha", SourceURL: src, AddedAt: created}},
			Tags:     []string{"go"}, Sources: []string{"agent"}, CreatedAt: created, UpdatedAt: created},
		{Key: "kestrel", Name: "Kestrel", Country: "US", Stage: model.StageUnknown, Headcount: model.HeadcountUnknown,
			Sources: []string{"edgar"}, CreatedAt: created, UpdatedAt: created.Add(24 * time.Hour)},
	}
}

func TestBuildCompanies(t *testing.T) {
	events := Build(result(), "run1", Options{Companies: goldenCompanies()})
	var changes []Change
	for _, e := range events {
		if e.Type == TypeCompany {
			changes = append(changes, e.Company.Change)
		}
	}
	run := events[len(events)-1]
	if !slices.Equal(changes, []Change{Created, Updated}) || run.Type != TypeRun || run.Run.Companies != 2 {
		t.Errorf("changes %v, last event %+v", changes, run)
	}
	c := goldenCompanies()[0]
	if v := CompanyVersion(&c); v != "co:acme ai@"+strconv.FormatInt(c.UpdatedAt.UnixNano(), 10) {
		t.Errorf("version %s", v)
	}
}

func TestEncodeGolden(t *testing.T) {
	got, err := Marshal(Build(result(), "run1", Options{Companies: goldenCompanies()}))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "batch.ndjson")
	if *update {
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("encoding changed; this is the public v%d contract.\ngot:\n%s\nwant:\n%s\n(run with -update if intended)", Version, got, want)
	}
}

func TestBuildPendingUsesFreshRecord(t *testing.T) {
	events := Build(result(), "run1", Options{Pending: []model.Job{{Key: "bbbb2222", Title: "stale"}}})
	for _, e := range events {
		if e.Type == TypeJob && e.Job.Key == "bbbb2222" && e.Job.Title != "SDE Intern" {
			t.Errorf("pending job sent with stored title %q, want the fresh record", e.Job.Title)
		}
	}
}
