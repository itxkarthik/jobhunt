package app

import (
	"strings"
	"testing"
	"time"

	"github.com/itxkarthik/jobhunt/internal/company"
	"github.com/itxkarthik/jobhunt/internal/config"
	"github.com/itxkarthik/jobhunt/internal/feed"
	"github.com/itxkarthik/jobhunt/internal/model"
)

func testProfile() config.Profile {
	return config.Profile{
		Types: []string{"internship", "new-grad"}, Areas: []string{"backend", "systems"}, SkipAreas: []string{"embedded"},
		Stack: []string{"go", " rust ", ""}, Projects: []string{"microvm"}, Countries: []string{"IN"},
		SkipRequiresExperience: true,
		Companies: config.ProfileCompanies{
			Stages: []string{"seed", "series-a"}, Headcounts: []string{"1-10", "11-50"}, Countries: []string{"IN"},
			Cities: []string{"Bengaluru"}, RemoteOK: true, FundedWithin: "270d", FundedBefore: "90d",
		},
	}
}

func TestProfileFilters(t *testing.T) {
	def := config.Default().Defaults
	p := testProfile()

	// Flags win; the profile fills what's empty.
	in := ProfileInput(p, FilterInput{Areas: []string{"devops"}})
	if strings.Join(in.Areas, ",") != "devops" || strings.Join(in.Types, ",") != "internship,new-grad" {
		t.Errorf("ProfileInput = %+v", in)
	}

	f, err := ProfileFilters(def, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Types) != 2 || len(f.Areas) != 2 || f.Countries[0] != "IN" || !f.SkipRequiresExperience ||
		len(f.SkipAreas) != 1 || f.SkipAreas[0] != model.AreaEmbedded || strings.Join(f.Stack, ",") != "go,rust" {
		t.Errorf("ProfileFilters = %+v", f)
	}

	// A preset with areas keeps them; its empty fields come from the profile.
	base := model.Filters{Areas: []model.Area{model.AreaFrontend}}
	FillFromProfile(&base, f)
	if base.Areas[0] != model.AreaFrontend || len(base.Types) != 2 {
		t.Errorf("FillFromProfile = %+v", base)
	}

	ci := ProfileCompanyInput(p, company.FilterInput{Stages: []string{"growth"}})
	if ci.Stages[0] != "growth" || !ci.RemoteOK || ci.FundedWithin != "270d" || ci.Cities[0] != "Bengaluru" {
		t.Errorf("ProfileCompanyInput = %+v", ci)
	}
	if err := CheckProfile(def, p); err != nil {
		t.Errorf("CheckProfile: %v", err)
	}
	for name, bad := range map[string]func(*config.Profile){
		"skip area":   func(p *config.Profile) { p.SkipAreas = []string{"cooking"} },
		"type":        func(p *config.Profile) { p.Types = []string{"gig"} },
		"stage":       func(p *config.Profile) { p.Companies.Stages = []string{"unicorn"} },
		"window":      func(p *config.Profile) { p.Companies.FundedBefore = "300d" },
		"min score":   func(p *config.Profile) { p.MinScore = 120 },
		"posted span": func(p *config.Profile) { p.PostedWithin = "forever" },
	} {
		q := testProfile()
		bad(&q)
		if err := CheckProfile(def, q); err == nil {
			t.Errorf("%s: bad profile accepted", name)
		}
	}
	if !(config.Profile{}).IsZero() || p.IsZero() {
		t.Error("IsZero")
	}
}

func TestBuildDigest(t *testing.T) {
	// 01:39 on Fri 20 Nov in India is still Thu 19 Nov in UTC; the heading
	// must show the local date.
	defer func(l *time.Location) { time.Local = l }(time.Local)
	time.Local = time.FixedZone("IST", 5*3600+1800)
	now := time.Date(2026, 11, 19, 20, 9, 0, 0, time.UTC)
	src := "https://example.com/news"
	jobs := []model.Job{
		{Key: "j1", Title: "Backend Intern", Company: "Acme", URL: "https://jobs.example.com/1", Score: 90},
		{Key: "j2", Title: "Go [Intern]", Company: "Beta*", URL: "https://jobs.example.com/2", Score: 70},
		{Key: "j3", Title: "Low", Company: "Gamma", URL: "https://jobs.example.com/3", Score: 20},
	}
	cs := []model.Company{
		{Key: "acme", Name: "Acme", Stage: model.StageSeed, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
			Rounds:   []model.Round{{Stage: model.StageSeed, Date: now.AddDate(0, -4, 0), SourceURL: src}},
			Contacts: []model.Contact{{Name: "Asha", Role: "CEO", Email: "asha@acme.ai", SourceURL: src}}},
		{Key: "beta", Name: "Beta", CreatedAt: now.AddDate(0, 0, -9), UpdatedAt: now.Add(-2 * time.Hour)},
	}
	d, keys := BuildDigest(jobs, cs, map[string]bool{}, true, DigestOptions{MinScore: 50, MaxJobs: 1, Stack: []string{"Go"}, Now: now})
	if !d.First || len(d.Jobs) != 1 || d.Jobs[0].Key != "j1" || d.MoreJobs != 1 || len(d.Companies) != 2 {
		t.Fatalf("digest = %+v", d)
	}
	if want := "j1,j2," + feed.CompanyVersion(&cs[0]) + "," + feed.CompanyVersion(&cs[1]); strings.Join(keys, ",") != want {
		t.Errorf("keys = %v, want %s (j3 is below min_score)", keys, want)
	}
	if d.Companies[0].Name != "Acme" || d.Companies[0].Draft == nil || d.Companies[0].Change != feed.Created ||
		d.Companies[1].Draft != nil || d.Companies[1].Change != feed.Updated {
		t.Errorf("companies = %+v", d.Companies)
	}
	md := d.Markdown()
	for _, want := range []string{"# jobhunt digest, Fri 20 Nov 2026", "First digest", "## 2 new jobs", "[Backend Intern](https://jobs.example.com/1) at Acme", "and 1 more", "**Acme** (created): seed · raised Jul 2026, [source](" + src + ")", "[draft email](mailto:asha@acme.ai?"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
	title, body := d.Notification()
	if title != "jobhunt: 2 new jobs, 2 companies" || !strings.HasPrefix(body, "Backend Intern at Acme") {
		t.Errorf("notification %q / %q", title, body)
	}

	// Everything delivered: empty.
	done := map[string]bool{}
	for _, k := range keys {
		done[k] = true
	}
	if d, keys := BuildDigest(jobs, cs, done, false, DigestOptions{MinScore: 50, Now: now}); !d.Empty() || len(keys) != 0 {
		t.Errorf("repeat digest = %+v, %v", d, keys)
	}
	if got := mdText("a [link](javascript:x) *b* `c`"); strings.ContainsAny(got, "[]*`") {
		t.Errorf("mdText = %q", got)
	}
}
