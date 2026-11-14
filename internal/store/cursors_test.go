package store

import (
	"context"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/itxkarthik/jobhunt/internal/model"
)

func pendingKeys(t *testing.T, s *Store, cursor string) (delivered, pending []string) {
	t.Helper()
	d, p, err := s.FeedState(context.Background(), cursor)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range p {
		pending = append(pending, j.Key)
	}
	return slices.Sorted(maps.Keys(d)), pending
}

func TestFeedDeliveries(t *testing.T) {
	ctx := context.Background()
	s := openMem(t)
	t0 := time.Unix(1_800_000_000, 0)
	jobs := []model.Job{{Key: "a", Title: "A", Score: 90}, {Key: "b", Title: "B", Score: 80}}
	if err := s.MarkSeen(ctx, jobs, t0); err != nil {
		t.Fatal(err)
	}

	steps := []struct {
		name          string
		do            func() error
		wantDelivered []string
		wantPending   []string
	}{
		{"new cursor is empty", func() error { return nil }, nil, nil},
		{"batch goes out", func() error { return s.MarkPending(ctx, "agent", []string{"a", "b"}, t0) }, nil, []string{"a", "b"}},
		{"a accepted", func() error { return s.MarkDelivered(ctx, "agent", []string{"a"}, t0) }, []string{"a"}, []string{"b"}},
		{"resending a keeps it delivered", func() error { return s.MarkPending(ctx, "agent", []string{"a"}, t0) }, []string{"a"}, []string{"b"}},
		{"b accepted", func() error { return s.MarkDelivered(ctx, "agent", []string{"b"}, t0) }, []string{"a", "b"}, nil},
	}
	for _, st := range steps {
		if err := st.do(); err != nil {
			t.Fatalf("%s: %v", st.name, err)
		}
		d, p := pendingKeys(t, s, "agent")
		if !slices.Equal(d, st.wantDelivered) || !slices.Equal(p, st.wantPending) {
			t.Errorf("%s: delivered=%v pending=%v, want %v %v", st.name, d, p, st.wantDelivered, st.wantPending)
		}
	}
	if d, p := pendingKeys(t, s, "db"); d != nil || p != nil {
		t.Errorf("cursors must be independent: db has delivered=%v pending=%v", d, p)
	}
}

func TestPendingJobKeepsStoredRecord(t *testing.T) {
	ctx := context.Background()
	s := openMem(t)
	t0 := time.Unix(1_800_000_000, 0)
	job := model.Job{Key: "a", Title: "Backend Intern", URL: "https://example.com/a", Score: 77}
	if err := s.MarkSeen(ctx, []model.Job{job}, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkPending(ctx, "agent", []string{"a"}, t0); err != nil {
		t.Fatal(err)
	}
	_, p, err := s.FeedState(ctx, "agent")
	if err != nil {
		t.Fatal(err)
	}
	if len(p) != 1 || p[0].Title != job.Title || p[0].URL != job.URL || p[0].Score != 77 || !p[0].FirstSeenAt.Equal(t0) {
		t.Errorf("pending = %+v, want the stored record of %q", p, job.Key)
	}
}

func TestDeliveryRetention(t *testing.T) {
	ctx := context.Background()
	s := openMem(t)
	t0 := time.Unix(1_800_000_000, 0)
	if err := s.MarkDelivered(ctx, "agent", []string{"old"}, t0); err != nil {
		t.Fatal(err)
	}
	later := t0.Add(deliveryRetention + time.Hour)
	if err := s.MarkDelivered(ctx, "agent", []string{"new"}, later); err != nil {
		t.Fatal(err)
	}
	if d, _ := pendingKeys(t, s, "agent"); !slices.Equal(d, []string{"new"}) {
		t.Errorf("delivered = %v, want [new] after retention", d)
	}
}

func TestValidCursorName(t *testing.T) {
	tests := []struct {
		name string
		ok   bool
	}{
		{"agent", true},
		{"my-agent.v2_prod", true},
		{"0", true},
		{"", false},
		{"Agent", false},
		{"-agent", false},
		{"a b", false},
		{"a/b", false},
		{"x'; DROP TABLE seen; --", false},
		{string(make([]byte, 65)), false},
	}
	for _, tt := range tests {
		if err := ValidCursorName(tt.name); (err == nil) != tt.ok {
			t.Errorf("ValidCursorName(%q) err=%v, want ok=%v", tt.name, err, tt.ok)
		}
	}
	s := openMem(t)
	if err := s.MarkPending(context.Background(), "Bad Name", []string{"a"}, time.Now()); err == nil {
		t.Error("MarkPending accepted an invalid name")
	}
}
