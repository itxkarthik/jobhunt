package rank

import (
	"math"
	"slices"
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

func part(f string, p, m float64) model.ScorePart {
	return model.ScorePart{Factor: f, Points: p, Max: m}
}

func TestExplain(t *testing.T) {
	now := time.Now()
	salary := 50000.0
	tests := []struct {
		name string
		w    Weights
		job  model.Job
		f    model.Filters
		want []model.ScorePart
	}{
		{"default weights, nothing asked",
			Weights{}, model.Job{JobType: model.TypeInternship, PostedAt: now},
			model.Filters{},
			[]model.ScorePart{part(FactorMatch, 17.5, 35), part(FactorRecency, 25, 25), part(FactorLevel, 25, 25), part(FactorFit, 5, 10), part(FactorInfo, 0, 5)}},
		{"custom weights, country and salary",
			Weights{Match: 10, Recency: 20, Level: 30, Fit: 30, Info: 10},
			model.Job{Experience: model.ExpSenior, Countries: []string{"IN"}, SalaryMin: &salary},
			model.Filters{Countries: []string{"IN"}},
			[]model.ScorePart{part(FactorMatch, 5, 10), part(FactorRecency, 6, 20), part(FactorLevel, 0, 30), part(FactorFit, 30, 30), part(FactorInfo, 6, 10)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Ranker{W: tt.w, Now: now}
			score, parts := r.Explain(&tt.job, tt.f, nil)
			if !slices.Equal(parts, tt.want) {
				t.Errorf("parts = %v, want %v", parts, tt.want)
			}
			sum := 0.0
			for _, p := range parts {
				sum += p.Points
			}
			if math.Abs(sum-score) > 0.3 {
				t.Errorf("parts sum to %.1f, score is %.1f", sum, score)
			}
			if s := r.Score(&tt.job, tt.f, nil); s != score {
				t.Errorf("Score = %.1f, Explain = %.1f", s, score)
			}
		})
	}
}
