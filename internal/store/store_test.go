package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/itxkarthik/jobhunt/internal/model"
)

// The database holds the tracker and notes, so Open keeps it private: a new
// file is created 0600 and an existing 0644 one is tightened.
func TestOpenKeepsDatabasePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file modes")
	}
	dir := t.TempDir()
	existing := filepath.Join(dir, "old.db")
	if err := os.WriteFile(existing, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(dir, "new", "jobhunt.db"), existing} {
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		_ = s.Close()
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if m := st.Mode().Perm(); m != 0o600 {
			t.Errorf("%s: mode %o, want 600", filepath.Base(path), m)
		}
	}
}

// Prune hands the space it frees back to the file system, also in a
// database made before incremental vacuum was on.
func TestPruneReclaimsSpace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec("CREATE TABLE t (x)"); err != nil { // auto_vacuum stays 0
		t.Fatal(err)
	}
	_ = old.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	// Random text stays big after gzip, so the row takes many pages.
	var desc strings.Builder
	for desc.Len() < 1<<20 {
		desc.WriteString(rand.Text())
	}
	if err := s.PutCache(ctx, "src", "k", []model.Job{{Description: desc.String()}}, "v", nil); err != nil {
		t.Fatal(err)
	}
	pragma := func(p string) int {
		var n int
		if err := s.db.QueryRowContext(ctx, "PRAGMA "+p).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := pragma("page_count")
	if err := s.Prune(ctx, time.Now().Add(StaleCache+time.Hour)); err != nil {
		t.Fatal(err)
	}
	if mode, pages, free := pragma("auto_vacuum"), pragma("page_count"), pragma("freelist_count"); mode != 2 || free != 0 || pages > before-100 {
		t.Errorf("auto_vacuum=%d, pages %d -> %d, %d free; want 2 and the cache's pages returned", mode, before, pages, free)
	}
}

func TestCacheTTL(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	if _, ok, err := s.CachedJobs(ctx, "src", "k", time.Hour); err != nil || ok {
		t.Fatalf("empty cache: ok=%v err=%v", ok, err)
	}
	want := []model.Job{{ID: "src:1", Title: "Backend Intern", Countries: []string{"IN"}}}
	if err := s.PutCache(ctx, "src", "k", want, "v1", []byte(`[{}]`)); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.CachedJobs(ctx, "src", "k", time.Hour)
	if err != nil || !ok || len(got.Jobs) != 1 || got.Jobs[0].Title != "Backend Intern" || got.Jobs[0].Countries[0] != "IN" ||
		got.ClassVersion != "v1" || string(got.Class) != `[{}]` {
		t.Fatalf("cache hit: %+v %v %v", got, ok, err)
	}
	// Rows from before jobs were gzipped still read.
	if _, err := s.db.ExecContext(ctx, `INSERT INTO fetch_cache (source, query_key, fetched_at, jobs_json, class_version, class_json)
		VALUES ('src', 'plain', ?, '[{"title":"Old Intern"}]', '', NULL)`, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if old, ok, _ := s.CachedJobs(ctx, "src", "plain", time.Hour); !ok || len(old.Jobs) != 1 || old.Jobs[0].Title != "Old Intern" {
		t.Errorf("plain JSON row: %+v %v", old, ok)
	}
	if _, ok, _ := s.CachedJobs(ctx, "src", "other", time.Hour); ok {
		t.Error("different key should miss")
	}
	if _, ok, _ := s.CachedJobs(ctx, "src", "k", -time.Second); ok {
		t.Error("expired entry should miss")
	}
	// Redo replaces the jobs and classification but keeps the fetch time.
	if err := s.RedoCache(ctx, "src", "k", got, nil, "v2", nil); err != nil {
		t.Fatal(err)
	}
	if redo, _, _ := s.CachedJobs(ctx, "src", "k", time.Hour); len(redo.Jobs) != 0 || redo.ClassVersion != "v2" || !redo.FetchedAt.Equal(got.FetchedAt) {
		t.Errorf("after redo: %+v", redo)
	}
	// A redo from a snapshot that has since changed leaves the entry alone.
	if err := s.RedoCache(ctx, "src", "k", got, nil, "v9", nil); err != nil {
		t.Fatal(err)
	}
	if redo, _, _ := s.CachedJobs(ctx, "src", "k", time.Hour); redo.ClassVersion != "v2" {
		t.Errorf("stale redo overwrote the entry: %+v", redo)
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
