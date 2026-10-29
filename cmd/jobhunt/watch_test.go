package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/itxkarthik/jobhunt/internal/model"
)

func TestWatchOnce(t *testing.T) {
	jobs := []model.Job{
		{Key: "a", Title: "Backend Intern", Company: "A", Score: 90, New: true},
		{Key: "b", Title: "Old Intern", Company: "B", Score: 95},            // not new
		{Key: "c", Title: "Low Intern", Company: "C", Score: 40, New: true}, // below min
		{Key: "d", Title: "SDE Intern", Company: "D", Score: 70, New: true},
	}
	tests := []struct {
		name      string
		jobs      []model.Job
		min       float64
		wantTitle string
		wantBody  []string
	}{
		{"two hits", jobs, 60, "jobhunt: 2 new jobs", []string{"90 · Backend Intern — A", "70 · SDE Intern — D"}},
		{"one hit", jobs[:1], 0, "jobhunt: 1 new job", []string{"Backend Intern"}},
		{"none", jobs[1:2], 0, "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var title, body string
			var out bytes.Buffer
			err := watchOnce(context.Background(), &out,
				func(context.Context) ([]model.Job, error) { return tt.jobs, nil }, tt.min,
				func(ti, b string) error { title, body = ti, b; return nil })
			if err != nil {
				t.Fatal(err)
			}
			if title != tt.wantTitle {
				t.Errorf("title = %q, want %q", title, tt.wantTitle)
			}
			for _, w := range tt.wantBody {
				if !strings.Contains(body, w) {
					t.Errorf("body %q missing %q", body, w)
				}
			}
			if tt.wantTitle == "" && !strings.Contains(out.String(), "no new jobs") {
				t.Errorf("out = %q", out.String())
			}
		})
	}
}
