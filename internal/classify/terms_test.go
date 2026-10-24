package classify

import (
	"slices"
	"strings"
	"testing"

	"github.com/itxkarthik/jobhunt/internal/model"
)

func TestTokenize(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"Back-End Engineer (Go)", []string{"back", "end", "engineer", "go"}},
		{"C++ / C# / .NET / Node.js.", []string{"c++", "c#", ".net", "node.js"}},
		{"CI/CD, 100% remote!", []string{"ci", "cd", "100", "remote"}},
		{"Can't sponsor. Ends here...", []string{"can", "t", "sponsor", "ends", "here"}},
		{"München – Köln", []string{"münchen", "köln"}},
		{"", []string{}},
	}
	for _, tt := range tests {
		if got := Tokenize(tt.in); !slices.Equal(got, tt.want) {
			t.Errorf("Tokenize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestTermSet(t *testing.T) {
	ts := NewTermSet([]string{"back-end", "go", "c++", "distributed systems", "ci/cd"})
	tests := []struct {
		text string
		any  bool
		hits int
	}{
		{"Back end engineer", true, 1},
		{"Google search", false, 0}, // "go" must be a whole word
		{"Write C++ and Go for distributed systems with CI/CD", true, 4},
		{"Distributed teams, systems thinking", false, 0},
		{"go go go", true, 1}, // distinct terms
	}
	for _, tt := range tests {
		toks := Tokenize(tt.text)
		if ts.Any(toks) != tt.any || ts.Hits(toks) != tt.hits {
			t.Errorf("%q: any=%v hits=%d, want %v %d", tt.text, ts.Any(toks), ts.Hits(toks), tt.any, tt.hits)
		}
	}
}

// BenchmarkClassify guards against regressions like the regex-per-term
// matcher that took minutes on ~20k ATS jobs.
func BenchmarkClassify(b *testing.B) {
	c := Default()
	desc := strings.Repeat("We build distributed systems in Go and Kubernetes on AWS with PostgreSQL, Kafka and gRPC. ", 50)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		j := model.Job{Title: "Software Engineer Intern, Backend", Description: desc, Location: "Bengaluru, India"}
		c.Classify(&j)
	}
}
