package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/itxkarthik/jobhunt/internal/model"
)

func TestMarkSeenNew(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	t0 := time.Unix(1_800_000_000, 0)
	jobs := []model.Job{{Key: "aaa111", Title: "A"}, {Key: "bbb222", Title: "B"}}
	if err := s.MarkSeen(ctx, jobs, t0); err != nil {
		t.Fatal(err)
	}
	for _, j := range jobs {
		if !j.New {
			t.Errorf("%s: want new on first run", j.Key)
		}
	}
	jobs2 := []model.Job{{Key: "aaa111"}, {Key: "ccc333"}}
	if err := s.MarkSeen(ctx, jobs2, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if jobs2[0].New || !jobs2[0].FirstSeenAt.Equal(t0) {
		t.Errorf("aaa111: New=%v first=%v, want old with first=%v", jobs2[0].New, jobs2[0].FirstSeenAt, t0)
	}
	if !jobs2[1].New {
		t.Error("ccc333: want new")
	}
	j, err := s.Job(ctx, "bbb222")
	if err != nil || j == nil || j.Title != "B" {
		t.Fatalf("Job(bbb222) = %+v, %v", j, err)
	}
}

func TestTracker(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	if err := s.MarkSeen(ctx, []model.Job{{Key: "abc123", Title: "Backend Intern"}, {Key: "abd999"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveKey(ctx, "ab"); err == nil {
		t.Error("ResolveKey(ab): want ambiguous error")
	}
	key, err := s.ResolveKey(ctx, "abc")
	if err != nil || key != "abc123" {
		t.Fatalf("ResolveKey(abc) = %q, %v", key, err)
	}
	steps := []struct {
		st   Status
		note string
	}{{Saved, ""}, {Applied, "via referral"}, {OA, "HackerRank"}}
	for _, st := range steps {
		if err := s.SetStatus(ctx, key, st.st, st.note); err != nil {
			t.Fatal(err)
		}
	}
	tr, err := s.TrackedJobs(ctx)
	if err != nil || len(tr) != 1 {
		t.Fatalf("TrackedJobs = %v, %v", tr, err)
	}
	got := tr[0]
	if got.Status != OA || got.Notes != "via referral\nHackerRank" || got.AppliedAt.IsZero() || got.Job == nil || got.Job.Title != "Backend Intern" {
		t.Errorf("tracked = %+v", got)
	}
	if only, _ := s.TrackedJobs(ctx, Offer); len(only) != 0 {
		t.Errorf("filter by offer: got %d rows", len(only))
	}
	ev, _ := s.Events(ctx, key)
	if len(ev) != 3 || ev[2].Status != OA {
		t.Errorf("events = %+v", ev)
	}
}

func TestHidden(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	_ = s.Hide(ctx, "k1")
	h, _ := s.HiddenKeys(ctx)
	if !h["k1"] {
		t.Fatal("k1 not hidden")
	}
	_ = s.Unhide(ctx, "k1")
	h, _ = s.HiddenKeys(ctx)
	if h["k1"] {
		t.Fatal("k1 still hidden")
	}
}

func TestParseStatus(t *testing.T) {
	tests := []struct {
		in   string
		want Status
		err  bool
	}{{"applied", Applied, false}, {" OA ", OA, false}, {"assessment", OA, false}, {"ghosted", "", true}}
	for _, tt := range tests {
		got, err := ParseStatus(tt.in)
		if (err != nil) != tt.err || got != tt.want {
			t.Errorf("ParseStatus(%q) = %q, %v", tt.in, got, err)
		}
	}
}

func openMem(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSetNotes(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	if err := s.MarkSeen(ctx, []model.Job{{Key: "aaa111", Title: "A"}}, time.Unix(1_800_000_000, 0)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNotes(ctx, "aaa111", "x"); !errors.Is(err, ErrNotTracked) {
		t.Fatalf("untracked: err = %v, want ErrNotTracked", err)
	}
	if err := s.SetStatus(ctx, "aaa111", Applied, "one"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNotes(ctx, "aaa111", "rewritten"); err != nil {
		t.Fatal(err)
	}
	rows, err := s.TrackedJobs(ctx)
	if err != nil || len(rows) != 1 || rows[0].Notes != "rewritten" || rows[0].Status != Applied {
		t.Fatalf("rows = %+v, %v", rows, err)
	}
	evs, err := s.Events(ctx, "aaa111")
	if err != nil || len(evs) != 1 || evs[0].Note != "one" {
		t.Errorf("history = %+v, %v; editing notes must not add events", evs, err)
	}
}

func TestPruneAndListed(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	old := time.Now().Add(-KeepSeen - time.Hour)
	jobs := []model.Job{{Key: "gone11", URL: "https://x/1"}, {Key: "kept22", URL: "https://x/2"}}
	if err := s.MarkSeen(ctx, jobs, old); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatus(ctx, "kept22", Applied, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Prune(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if j, _ := s.Job(ctx, "gone11"); j != nil {
		t.Error("untracked job not seen for KeepSeen was kept")
	}
	keys, byURL, err := s.TrackedKeys(ctx)
	if err != nil || !keys["kept22"] || byURL["https://x/2"] != "kept22" {
		t.Fatalf("tracked job pruned or unmatched: %v %v %v", keys, byURL, err)
	}
	if err := s.MarkListed(ctx, []string{"kept22"}, old); err != nil {
		t.Fatal(err)
	}
	rows, err := s.TrackedJobs(ctx)
	if err != nil || len(rows) != 1 || !rows[0].Unlisted(time.Now()) {
		t.Fatalf("want one unlisted row, got %+v %v", rows, err)
	}
}
