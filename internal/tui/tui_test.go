package tui

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/itxkarthik/jobhunt/internal/app"
	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/search"
	"github.com/itxkarthik/jobhunt/internal/store"
)

func testModel(t *testing.T) (*Model, *store.Store) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	m := New(&app.App{Store: st}, model.Filters{Countries: []string{"IN"}})
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	jobs := []model.Job{
		{Key: "aaa111", Title: "Backend Intern", Company: "Zeta", Score: 80, PostedAt: time.Now(), URL: "https://x/1", New: true},
		{Key: "bbb222", Title: "Go Developer Intern", Company: "Acme", Score: 60, PostedAt: time.Now().Add(-48 * time.Hour), URL: "https://x/2"},
	}
	m.Update(resultMsg{gen: m.gen, res: &search.Result{Jobs: jobs, Total: 100}})
	return m, st
}

func keys(m *Model, ks ...string) {
	for _, k := range ks {
		var msg tea.KeyPressMsg
		switch k {
		case "tab":
			msg = tea.KeyPressMsg{Code: tea.KeyTab}
		case "down":
			msg = tea.KeyPressMsg{Code: tea.KeyDown}
		default:
			msg = tea.KeyPressMsg{Code: []rune(k)[0], Text: k}
		}
		m.Update(msg)
	}
}

func TestViewShowsResults(t *testing.T) {
	m, _ := testModel(t)
	v := m.render()
	for _, want := range []string{"Backend Intern", "Acme", "2 jobs (of 100 fetched)", "[x] IN", "★"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q", want)
		}
	}
}

func TestTrackAndHide(t *testing.T) {
	m, st := testModel(t)
	keys(m, "a") // applied on first row
	if got := m.statuses["aaa111"]; got != store.Applied {
		t.Fatalf("status = %q, want applied", got)
	}
	keys(m, "down", "S")
	if got := m.statuses["bbb222"]; got != store.Saved {
		t.Fatalf("status = %q, want saved", got)
	}
	keys(m, "h")
	if len(m.jobs) != 1 {
		t.Fatalf("after hide: %d jobs, want 1", len(m.jobs))
	}
	hidden, _ := st.HiddenKeys(t.Context())
	if !hidden["bbb222"] {
		t.Error("bbb222 not hidden in store")
	}
}

func TestSortCycle(t *testing.T) {
	m, _ := testModel(t)
	keys(m, "s", "s") // score -> date -> company
	if m.jobs[0].Company != "Acme" {
		t.Errorf("company sort: first = %s", m.jobs[0].Company)
	}
}

func TestNewOnly(t *testing.T) {
	m, _ := testModel(t)
	keys(m, "n")
	// A refetch reports nothing new (the first search marked it seen), but
	// the job that was new when the TUI opened stays in view with its ★.
	m.Update(resultMsg{gen: m.gen, res: &search.Result{Jobs: []model.Job{{Key: "aaa111", Title: "Backend Intern"}, {Key: "bbb222"}}}})
	if len(m.jobs) != 1 || m.jobs[0].Key != "aaa111" || !m.jobs[0].New || !strings.Contains(m.render(), "new only") {
		t.Fatalf("new only: %+v", m.jobs)
	}
	if keys(m, "n"); len(m.jobs) != 2 {
		t.Errorf("all again: %d jobs", len(m.jobs))
	}
}

func TestStaleResultIgnored(t *testing.T) {
	m, _ := testModel(t)
	m.Update(resultMsg{gen: m.gen - 1, res: &search.Result{}})
	if len(m.jobs) != 2 {
		t.Error("stale result replaced jobs")
	}
}

func TestToggleFilter(t *testing.T) {
	f := model.Filters{}
	m := &Model{filters: f}
	o := option{"area", "backend"}
	m.toggle(o)
	if !m.isOn(o) {
		t.Fatal("not on after toggle")
	}
	m.toggle(o)
	if m.isOn(o) {
		t.Fatal("still on after second toggle")
	}
}

func TestSidebarSpaceToggles(t *testing.T) {
	m, _ := testModel(t)
	var got model.Filters
	m.search = func(_ context.Context, f model.Filters, _ bool, _ func(search.Report)) (*search.Result, error) {
		got = f
		return &search.Result{}, nil
	}
	keys(m, "tab", "tab") // table -> detail -> filters
	if m.focus != paneFilters {
		t.Fatalf("focus = %d", m.focus)
	}
	keys(m, "j") // US
	cmd := func() tea.Cmd { _, c := m.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}); return c }()
	if !slices.Contains(m.filters.Countries, "US") {
		t.Fatalf("countries = %v, want US toggled on", m.filters.Countries)
	}
	if cmd == nil {
		t.Fatal("toggle did not start a search")
	}
	for msg := cmd(); msg != nil; msg = wait(m.ch)() {
		m.Update(msg)
	}
	if !slices.Contains(got.Countries, "US") || m.loading {
		t.Errorf("search got %v, loading=%v", got.Countries, m.loading)
	}
}
