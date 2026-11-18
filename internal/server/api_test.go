package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/search"
	"github.com/itxkarthik/jobhunt/internal/sources"
	"github.com/itxkarthik/jobhunt/internal/store"
)

var t0 = time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)

var testJobs = []model.Job{
	{Key: "aaaa1111", Title: "Backend Intern", Company: "Acme", URL: "https://acme.example/1", Score: 90,
		JobType: model.TypeInternship, WorkMode: model.ModeRemote, Experience: model.ExpIntern},
	{Key: "aaaa2222", Title: "SDE Intern", Company: "Beta", URL: "https://beta.example/2", Score: 70,
		JobType: model.TypeInternship, WorkMode: model.ModeOnsite, Experience: model.ExpIntern},
	{Key: "bbbb3333", Title: "Data Intern", Company: "Gamma", URL: "https://gamma.example/3", Score: 50,
		JobType: model.TypeInternship, WorkMode: model.ModeHybrid, Experience: model.ExpIntern},
}

// recorder captures what the fake search was asked to do.
type recorder struct {
	calls   atomic.Int32
	filters model.Filters
	opts    SearchOptions
}

func apiServer(t *testing.T) (*Server, *store.Store, *recorder) {
	t.Helper()
	rec := &recorder{}
	s, st := newTestServer(t, func(o *Options) {
		o.Search = func(ctx context.Context, f model.Filters, so SearchOptions, _ func(search.Report)) (*search.Result, error) {
			rec.calls.Add(1)
			rec.filters, rec.opts = f, so
			jobs := slices.Clone(testJobs)
			if err := o.Store.MarkSeen(ctx, jobs, t0); err != nil {
				return nil, err
			}
			return &search.Result{RunAt: t0, Total: 1200, Jobs: jobs, Reports: []search.Report{
				{Source: "greenhouse", Status: search.StatusOK, Count: 1000, Duration: 2 * time.Second},
				{Source: "jsearch", Status: search.StatusSkipped, Err: "missing key", Link: "https://www.google.com/search?q=jobs"},
			}}, nil
		}
		o.Sources = []sources.Source{fakeSource{"greenhouse", nil}, fakeSource{"jsearch", errors.New("missing jsearch api_key")}}
		o.Catalog = []SourceDesc{
			{Name: "greenhouse", Description: "Company boards on Greenhouse"},
			{Name: "jsearch", Description: "JSearch", Credential: "api_key"},
			{Name: "adzuna", Description: "Adzuna API", Credential: "app_id + app_key"}, // disabled, never run
		}
	})
	if err := st.MarkSeen(context.Background(), slices.Clone(testJobs), t0); err != nil {
		t.Fatal(err)
	}
	return s, st, rec
}

type fakeSource struct {
	name  string
	ready error
}

func (f fakeSource) Name() string                { return f.name }
func (f fakeSource) Supports(model.Filters) bool { return true }
func (f fakeSource) Ready() error                { return f.ready }
func (f fakeSource) Fetch(context.Context, model.Filters) ([]model.Job, error) {
	return nil, nil
}

// call sends a request and decodes the JSON response into out (if non-nil).
func call(t *testing.T, s *Server, method, path, body string, out any) int {
	t.Helper()
	w := serve(s, req(method, path, body))
	if out != nil && w.Body.Len() > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			t.Fatalf("%s %s: bad JSON %q: %v", method, path, w.Body, err)
		}
	}
	return w.Code
}

func TestSearch(t *testing.T) {
	s, st, rec := apiServer(t)
	if err := st.SetStatus(context.Background(), "aaaa1111", store.Applied, ""); err != nil {
		t.Fatal(err)
	}
	var res searchResponse
	code := call(t, s, http.MethodPost, "/api/search",
		`{"types":["internship"],"areas":["backend"],"posted_within":"7d","sort":"date","refresh":true,"limit":2}`, &res)
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	// Defaults fill what the request left out, exactly as in the CLI.
	if !slices.Equal(rec.filters.Countries, []string{"IN"}) || rec.filters.PostedWithin != 7*24*time.Hour ||
		!slices.Equal(rec.filters.Areas, []model.Area{model.AreaBackend}) {
		t.Errorf("filters = %+v", rec.filters)
	}
	if rec.opts != (SearchOptions{Refresh: true, Sort: "date"}) {
		t.Errorf("options = %+v", rec.opts)
	}
	if res.Fetched != 1200 || res.Matched != 3 || len(res.Jobs) != 2 || !res.RunAt.Equal(t0) {
		t.Errorf("response = fetched %d matched %d jobs %d at %v", res.Fetched, res.Matched, len(res.Jobs), res.RunAt)
	}
	if res.Jobs[0].Status != store.Applied || res.Jobs[1].Status != "" {
		t.Errorf("statuses = %q %q, want applied and none", res.Jobs[0].Status, res.Jobs[1].Status)
	}
	if len(res.Sources) != 2 || res.Sources[0].DurationMS != 2000 || res.Sources[1].Link == "" {
		t.Errorf("sources = %+v", res.Sources)
	}
	if len(res.Links) == 0 {
		t.Error("no browser links")
	}
}

func TestSearchRejectsBadInput(t *testing.T) {
	s, _, rec := apiServer(t)
	tests := []struct {
		name, body, ctype string
		want              int
	}{
		{"unknown field (typo)", `{"countrys":["IN"]}`, "application/json", http.StatusBadRequest},
		{"unknown country", `{"countries":["Atlantis"]}`, "application/json", http.StatusBadRequest},
		{"bad age", `{"posted_within":"soon"}`, "application/json", http.StatusBadRequest},
		{"bad sort", `{"sort":"random"}`, "application/json", http.StatusBadRequest},
		{"negative limit", `{"limit":-1}`, "application/json", http.StatusBadRequest},
		{"not JSON", `countries=IN`, "application/json", http.StatusBadRequest},
		{"two values", `{}{}`, "application/json", http.StatusBadRequest},
		{"form content type", `{}`, "application/x-www-form-urlencoded", http.StatusUnsupportedMediaType},
		{"no content type", `{}`, "", http.StatusUnsupportedMediaType},
		{"too large", `{"keywords":["` + strings.Repeat("a", maxBody) + `"]}`, "application/json", http.StatusRequestEntityTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := req(http.MethodPost, "/api/search", tt.body)
			r.Header.Set("Content-Type", tt.ctype)
			w := serve(s, r)
			if w.Code != tt.want || !strings.Contains(w.Body.String(), `"error"`) {
				t.Fatalf("status %d (%s), want %d", w.Code, w.Body, tt.want)
			}
		})
	}
	if rec.calls.Load() != 0 {
		t.Errorf("search ran %d times for invalid requests", rec.calls.Load())
	}
}

func TestInternalErrorsAreNotLeaked(t *testing.T) {
	s, _ := newTestServer(t, func(o *Options) {
		o.Search = func(context.Context, model.Filters, SearchOptions, func(search.Report)) (*search.Result, error) {
			return nil, errors.New("open /home/user/.local/share/jobhunt/jobhunt.db: disk I/O error")
		}
	})
	w := serve(s, req(http.MethodPost, "/api/search", `{}`))
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "/home") {
		t.Fatalf("got %d %s; want a generic 500", w.Code, w.Body)
	}
}

func TestSearchWaitsForRunningSearch(t *testing.T) {
	s, _, rec := apiServer(t)
	s.searching <- struct{}{} // another search holds the slot
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	w := serve(s, req(http.MethodPost, "/api/search", `{}`).WithContext(ctx))
	if rec.calls.Load() != 0 {
		t.Errorf("search ran while another held the slot (status %d)", w.Code)
	}
	<-s.searching
	if code := call(t, s, http.MethodPost, "/api/search", `{}`, nil); code != http.StatusOK || rec.calls.Load() != 1 {
		t.Errorf("after the slot freed: status %d, calls %d", code, rec.calls.Load())
	}
}

func TestJobByKey(t *testing.T) {
	s, _, _ := apiServer(t)
	var j jobOut
	tests := []struct {
		path string
		want int
	}{
		{"/api/jobs/aaaa1111", http.StatusOK},
		{"/api/jobs/bbbb", http.StatusOK}, // unique prefix
		{"/api/jobs/aaaa", http.StatusConflict},
		{"/api/jobs/ffff", http.StatusNotFound},
	}
	for _, tt := range tests {
		if code := call(t, s, http.MethodGet, tt.path, "", &j); code != tt.want {
			t.Errorf("GET %s = %d, want %d", tt.path, code, tt.want)
		}
	}
	call(t, s, http.MethodGet, "/api/jobs/bbbb", "", &j)
	if j.Key != "bbbb3333" || j.Title != "Data Intern" {
		t.Errorf("job = %+v", j)
	}
}

func TestTrackerFlow(t *testing.T) {
	s, _, _ := apiServer(t)
	var tr trackedOut
	if code := call(t, s, http.MethodPut, "/api/tracker/aaaa1", `{"status":"applied","note":"via careers page"}`, &tr); code != http.StatusOK {
		t.Fatalf("PUT = %d", code)
	}
	if tr.Key != "aaaa1111" || tr.Status != store.Applied || tr.Notes != "via careers page" || tr.AppliedAt.IsZero() || tr.Job == nil {
		t.Errorf("tracked = %+v", tr)
	}
	call(t, s, http.MethodPut, "/api/tracker/bbbb", `{"status":"saved"}`, nil)

	var list []trackedOut
	call(t, s, http.MethodGet, "/api/tracker?status=applied", "", &list)
	if len(list) != 1 || list[0].Key != "aaaa1111" {
		t.Errorf("applied = %+v", list)
	}
	call(t, s, http.MethodGet, "/api/tracker", "", &list)
	if len(list) != 2 {
		t.Errorf("all = %d rows, want 2", len(list))
	}
	if code := call(t, s, http.MethodGet, "/api/tracker?status=dreaming", "", nil); code != http.StatusBadRequest {
		t.Errorf("bad status filter = %d", code)
	}

	call(t, s, http.MethodPut, "/api/tracker/aaaa1111", `{"status":"interview"}`, nil)
	var evs []eventOut
	call(t, s, http.MethodGet, "/api/tracker/aaaa1111/events", "", &evs)
	if len(evs) != 2 || evs[0].Status != store.Applied || evs[1].Status != store.Interview {
		t.Errorf("events = %+v", evs)
	}

	for _, tt := range []struct {
		name, body string
		want       int
	}{
		{"unknown status", `{"status":"hired-ish"}`, http.StatusBadRequest},
		{"note too long", `{"status":"saved","note":"` + strings.Repeat("x", maxNoteLen+1) + `"}`, http.StatusBadRequest},
	} {
		if code := call(t, s, http.MethodPut, "/api/tracker/aaaa1111", tt.body, nil); code != tt.want {
			t.Errorf("%s: %d, want %d", tt.name, code, tt.want)
		}
	}

	if code := call(t, s, http.MethodDelete, "/api/tracker/aaaa1111", "", nil); code != http.StatusNoContent {
		t.Errorf("DELETE = %d", code)
	}
	call(t, s, http.MethodGet, "/api/tracker?status=interview", "", &list)
	if list == nil || len(list) != 0 {
		t.Errorf("after delete = %#v, want an empty array (not null)", list)
	}
}

func TestHide(t *testing.T) {
	s, st, _ := apiServer(t)
	hidden := func() bool {
		keys, err := st.HiddenKeys(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return keys["bbbb3333"]
	}
	if code := call(t, s, http.MethodPost, "/api/jobs/bbbb/hide", "", nil); code != http.StatusNoContent || !hidden() {
		t.Fatalf("hide = %d, hidden %v", code, hidden())
	}
	if code := call(t, s, http.MethodDelete, "/api/jobs/bbbb/hide", "", nil); code != http.StatusNoContent || hidden() {
		t.Fatalf("unhide = %d, hidden %v", code, hidden())
	}
}

func TestPresets(t *testing.T) {
	s, _, _ := apiServer(t)
	var p presetOut
	if code := call(t, s, http.MethodPut, "/api/presets/india-backend", `{"types":["internship"],"areas":["backend"],"posted_within":"72h"}`, &p); code != http.StatusOK {
		t.Fatalf("PUT = %d", code)
	}
	if p.Name != "india-backend" || !slices.Equal(p.Filters.Countries, []string{"IN"}) || p.Filters.PostedWithin != "3d" {
		t.Errorf("saved = %+v", p)
	}
	var list []presetOut
	call(t, s, http.MethodGet, "/api/presets", "", &list)
	if len(list) != 1 {
		t.Errorf("list = %+v", list)
	}
	for _, tt := range []struct {
		method, path, body string
		want               int
	}{
		{http.MethodGet, "/api/presets/india-backend", "", http.StatusOK},
		{http.MethodGet, "/api/presets/missing", "", http.StatusNotFound},
		{http.MethodPut, "/api/presets/-bad", `{}`, http.StatusBadRequest},
		{http.MethodPut, "/api/presets/" + strings.Repeat("a", 65), `{}`, http.StatusBadRequest},
		{http.MethodPut, "/api/presets/x", `{"countries":["Atlantis"]}`, http.StatusBadRequest},
		{http.MethodDelete, "/api/presets/india-backend", "", http.StatusNoContent},
		{http.MethodDelete, "/api/presets/india-backend", "", http.StatusNotFound},
	} {
		if code := call(t, s, tt.method, tt.path, tt.body, nil); code != tt.want {
			t.Errorf("%s %s = %d, want %d", tt.method, tt.path, code, tt.want)
		}
	}
}

func TestSources(t *testing.T) {
	s, st, _ := apiServer(t)
	ctx := context.Background()
	for _, r := range []store.SourceRun{
		{Source: "greenhouse", RunAt: t0, Status: "ok", Count: 900, Duration: time.Second},
		{Source: "remotive", RunAt: t0, Status: "error", Error: "timeout"}, // ran before, now disabled
	} {
		if err := st.RecordRun(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	var out []sourceStatus
	if code := call(t, s, http.MethodGet, "/api/sources", "", &out); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	byName := map[string]sourceStatus{}
	for _, o := range out {
		byName[o.Name] = o
	}
	if g := byName["greenhouse"]; !g.Enabled || !g.Ready || g.Last == nil || g.Last.Count != 900 || g.LastSuccess.IsZero() {
		t.Errorf("greenhouse = %+v", g)
	}
	if j := byName["jsearch"]; !j.Enabled || j.Ready || !strings.Contains(j.NotReady, "missing") || j.Last != nil {
		t.Errorf("jsearch = %+v", j)
	}
	if r := byName["remotive"]; r.Enabled || r.Last == nil || r.RecentErrors != 1 {
		t.Errorf("remotive = %+v", r)
	}
	// Regression: disabled sources that never ran must still be listed.
	if a, ok := byName["adzuna"]; !ok || a.Enabled || a.Ready || a.Credential != "app_id + app_key" || a.Description == "" || a.Last != nil {
		t.Errorf("adzuna = %+v (listed %v)", a, ok)
	}
	if g := byName["greenhouse"]; g.Description == "" || g.Credential != "" {
		t.Errorf("greenhouse description/credential = %q/%q", g.Description, g.Credential)
	}
	var names []string
	for _, o := range out {
		names = append(names, o.Name)
	}
	// Catalog order first, then sources only known from past runs, by name.
	if want := []string{"greenhouse", "jsearch", "adzuna", "remotive"}; !slices.Equal(names, want) {
		t.Errorf("order = %v, want %v", names, want)
	}
}

func TestMeta(t *testing.T) {
	s, _, _ := apiServer(t)
	var m struct {
		APIVersion int            `json:"api_version"`
		Defaults   map[string]any `json:"defaults"`
		Options    struct {
			Countries []string `json:"countries"`
			Areas     []string `json:"areas"`
			Statuses  []string `json:"statuses"`
		} `json:"options"`
	}
	if code := call(t, s, http.MethodGet, "/api/meta", "", &m); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if m.APIVersion != APIVersion || !slices.Contains(m.Options.Countries, "IN") ||
		len(m.Options.Areas) != len(model.AllAreas) || len(m.Options.Statuses) != len(store.Statuses) {
		t.Errorf("meta = %+v", m)
	}
	if c, _ := m.Defaults["countries"].([]any); len(c) != 1 || c[0] != "IN" {
		t.Errorf("defaults = %+v", m.Defaults)
	}
}

func TestLinks(t *testing.T) {
	s, _, rec := apiServer(t)
	var out []linkOut
	if code := call(t, s, http.MethodGet, "/api/links?countries=IN&types=internship&keywords=Go", "", &out); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	sites := map[string]bool{}
	for _, l := range out {
		sites[l.Site] = true
		if !strings.HasPrefix(l.URL, "https://") {
			t.Errorf("%s link %q isn't https", l.Site, l.URL)
		}
	}
	if !sites["LinkedIn"] || len(out) < 3 {
		t.Errorf("links = %+v", out)
	}
	if rec.calls.Load() != 0 {
		t.Error("/api/links ran a search")
	}
	for _, q := range []string{"countries=Atlantis", "colour=blue", "visa=maybe"} {
		if code := call(t, s, http.MethodGet, "/api/links?"+q, "", nil); code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", q, code)
		}
	}
	// Empty filters take the config defaults, like a search would.
	if code := call(t, s, http.MethodGet, "/api/links", "", &out); code != http.StatusOK || len(out) == 0 {
		t.Errorf("default links: %d, %+v", code, out)
	}
}

func TestTrackerNotes(t *testing.T) {
	s, _, _ := apiServer(t)
	call(t, s, http.MethodPut, "/api/tracker/aaaa1111", `{"status":"applied","note":"first"}`, nil)
	var tr trackedOut
	if code := call(t, s, http.MethodPut, "/api/tracker/aaaa1/notes", `{"notes":"  recruiter: Priya\nfollow up Friday  "}`, &tr); code != http.StatusOK {
		t.Fatalf("PUT notes = %d", code)
	}
	if tr.Notes != "recruiter: Priya\nfollow up Friday" || tr.Status != store.Applied {
		t.Errorf("tracked = %+v", tr)
	}
	// Editing notes replaces them and leaves the status history alone.
	var evs []eventOut
	call(t, s, http.MethodGet, "/api/tracker/aaaa1111/events", "", &evs)
	if len(evs) != 1 || evs[0].Note != "first" {
		t.Errorf("events = %+v", evs)
	}
	var cleared trackedOut // notes is omitempty: decode into a fresh value
	if code := call(t, s, http.MethodPut, "/api/tracker/aaaa1111/notes", `{"notes":""}`, &cleared); code != http.StatusOK || cleared.Notes != "" {
		t.Errorf("clear notes = %d, %+v", code, cleared)
	}
	for _, tt := range []struct {
		name, path, body string
		want             int
	}{
		{"not tracked", "/api/tracker/bbbb3333/notes", `{"notes":"x"}`, http.StatusNotFound},
		{"unknown job", "/api/tracker/ffff/notes", `{"notes":"x"}`, http.StatusNotFound},
		{"too long", "/api/tracker/aaaa1111/notes", `{"notes":"` + strings.Repeat("x", maxNotesLen+1) + `"}`, http.StatusBadRequest},
		{"unknown field", "/api/tracker/aaaa1111/notes", `{"note":"x"}`, http.StatusBadRequest},
	} {
		if code := call(t, s, http.MethodPut, tt.path, tt.body, nil); code != tt.want {
			t.Errorf("%s: %d, want %d", tt.name, code, tt.want)
		}
	}
}
