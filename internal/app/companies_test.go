package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/itxkarthik/jobhunt/internal/company"
	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/store"
)

func TestCompanies(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	src := "https://example.com/news"
	for _, c := range []model.Company{
		{Name: "Acme AI", Stage: "seed", Headcount: "12", Country: "IN", Rounds: []model.Round{{Stage: "seed", Amount: 2e6, Date: time.Now().AddDate(0, -4, 0), SourceURL: src}}},
		{Name: "BigCo", Stage: "growth", Headcount: "5000"},
		{Name: "Quiet Labs", Stage: "series a", Headcount: "40"},
	} {
		if _, created, err := UpsertCompany(ctx, st, c, company.Removal{}); err != nil || !created {
			t.Fatalf("upsert %s: created=%v err=%v", c.Name, created, err)
		}
	}
	if _, _, err := UpsertCompany(ctx, st, model.Company{Name: "Bad", Website: "javascript:x"}, company.Removal{}); err == nil {
		t.Error("unsafe website accepted")
	}

	now := time.Now()
	jobs := []model.Job{
		{Key: "j1", Company: "Acme AI Pvt Ltd", Title: "Backend Engineer", Experience: model.ExpMid},
		{Key: "j2", Company: "Acme AI", Title: "Intern", Experience: model.ExpIntern},
		{Key: "j3", Company: "BigCo", Title: "Staff Engineer", Experience: model.ExpSenior},
	}
	if err := st.MarkSeen(ctx, jobs, now); err != nil {
		t.Fatal(err)
	}
	// Seen long ago: not an open job any more.
	if err := st.MarkSeen(ctx, []model.Job{{Key: "old", Company: "Quiet Labs", Experience: model.ExpMid}}, now.Add(-OpenJobWindow-time.Hour)); err != nil {
		t.Fatal(err)
	}

	names := func(vs []CompanyView) string {
		var out []string
		for _, v := range vs {
			out = append(out, v.Name)
		}
		return strings.Join(out, ",")
	}
	tests := []struct {
		name string
		q    CompanyQuery
		want string
	}{
		{"all, funded first", CompanyQuery{}, "Acme AI,BigCo,Quiet Labs"},
		{"hiring any", CompanyQuery{Hiring: []string{"any"}}, "Acme AI,BigCo"},
		{"hiring mid", CompanyQuery{Hiring: []string{"mid"}}, "Acme AI"},
		{"hiring senior", CompanyQuery{Hiring: []string{"senior"}}, "BigCo"},
		{"small and hiring", CompanyQuery{Headcounts: []string{"1-10", "11-50"}, Hiring: []string{"any"}}, "Acme AI"},
		{"funding window", CompanyQuery{FundedWithin: "270d", FundedBefore: "90d"}, "Acme AI"},
		{"limit", CompanyQuery{Limit: 2}, "Acme AI,BigCo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Companies(ctx, st, tt.q)
			if err != nil {
				t.Fatal(err)
			}
			if names(got) != tt.want {
				t.Errorf("got %s, want %s", names(got), tt.want)
			}
		})
	}

	got, _ := Companies(ctx, st, CompanyQuery{Jobs: true, Hiring: []string{"mid", "intern"}})
	if len(got) != 1 || got[0].OpenJobs != 2 || len(got[0].Jobs) != 2 {
		t.Errorf("acme jobs = %+v", got)
	}
	got, _ = Companies(ctx, st, CompanyQuery{})
	if got[0].OpenJobs != 2 || got[0].Jobs != nil {
		t.Errorf("without Jobs: open=%d jobs=%v", got[0].OpenJobs, got[0].Jobs)
	}
	for _, bad := range []CompanyQuery{{Hiring: []string{"wizard"}}, {Limit: -1}, {Stages: []string{"unicorn"}}} {
		if _, err := Companies(ctx, st, bad); err == nil {
			t.Errorf("query %+v accepted", bad)
		}
	}

	if c, err := FindCompany(ctx, st, "acme"); err != nil || c.Name != "Acme AI" {
		t.Errorf("FindCompany(acme) = %v, %v", c, err)
	}
	if c, err := FindCompany(ctx, st, "BIGCO, Inc."); err != nil || c.Name != "BigCo" {
		t.Errorf("FindCompany(BIGCO, Inc.) = %v, %v", c, err)
	}
	if _, err := FindCompany(ctx, st, "nobody"); !errors.Is(err, store.ErrNoCompany) {
		t.Errorf("FindCompany(nobody) err = %v", err)
	}
}
