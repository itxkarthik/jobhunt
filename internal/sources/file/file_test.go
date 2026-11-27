package file

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources"
)

func TestFetch(t *testing.T) {
	const fixture = "testdata/jobs.ndjson"
	fi, err := os.Stat(fixture)
	if err != nil {
		t.Fatal(err)
	}
	s := New([]string{fixture})
	s.Reserved = []string{"greenhouse"}
	s.Now = func() time.Time { return fi.ModTime().Add(time.Hour) } // however old the checkout is
	jobs, err := s.Fetch(context.Background(), model.Filters{})
	if pe, ok := errors.AsType[*sources.PartialError](err); !ok || len(pe.Failed) != 2 {
		t.Fatalf("want a partial error for 2 bad lines, got %v", err)
	}
	if len(jobs) != 4 {
		t.Fatalf("got %d jobs, want 4: %+v", len(jobs), jobs)
	}
	li := jobs[0]
	if li.ID != "linkedin:4123" || li.Source != "linkedin" || li.Key != "" || li.Score != 0 || li.New {
		t.Errorf("computed fields not cleared: %+v", li)
	}
	if len(li.Countries) == 0 || li.Countries[0] != "IN" || li.City == "" {
		t.Errorf("location not parsed: %v %q", li.Countries, li.City)
	}
	if li.JobType != model.TypeInternship || li.WorkMode != model.ModeUnknown || li.PayPeriod != model.PerMonth {
		t.Errorf("enums: %s %s %q", li.JobType, li.WorkMode, li.PayPeriod)
	}
	is := jobs[1]
	if is.Source != "internshala" || is.ID != "internshala:https://internshala.com/internship/detail/ds-1" ||
		is.Company != "unknown" || is.WorkMode != model.ModeRemote || is.Experience != model.ExpUnknown ||
		!slices.Equal(is.Areas, []model.Area{model.AreaDataSci, model.AreaDevOps}) {
		t.Errorf("internshala job: %+v", is)
	}
	if c := jobs[3].Countries; !slices.Equal(c, []string{"IN", "region:apac"}) {
		t.Errorf("countries = %q, want only valid codes", c)
	}
	if strings.ContainsAny(jobs[3].ID, "\x1b\x07") {
		t.Errorf("control characters left in ID %q", jobs[3].ID)
	}
	if jobs[2].Source != "file" || jobs[3].Source != "file" {
		t.Errorf("reserved or missing source names should become file: %s, %s", jobs[2].Source, jobs[3].Source)
	}
}

func TestMissingFileAndCacheKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.ndjson")
	s := New([]string{path})
	missing := s.CacheKey(model.Filters{})
	if _, err := s.Fetch(context.Background(), model.Filters{}); err == nil {
		t.Fatal("missing file should fail")
	} else if !strings.HasPrefix(err.Error(), "open jobs.ndjson: ") {
		t.Errorf("error should name only the file: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"title":"A","url":"https://a.example/jobs/1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if s.CacheKey(model.Filters{}) == missing {
		t.Error("cache key should change when the file does")
	}
	if jobs, err := s.Fetch(context.Background(), model.Filters{}); err != nil || len(jobs) != 1 {
		t.Errorf("got %v, %v", jobs, err)
	}
	fresh := s.CacheKey(model.Filters{})
	s.Now = func() time.Time { return time.Now().Add(MaxAge + 24*time.Hour) }
	if s.CacheKey(model.Filters{}) == fresh {
		t.Error("cache key should change when the file goes stale")
	}
	jobs, err := s.Fetch(context.Background(), model.Filters{})
	if se, ok := errors.AsType[*sources.StaleError](err); !ok || len(jobs) != 1 || !strings.Contains(se.Error(), "not updated for 4d") {
		t.Errorf("stale file: got %d jobs, %v", len(jobs), err)
	}
	// A stale result still says which lines were bad.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("\njunk\n")
	_ = f.Close()
	if _, err := s.Fetch(context.Background(), model.Filters{}); err == nil || !strings.Contains(err.Error(), "not a job") {
		t.Errorf("stale result hid a bad line: %v", err)
	}

	// Directories, FIFOs and devices aren't read (see file_unix_test.go),
	// and errors never show the directory they're in.
	dir := t.TempDir()
	if _, err := New([]string{dir}).Fetch(context.Background(), model.Filters{}); err == nil ||
		!strings.Contains(err.Error(), "not a regular file") || strings.Contains(err.Error(), filepath.Dir(dir)) {
		t.Errorf("directory: %v", err)
	}
	if New(nil).Ready() == nil {
		t.Error("no paths should not be ready")
	}
}
