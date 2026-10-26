package dedup

import (
	"slices"
	"testing"
	"time"

	"github.com/itxkarthik/jobhunt/internal/model"
)

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"Razorpay Software Pvt Ltd": "razorpay",
		"Acme Technologies, Inc.":   "acme",
		"Stripe":                    "stripe",
		"PhonePe (India)":           "phonepe",
	} {
		if got := NormCompany(in); got != want {
			t.Errorf("NormCompany(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"Sr. Backend Engineer (Remote)":   "senior back end engineer",
		"SWE Intern [2027]":               "software engineer intern",
		"Software Engineering Internship": "software engineering intern",
	} {
		if got := NormTitle(in); got != want {
			t.Errorf("NormTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMerge(t *testing.T) {
	salary := 400000.0
	now := time.Now()
	jobs := []model.Job{
		{ID: "greenhouse:1", Source: "greenhouse", Company: "Razorpay", Title: "Backend Engineer Intern", City: "Bengaluru", Countries: []string{"IN"}, PostedAt: now.Add(-24 * time.Hour), Description: "short"},
		{ID: "himalayas:1", Source: "himalayas", Company: "Razorpay Software Pvt Ltd", Title: "Backend Engineer Intern (Remote)", Countries: []string{"IN"}, PostedAt: now, Description: "a much longer description with lots of detail", SalaryMax: &salary, Currency: "INR"},
		{ID: "greenhouse:2", Source: "greenhouse", Company: "Razorpay", Title: "Frontend Engineer Intern", City: "Bengaluru", Countries: []string{"IN"}},
		{ID: "lever:9", Source: "lever", Company: "Razorpay", Title: "Backend Engineer Intern", City: "Pune", Countries: []string{"IN"}}, // different city
		{ID: "hn:5", Source: "hn", Company: "Other Co", Title: "Backend Engineer Intern", City: "Bengaluru", Countries: []string{"IN"}},
	}
	got := Merge(jobs)
	if len(got) != 4 {
		t.Fatalf("got %d jobs, want 4: %+v", len(got), got)
	}
	m := got[0]
	if !slices.Equal(m.Sources, []string{"greenhouse", "himalayas"}) {
		t.Errorf("sources = %v", m.Sources)
	}
	if m.ID != "himalayas:1" || m.SalaryMax == nil {
		t.Errorf("should keep the richer record, got %s", m.ID)
	}
	if !m.PostedAt.Equal(now.Add(-24 * time.Hour)) {
		t.Errorf("should keep the earliest posting date, got %v", m.PostedAt)
	}
	if m.Key == "" || m.Key != Key(&jobs[0]) {
		t.Errorf("key = %q, want first-seen key %q", m.Key, Key(&jobs[0]))
	}
}

func TestKeyStable(t *testing.T) {
	a := model.Job{Company: "Stripe, Inc.", Title: "Software Engineer, Intern", Countries: []string{"IN"}, City: "Bengaluru"}
	b := model.Job{Company: "stripe", Title: "Software Engineer Intern", Countries: []string{"IN"}, City: "bengaluru"}
	if Key(&a) != Key(&b) {
		t.Error("equivalent postings should share a key")
	}
}
