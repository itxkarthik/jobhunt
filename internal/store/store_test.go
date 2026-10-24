package store

import (
	"context"
	"testing"
	"time"

	"github.com/itxkarthik/jobhunt/internal/model"
)

func TestCacheTTL(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	if _, _, ok, err := s.CachedJobs(ctx, "src", "k", time.Hour); err != nil || ok {
		t.Fatalf("empty cache: ok=%v err=%v", ok, err)
	}
	want := []model.Job{{ID: "src:1", Title: "Backend Intern", Countries: []string{"IN"}}}
	if err := s.PutCache(ctx, "src", "k", want); err != nil {
		t.Fatal(err)
	}
	got, _, ok, err := s.CachedJobs(ctx, "src", "k", time.Hour)
	if err != nil || !ok || len(got) != 1 || got[0].Title != "Backend Intern" || got[0].Countries[0] != "IN" {
		t.Fatalf("cache hit: %v %v %v", got, ok, err)
	}
	if _, _, ok, _ := s.CachedJobs(ctx, "src", "other", time.Hour); ok {
		t.Error("different key should miss")
	}
	if _, _, ok, _ := s.CachedJobs(ctx, "src", "k", -time.Second); ok {
		t.Error("expired entry should miss")
	}
	// Overwrite replaces.
	if err := s.PutCache(ctx, "src", "k", nil); err != nil {
		t.Fatal(err)
	}
	if got, _, _, _ := s.CachedJobs(ctx, "src", "k", time.Hour); len(got) != 0 {
		t.Errorf("after overwrite got %d jobs", len(got))
	}
}

func TestHealth(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	base := time.Now().Add(-time.Hour)
	runs := []SourceRun{
		{Source: "a", RunAt: base, Status: "ok", Count: 5},
		{Source: "a", RunAt: base.Add(time.Minute), Status: "error", Error: "boom"},
		{Source: "b", RunAt: base, Status: "skipped", Error: "no key"},
	}
	for _, r := range runs {
		if err := s.RecordRun(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	h, err := s.Health(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if h["a"].Last.Status != "error" || h["a"].Last.Error != "boom" || h["a"].RecentErrors != 1 {
		t.Errorf("a = %+v", h["a"])
	}
	if h["a"].LastSuccess.Unix() != base.Unix() {
		t.Errorf("a last success = %v, want %v", h["a"].LastSuccess, base)
	}
	if h["b"].Last.Status != "skipped" || !h["b"].LastSuccess.IsZero() {
		t.Errorf("b = %+v", h["b"])
	}
}
