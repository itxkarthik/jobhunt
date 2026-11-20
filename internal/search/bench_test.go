package search

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/itxkarthik/jobhunt/internal/classify"
	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources"
	"github.com/itxkarthik/jobhunt/internal/store"
)

// benchJobs makes n postings shaped like real ones: several KB of
// description, about half asking for years of experience, spread over
// companies and places, so classify, filter, dedup and rank all do real
// work. The text is generated; no real posting is copied.
func benchJobs(n int) []model.Job {
	rng := rand.New(rand.NewPCG(7, 7))
	pick := func(xs []string) string { return xs[rng.IntN(len(xs))] }
	titles := []string{"Backend Engineer", "Software Engineer Intern", "Senior Platform Engineer", "Data Engineer",
		"Frontend Developer", "Site Reliability Engineer", "ML Engineer", "New Grad Software Engineer", "Go Developer"}
	places := []string{"Bengaluru, India", "Remote", "San Francisco, CA", "London, UK", "Pune, India", "Remote - India", "Berlin"}
	paras := []string{
		"We build infrastructure that helps teams ship reliable software. You will design APIs, own services end to end and work closely with product.",
		"Our stack is Go, PostgreSQL, Kafka and Kubernetes on AWS. We care about observability, testing and clear code reviews.",
		"You will join a small team, pair with senior engineers and take on real projects from your first week.",
		"Benefits include health insurance, a learning budget, flexible hours and a hybrid office policy.",
		"We are an equal opportunity employer and value diversity. We do not discriminate on the basis of any protected characteristic.",
		"Nice to have: experience with distributed systems, gRPC, Redis, Terraform, or contributions to open source.",
	}
	years := []string{"3+ years of professional software engineering experience", "5 - 7 years of relevant experience",
		"no prior experience required", "1 to 2 yrs experience with backend systems"}
	jobs := make([]model.Job, n)
	for i := range jobs {
		var d strings.Builder
		for range 25 + rng.IntN(25) { // 4-8 KB, like real postings
			d.WriteString(pick(paras) + "\n\n")
		}
		if rng.IntN(2) == 0 {
			d.WriteString("Requirements: " + pick(years) + ".\n")
		}
		loc := pick(places)
		countries, city := model.ParseLocation(loc)
		jobs[i] = model.Job{
			ID: fmt.Sprintf("bench:%d", i), Source: "bench", Title: pick(titles), Company: fmt.Sprintf("Company %d", rng.IntN(n/8+1)),
			URL: fmt.Sprintf("https://jobs.example.com/%d", i), Location: loc, Countries: countries, City: city,
			PostedAt: time.Now().Add(-time.Duration(rng.IntN(30*24)) * time.Hour), Description: d.String(),
		}
	}
	return jobs
}

// cloneSource hands out a fresh copy on each fetch, as a real source would.
type cloneSource struct{ jobs []model.Job }

func (s *cloneSource) Name() string                { return "bench" }
func (s *cloneSource) Supports(model.Filters) bool { return true }
func (s *cloneSource) Fetch(_ context.Context, _ model.Filters) ([]model.Job, error) {
	return slices.Clone(s.jobs), nil
}

// BenchmarkSearch runs a whole search over 3,000 postings: from the cache
// (the usual case) and fetched afresh (every job cleaned and classified).
func BenchmarkSearch(b *testing.B) {
	st, err := store.Open(":memory:")
	if err != nil {
		b.Fatal(err)
	}
	defer st.Close()
	src := &cloneSource{jobs: benchJobs(3000)}
	f := model.Filters{Countries: []string{"IN"}, Types: []model.JobType{model.TypeInternship, model.TypeNewGrad, model.TypeFullTime},
		IncludeUnknown: true}
	for _, refresh := range []bool{false, true} {
		name := map[bool]string{false: "cached", true: "fresh"}[refresh]
		b.Run(name, func(b *testing.B) {
			e := &Engine{Sources: []sources.Source{src}, Store: st, Classifier: classify.Default(), CacheTTL: time.Hour, Refresh: refresh}
			if _, err := e.Run(b.Context(), f); err != nil { // fill the cache
				b.Fatal(err)
			}
			for b.Loop() {
				if _, err := e.Run(b.Context(), f); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
