// Package rank scores jobs 0–100 by how well they fit the search: keyword
// and area match, recency, entry-level fit, location fit, and how much
// detail (salary, visa) the posting gives.
package rank

import (
	"math"
	"slices"
	"strings"
	"time"

	"github.com/itxkarthik/jobhunt/internal/classify"
	"github.com/itxkarthik/jobhunt/internal/model"
)

// Weights are the maximum points per component; they should sum to 100.
type Weights struct {
	Match   float64 `yaml:"match"`
	Recency float64 `yaml:"recency"`
	Level   float64 `yaml:"level"`
	Fit     float64 `yaml:"fit"`
	Info    float64 `yaml:"info"`
}

var DefaultWeights = Weights{Match: 35, Recency: 25, Level: 25, Fit: 10, Info: 5}

// HalfLife is the recency decay: a week-old posting gets half the points.
const HalfLife = 7 * 24 * time.Hour

type Ranker struct {
	W          Weights
	Classifier *classify.Classifier
	Now        time.Time

	prefsFor []string // the Stack and Projects prefs was built for
	prefs    *prefs
}

// prefs are the term sets for a filter's Stack and Projects.
type prefs struct {
	stack    []*classify.TermSet // in priority order
	projects *classify.TermSet
}

// languageTerms are the ways a stack language is written in postings.
var languageTerms = map[string][]string{
	"go":         {"go", "golang"},
	"node":       {"node.js", "nodejs", "node"},
	"javascript": {"javascript", "js"},
	"typescript": {"typescript", "ts"},
	"c++":        {"c++", "cpp"},
	"c#":         {"c#", ".net", "dotnet"},
}

func (r *Ranker) prefsOf(f model.Filters) *prefs {
	key := append(append([]string{}, f.Stack...), "\x00")
	key = append(key, f.Projects...)
	if r.prefs != nil && slices.Equal(key, r.prefsFor) {
		return r.prefs
	}
	p := &prefs{projects: classify.NewTermSet(f.Projects)}
	for _, l := range f.Stack {
		terms, ok := languageTerms[strings.ToLower(strings.TrimSpace(l))]
		if !ok {
			terms = []string{l}
		}
		p.stack = append(p.stack, classify.NewTermSet(terms))
	}
	r.prefs, r.prefsFor = p, key
	return p
}

// stackWeight is what a job using the i-th language of an n-language
// stack adds: 1 for the first, falling to 0.4 for the last.
func stackWeight(i, n int) float64 {
	if n <= 1 {
		return 1
	}
	return 1 - 0.6*float64(i)/float64(n-1)
}

// Factor names, as reported in model.ScorePart.
const (
	FactorMatch   = "match"
	FactorRecency = "recency"
	FactorLevel   = "level"
	FactorFit     = "fit"
	FactorInfo    = "info"
)

// Explain returns j's score and each factor's share of it, in a fixed
// order. Points are rounded to one decimal, so they may not add up to the
// score exactly.
func (r *Ranker) Explain(j *model.Job, f model.Filters, kw *classify.TermSet) (float64, []model.ScorePart) {
	w := r.W
	if w == (Weights{}) {
		w = DefaultWeights
	}
	parts := []model.ScorePart{
		{Factor: FactorMatch, Points: w.Match * r.match(j, f, kw), Max: w.Match},
		{Factor: FactorRecency, Points: w.Recency * r.recency(j), Max: w.Recency},
		{Factor: FactorLevel, Points: w.Level * level(j), Max: w.Level},
		{Factor: FactorFit, Points: w.Fit * fit(j, f), Max: w.Fit},
		{Factor: FactorInfo, Points: w.Info * info(j), Max: w.Info},
	}
	s := 0.0
	for i := range parts {
		s += parts[i].Points
		parts[i].Points = round1(parts[i].Points)
	}
	return round1(s), parts
}

func round1(x float64) float64 { return math.Round(x*10) / 10 }

func (r *Ranker) match(j *model.Job, f model.Filters, kw *classify.TermSet) float64 {
	var parts []float64
	if kw != nil && kw.Len() > 0 {
		switch {
		case kw.Any(classify.Tokenize(j.Title)):
			parts = append(parts, 1)
		case kw.Any(classify.Tokenize(j.Description)):
			parts = append(parts, 0.6)
		default:
			parts = append(parts, 0)
		}
	}
	if len(f.Areas) > 0 {
		best := 0.0
		for _, a := range f.Areas {
			switch {
			case j.HasArea(a):
				best = 1
			case r.Classifier != nil && r.Classifier.MatchesArea(j, a):
				best = math.Max(best, 0.6) // generic "Software Engineer" title
			}
		}
		parts = append(parts, best)
	}
	bonus := 0.0
	if len(f.Stack) > 0 || len(f.Projects) > 0 {
		p := r.prefsOf(f)
		text := classify.Tokenize(j.Title + "\n" + j.Description)
		if len(p.stack) > 0 {
			best := 0.0
			for i, ts := range p.stack {
				if ts.Any(text) {
					best = stackWeight(i, len(p.stack))
					break
				}
			}
			parts = append(parts, best)
		}
		if p.projects.Len() > 0 && p.projects.Any(text) {
			bonus = 0.25 // a project you care about: a bonus, never a penalty
		}
	}
	if len(parts) == 0 {
		return min(0.5+bonus, 1) // nothing asked for: neutral
	}
	sum := 0.0
	for _, p := range parts {
		sum += p
	}
	return min(sum/float64(len(parts))+bonus, 1)
}

func (r *Ranker) recency(j *model.Job) float64 {
	if j.PostedAt.IsZero() {
		return 0.3
	}
	age := max(r.Now.Sub(j.PostedAt), 0)
	return math.Pow(0.5, float64(age)/float64(HalfLife))
}

func level(j *model.Job) float64 {
	switch {
	case j.JobType == model.TypeInternship || j.JobType == model.TypeNewGrad:
		return 1
	case j.Experience == model.ExpIntern || j.Experience == model.ExpEntry:
		return 1
	case j.Experience == model.ExpMid:
		return 0.2
	case j.Experience == model.ExpSenior:
		return 0
	}
	return 0.5
}

func fit(j *model.Job, f model.Filters) float64 {
	if len(f.Countries) == 0 {
		return 0.5
	}
	if len(j.Countries) == 0 {
		return 0.4
	}
	for _, c := range f.Countries {
		if slices.Contains(j.Countries, c) {
			return 1
		}
	}
	return 0.7 // matched through worldwide/regional remote
}

func info(j *model.Job) float64 {
	s := 0.0
	if j.SalaryMin != nil || j.SalaryMax != nil {
		s += 0.6
	}
	if j.VisaSponsorship != nil {
		s += 0.4
	}
	return s
}
