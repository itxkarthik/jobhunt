package rank

import (
	"testing"
	"time"

	"github.com/itxkarthik/jobhunt/internal/classify"
	"github.com/itxkarthik/jobhunt/internal/model"
)

func TestScoreOrdering(t *testing.T) {
	now := time.Now()
	c := classify.Default()
	r := &Ranker{Classifier: c, Now: now}
	f := model.Filters{Countries: []string{"IN"}, Areas: []model.Area{model.AreaBackend}, Keywords: []string{"go"}}
	kw := classify.NewTermSet(f.Keywords)
	mk := func(title string, age time.Duration, countries ...string) model.Job {
		j := model.Job{Title: title, PostedAt: now.Add(-age), Countries: countries}
		c.Classify(&j)
		return j
	}
	best := mk("Backend Intern (Go)", time.Hour, "IN")
	older := mk("Backend Intern (Go)", 21*24*time.Hour, "IN")
	senior := mk("Senior Backend Engineer (Go)", time.Hour, "IN")
	generic := mk("Software Engineer Intern", time.Hour, "IN")
	remote := mk("Backend Intern (Go)", time.Hour, model.RemoteGlobal)

	s := func(j model.Job) float64 { return r.Score(&j, f, kw) }
	tests := []struct {
		name   string
		hi, lo model.Job
	}{
		{"recent beats old", best, older},
		{"intern beats senior", best, senior},
		{"keyword+area beats generic title", best, generic},
		{"in-country beats worldwide remote", best, remote},
	}
	for _, tt := range tests {
		if s(tt.hi) <= s(tt.lo) {
			t.Errorf("%s: %.1f <= %.1f", tt.name, s(tt.hi), s(tt.lo))
		}
	}
	if v := s(best); v < 80 || v > 100 {
		t.Errorf("perfect match scored %.1f, want 80–100", v)
	}
}

func TestRecencyHalfLife(t *testing.T) {
	now := time.Now()
	r := &Ranker{Now: now}
	j := model.Job{PostedAt: now.Add(-HalfLife)}
	if got := r.recency(&j); got < 0.49 || got > 0.51 {
		t.Errorf("recency at half-life = %.3f", got)
	}
}
