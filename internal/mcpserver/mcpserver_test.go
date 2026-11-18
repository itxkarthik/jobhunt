package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/itxkarthik/jobhunt/internal/app"
	"github.com/itxkarthik/jobhunt/internal/classify"
	"github.com/itxkarthik/jobhunt/internal/config"
	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources"
	"github.com/itxkarthik/jobhunt/internal/sources/ats"
	"github.com/itxkarthik/jobhunt/internal/store"
)

type fakeSource struct{ jobs []model.Job }

func (f *fakeSource) Name() string                { return "fake" }
func (f *fakeSource) Supports(model.Filters) bool { return true }
func (f *fakeSource) Fetch(context.Context, model.Filters) ([]model.Job, error) {
	return f.jobs, nil
}

type env struct {
	t     *testing.T
	cs    *mcp.ClientSession
	st    *store.Store
	log   *bytes.Buffer
	check int // board checks made
}

func newEnv(t *testing.T, readOnly bool) *env {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	src := &fakeSource{jobs: []model.Job{{
		ID: "fake:1", Source: "fake", Title: "Backend Engineering Intern", Company: "Acme AI",
		Location: "Bengaluru, India", Description: strings.Repeat("Go services. ", 100),
		URL: "https://example.com/jobs/1", PostedAt: time.Now().Add(-time.Hour),
	}}}
	a := &app.App{Config: config.Default(), Store: st, Classifier: classify.Default(), Boards: ats.NewBoards(nil), Sources: []sources.Source{src}}
	e := &env{t: t, st: st, log: &bytes.Buffer{}}
	srv := New(Options{
		App: a, ReadOnly: readOnly, Version: "test",
		Log: slog.New(slog.NewTextHandler(e.log, nil)),
		CheckBoard: func(_ context.Context, c ats.Company) (int, error) {
			e.check++
			if c.Slug == "dead" {
				return 0, &sources.StatusError{Code: 404}
			}
			if c.Slug == "empty" {
				return 0, nil
			}
			return 3, nil
		},
	})
	st1, st2 := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st1, nil)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil).Connect(ctx, st2, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close(); _ = ss.Wait() })
	e.cs = cs
	return e
}

// call runs a tool, decoding its structured result into out. It returns
// the tool error text, "" on success.
func (e *env) call(name string, args any, out any) string {
	e.t.Helper()
	res, err := e.cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		e.t.Fatalf("%s: protocol error %v", name, err)
	}
	if res.IsError {
		var msg []string
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				msg = append(msg, tc.Text)
			}
		}
		return strings.Join(msg, " ")
	}
	if out != nil {
		b, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(b, out); err != nil {
			e.t.Fatalf("%s: decoding %s: %v", name, b, err)
		}
	}
	return ""
}

func TestTools(t *testing.T) {
	ctx := context.Background()
	names := func(e *env) []string {
		res, err := e.cs.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, tl := range res.Tools {
			out = append(out, tl.Name)
		}
		slices.Sort(out)
		return out
	}
	read := []string{"get_company", "get_job", "list_companies", "list_sources", "search_jobs"}
	if got := names(newEnv(t, true)); !slices.Equal(got, read) {
		t.Errorf("read-only tools = %v", got)
	}
	all := append(slices.Clone(read), "add_board", "add_contact", "add_note", "track_job", "upsert_company")
	slices.Sort(all)
	if got := names(newEnv(t, false)); !slices.Equal(got, all) {
		t.Errorf("tools = %v", got)
	}
	// A read-only server refuses write tools outright.
	e := newEnv(t, true)
	if _, err := e.cs.CallTool(ctx, &mcp.CallToolParams{Name: "upsert_company", Arguments: map[string]any{"name": "X"}}); err == nil {
		t.Error("read-only server ran upsert_company")
	}
}

func TestSearchAndTrack(t *testing.T) {
	e := newEnv(t, false)
	var out SearchOutput
	all := []string{"any"}
	if msg := e.call("search_jobs", map[string]any{"countries": all, "types": all, "areas": all, "modes": all, "include_unknown": true}, &out); msg != "" {
		t.Fatal(msg)
	}
	if len(out.Jobs) != 1 || out.Jobs[0].Company != "Acme AI" || len([]rune(out.Jobs[0].Description)) > maxDescList+1 || len(out.Sources) != 1 {
		t.Fatalf("search = %+v", out)
	}
	key := out.Jobs[0].Key

	if msg := e.call("search_jobs", map[string]any{"limit": 1000}, nil); !strings.Contains(msg, "limit") {
		t.Errorf("limit 1000: %q", msg)
	}
	if msg := e.call("search_jobs", map[string]any{"types": []string{"astronaut"}}, nil); msg == "" {
		t.Error("bad type accepted")
	}

	var job JobBrief
	if msg := e.call("get_job", map[string]any{"key": key[:8]}, &job); msg != "" || job.Key != key || len(job.Description) < 1000 {
		t.Errorf("get_job: %q %+v", msg, job.Key)
	}
	if msg := e.call("get_job", map[string]any{"key": "ab"}, nil); !strings.Contains(msg, "4 characters") {
		t.Errorf("short key: %q", msg)
	}

	var tr TrackOutput
	if msg := e.call("track_job", map[string]any{"key": key[:8], "status": "saved", "note": "fits\u202e"}, &tr); msg != "" || tr.Status != store.Saved {
		t.Errorf("track: %q %+v", msg, tr)
	}
	if msg := e.call("track_job", map[string]any{"key": key, "status": "hired"}, nil); msg == "" {
		t.Error("bad status accepted")
	}
	evs, _ := e.st.Events(context.Background(), key)
	if len(evs) != 1 || strings.ContainsRune(evs[0].Note, '‮') {
		t.Errorf("events = %+v (note must be sanitized)", evs)
	}
}

func TestCompanyTools(t *testing.T) {
	e := newEnv(t, false)
	src := "https://example.com/news/acme-seed"
	var co CompanyOutput
	msg := e.call("upsert_company", map[string]any{
		"name": "Acme AI", "country": "India", "city": "Bengaluru", "headcount": "25", "headcount_source": "estimate",
		"rounds":   []map[string]any{{"stage": "seed", "amount": 3e6, "currency": "USD", "date": "2026-08-01T00:00:00Z", "source_url": src}},
		"contacts": []map[string]any{{"name": "Asha Rao", "role": "CEO", "url": "https://x.com/asha", "source_url": src}},
		"tags":     []string{"go"},
	}, &co)
	if msg != "" || !co.Created || co.Company.Country != "IN" || co.Company.Stage != model.StageSeed || co.Company.Headcount != model.Headcount11to50 || co.Company.Sources[0] != "agent" {
		t.Fatalf("upsert: %q %+v", msg, co)
	}

	// Validation errors reach the agent; nothing is stored.
	for _, tc := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"upsert_company", map[string]any{"name": "Beta", "contacts": []map[string]any{{"name": "Bo", "email": "bo@beta.io"}}}, "source"},
		{"upsert_company", map[string]any{"name": "Beta", "website": "javascript:alert(1)"}, "website"},
		{"upsert_company", map[string]any{"name": "Beta", "stage": "series-z"}, "stage"},
		{"add_contact", map[string]any{"company": "Nobody Inc", "name": "X", "source_url": src}, "no company"},
		{"add_contact", map[string]any{"company": "Acme", "email": "Asha <a@acme.ai>", "source_url": src}, "email"},
		{"add_board", map[string]any{"company": "Acme AI", "ats": "lever", "slug": "dead"}, "lever:dead"},
		{"add_board", map[string]any{"company": "Acme AI", "ats": "lever", "slug": "empty"}, "no jobs"},
		{"add_board", map[string]any{"company": "Acme AI", "ats": "monster", "slug": "acme"}, "ats"},
		{"add_note", map[string]any{"company": "Acme AI", "note": "  "}, "note"},
		{"add_note", map[string]any{"company": "Acme AI", "note": "ok", "source_url": "file:///etc/passwd"}, "source_url"},
	} {
		if msg := e.call(tc.tool, tc.args, nil); !strings.Contains(strings.ToLower(msg), tc.want) {
			t.Errorf("%s %v: error %q, want %q", tc.tool, tc.args, msg, tc.want)
		}
	}
	if cs, _ := e.st.Companies(context.Background()); len(cs) != 1 {
		t.Errorf("%d companies stored, want 1", len(cs))
	}

	if msg := e.call("add_contact", map[string]any{"company": "acme", "name": "Ravi K", "role": "CTO", "email": "ravi@acme.ai", "source_url": src}, &co); msg != "" || len(co.Company.Contacts) != 2 {
		t.Errorf("add_contact: %q %+v", msg, co.Company.Contacts)
	}
	checks := e.check
	if msg := e.call("add_board", map[string]any{"company": "Acme AI", "ats": "ashby", "slug": "acme"}, &co); msg != "" || len(co.Company.Boards) != 1 || e.check != checks+1 {
		t.Errorf("add_board: %q %+v", msg, co.Company.Boards)
	}
	if msg := e.call("add_note", map[string]any{"company": "Acme AI", "note": "Small Go team", "source_url": src}, &co); msg != "" || !strings.Contains(co.Company.Notes, "Small Go team ("+src+")") {
		t.Errorf("add_note: %q %q", msg, co.Company.Notes)
	}
	e.call("add_note", map[string]any{"company": "Acme AI", "note": "Second"}, &co)
	if strings.Count(co.Company.Notes, "\n") != 1 {
		t.Errorf("notes don't append: %q", co.Company.Notes)
	}

	var rmOut CompanyOutput
	if msg := e.call("upsert_company", map[string]any{"name": "Acme AI", "remove": map[string]any{"tags": []string{"go"}, "contacts": []string{"Ravi K"}}}, &rmOut); msg != "" || len(rmOut.Company.Tags) != 0 || len(rmOut.Company.Contacts) != 1 {
		t.Errorf("remove: %q tags %v contacts %+v", msg, rmOut.Company.Tags, rmOut.Company.Contacts)
	}

	var list ListCompaniesOutput
	if msg := e.call("list_companies", map[string]any{"stages": []string{"seed"}, "countries": []string{"IN"}}, &list); msg != "" || len(list.Companies) != 1 {
		t.Errorf("list: %q %+v", msg, list)
	}
	if msg := e.call("list_companies", map[string]any{"funded_within": "forever"}, nil); msg == "" {
		t.Error("bad funded_within accepted")
	}
	var view app.CompanyView
	if msg := e.call("get_company", map[string]any{"name": "acme"}, &view); msg != "" || view.Name != "Acme AI" || len(view.Contacts) != 1 {
		t.Errorf("get_company: %q %+v", msg, view)
	}
}

func TestInternalErrorsStayPrivate(t *testing.T) {
	e := newEnv(t, false)
	e.st.Close() // every query now fails with a driver error
	msg := e.call("list_companies", map[string]any{}, nil)
	if msg != "internal error" {
		t.Errorf("error shown to agent: %q", msg)
	}
	if !strings.Contains(e.log.String(), "list_companies") {
		t.Errorf("error not logged: %s", e.log)
	}
	h := &handlers{o: Options{Log: slog.New(slog.DiscardHandler)}}
	if err := h.public("x", invalid("bad %s", "input")); err.Error() != "bad input" {
		t.Errorf("validation error = %v", err)
	}
	if err := h.public("x", errors.New("sql: secret detail")); err.Error() != "internal error" {
		t.Errorf("internal error = %v", err)
	}
}

func TestListSources(t *testing.T) {
	e := newEnv(t, false)
	var out SourcesOutput
	if msg := e.call("list_sources", map[string]any{}, &out); msg != "" || len(out.Sources) != len(app.Catalog) {
		t.Fatalf("list_sources: %q, %d sources", msg, len(out.Sources))
	}
	b, _ := json.Marshal(out)
	for _, bad := range []string{"api_key", "bearer", "token", "app_key"} {
		if strings.Contains(strings.ToLower(string(b)), bad) {
			t.Errorf("list_sources output mentions %q", bad)
		}
	}
}
