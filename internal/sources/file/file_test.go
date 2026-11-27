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
	s := New([]string{"testdata/jobs.ndjson"})
	s.Reserved = []string{"greenhouse"}
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
		len(is.Areas) != 1 {
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
	before := s.CacheKey(model.Filters{})
	if _, err := s.Fetch(context.Background(), model.Filters{}); err == nil {
		t.Fatal("missing file should fail")
	} else if !strings.HasPrefix(err.Error(), "open jobs.ndjson: ") {
		t.Errorf("error should name only the file: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"title":"A","url":"https://a.example/jobs/1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if s.CacheKey(model.Filters{}) == before {
		t.Error("cache key should change when the file does")
	}
	if jobs, err := s.Fetch(context.Background(), model.Filters{}); err != nil || len(jobs) != 1 {
		t.Errorf("got %v, %v", jobs, err)
	}
	s.Now = func() time.Time { return time.Now().Add(MaxAge + 24*time.Hour) }
	if s.CacheKey(model.Filters{}) == before {
		t.Error("cache key should change when the file goes stale")
	}
	jobs, err := s.Fetch(context.Background(), model.Filters{})
	if se, ok := errors.AsType[*sources.StaleError](err); !ok || len(jobs) != 1 || !strings.Contains(se.Error(), "not updated for 4d") {
		t.Errorf("stale file: got %d jobs, %v", len(jobs), err)
	}
	if New(nil).Ready() == nil {
		t.Error("no paths should not be ready")
	}
}
