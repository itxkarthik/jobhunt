package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/itxkarthik/jobhunt/internal/feed"
	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/search"
	"github.com/itxkarthik/jobhunt/internal/store"
)

// fakeRuns simulates successive searches against a real store: each call
// marks its jobs seen at the next run time, exactly as the engine does.
type fakeRuns struct {
	st   *store.Store
	runs [][]model.Job
	at   []time.Time
	n    int
}

func (f *fakeRuns) search(ctx context.Context) (*search.Result, error) {
	jobs := slices.Clone(f.runs[f.n])
	at := f.at[f.n]
	f.n++
	if err := f.st.MarkSeen(ctx, jobs, at); err != nil {
		return nil, err
	}
	return &search.Result{RunAt: at, Jobs: jobs, Total: len(jobs)}, nil
}

func job(key string, score float64) model.Job {
	return model.Job{Key: key, Title: "Intern " + key, Company: "Co", URL: "https://example.com/" + key, Score: score}
}

// emitted decodes stdout and returns the job keys in order.
func emitted(t *testing.T, out []byte) []string {
	t.Helper()
	var keys []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		var e feed.Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("bad NDJSON line %q: %v", sc.Text(), err)
		}
		if e.Type == feed.TypeJob {
			keys = append(keys, e.Job.Key)
		}
	}
	return keys
}

func newFeeder(t *testing.T, runs *fakeRuns, cursor string, hook *feed.Webhook) (*feeder, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	return &feeder{
		search: runs.search, cursors: runs.st, out: &out, log: &bytes.Buffer{},
		webhook: hook, cursor: cursor, newID: func() string { return "run" },
	}, &out
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestFeedCursorDeliversEachJobOnce(t *testing.T) {
	t0 := time.Unix(1_800_000_000, 0)
	runs := &fakeRuns{
		st: openStore(t),
		runs: [][]model.Job{
			{job("a", 90), job("b", 80)},
			{job("a", 90), job("b", 80)},               // nothing new
			{job("c", 70), job("a", 90), job("b", 80)}, // c is new
		},
		at: []time.Time{t0, t0.Add(6 * time.Hour), t0.Add(12 * time.Hour)},
	}
	want := [][]string{{"a", "b"}, nil, {"c"}}
	for i, w := range want {
		fd, out := newFeeder(t, runs, "agent", nil)
		if err := fd.once(context.Background()); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if got := emitted(t, out.Bytes()); !slices.Equal(got, w) {
			t.Errorf("run %d emitted %v, want %v", i, got, w)
		}
	}
}

func TestFeedCursorsAreIndependent(t *testing.T) {
	t0 := time.Unix(1_800_000_000, 0)
	runs := &fakeRuns{
		st:   openStore(t),
		runs: [][]model.Job{{job("a", 90)}, {job("a", 90)}},
		at:   []time.Time{t0, t0.Add(time.Hour)},
	}
	for _, name := range []string{"agent", "db"} {
		fd, out := newFeeder(t, runs, name, nil)
		if err := fd.once(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := emitted(t, out.Bytes()); !slices.Equal(got, []string{"a"}) {
			t.Errorf("cursor %s emitted %v, want [a]", name, got)
		}
	}
}

func TestFeedWebhookFailureKeepsCursor(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if fail.Load() {
			w.WriteHeader(http.StatusBadRequest) // not retried: keeps the test fast
		}
	}))
	defer srv.Close()
	hook, err := feed.NewWebhook(srv.URL, nil, "test")
	if err != nil {
		t.Fatal(err)
	}

	t0 := time.Unix(1_800_000_000, 0)
	runs := &fakeRuns{
		st:   openStore(t),
		runs: [][]model.Job{{job("a", 90)}, {job("a", 90)}, {job("a", 90)}},
		at:   []time.Time{t0, t0.Add(time.Hour), t0.Add(2 * time.Hour)},
	}

	fd, out := newFeeder(t, runs, "n8n", hook)
	err = fd.once(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not advanced") {
		t.Fatalf("err = %v, want delivery failure that keeps the cursor", err)
	}
	if got := emitted(t, out.Bytes()); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("stdout = %v, want [a] even when the webhook fails", got)
	}

	fail.Store(false)
	fd, out = newFeeder(t, runs, "n8n", hook)
	if err := fd.once(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := emitted(t, out.Bytes()); !slices.Equal(got, []string{"a"}) {
		t.Errorf("after recovery emitted %v, want [a] resent", got)
	}

	fd, out = newFeeder(t, runs, "n8n", hook)
	if err := fd.once(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := emitted(t, out.Bytes()); got != nil {
		t.Errorf("after delivery emitted %v, want nothing", got)
	}
	if calls.Load() != 3 {
		t.Errorf("webhook calls = %d, want 3", calls.Load())
	}
}

func TestFeedStatelessOptions(t *testing.T) {
	t0 := time.Unix(1_800_000_000, 0)
	runs := &fakeRuns{
		st:   openStore(t),
		runs: [][]model.Job{{job("a", 90), job("b", 50)}, {job("a", 90), job("b", 50)}},
		at:   []time.Time{t0, t0.Add(time.Hour)},
	}
	for i := range 2 {
		fd, out := newFeeder(t, runs, "", nil)
		fd.opts = feed.Options{MinScore: 60}
		if err := fd.once(context.Background()); err != nil {
			t.Fatal(err)
		}
		// Without a cursor every run emits the full current selection.
		if got := emitted(t, out.Bytes()); !slices.Equal(got, []string{"a"}) {
			t.Errorf("run %d emitted %v, want [a]", i, got)
		}
	}
}

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 11, 14, 18, 0, 0, 0, time.UTC)
	tests := []struct {
		in   string
		want time.Time
		ok   bool
	}{
		{"", time.Time{}, true},
		{"2026-11-14T09:00:00+05:30", time.Date(2026, 11, 14, 3, 30, 0, 0, time.UTC), true},
		{"2026-11-13", time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC), true},
		{"24h", now.Add(-24 * time.Hour), true},
		{"7d", now.Add(-7 * 24 * time.Hour), true},
		{"yesterday", time.Time{}, false},
		{"-3d", time.Time{}, false},
	}
	for _, tt := range tests {
		got, err := parseSince(tt.in, now)
		if (err == nil) != tt.ok || !got.Equal(tt.want) {
			t.Errorf("parseSince(%q) = %v, %v; want %v ok=%v", tt.in, got, err, tt.want, tt.ok)
		}
	}
}

func TestFeedFlagsValidate(t *testing.T) {
	tests := []struct {
		name string
		fl   feedFlags
		ok   bool
	}{
		{"defaults", feedFlags{}, true},
		{"every 6h", feedFlags{every: 6 * time.Hour}, true},
		{"every too often", feedFlags{every: 5 * time.Minute}, false},
		{"score too high", feedFlags{minScore: 101}, false},
		{"negative score", feedFlags{minScore: -1}, false},
		{"good cursor", feedFlags{cursor: "agent-1"}, true},
		{"bad cursor", feedFlags{cursor: "Agent 1"}, false},
	}
	for _, tt := range tests {
		if err := tt.fl.validate(); (err == nil) != tt.ok {
			t.Errorf("%s: err = %v, want ok=%v", tt.name, err, tt.ok)
		}
	}
}

func TestFeedSinceAndCursorExclusive(t *testing.T) {
	cmd := newFeedCmd()
	cmd.SetArgs([]string{"--since", "24h", "--cursor", "agent"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "none of the others can be") {
		t.Fatalf("err = %v, want mutually exclusive flag error", err)
	}
}
