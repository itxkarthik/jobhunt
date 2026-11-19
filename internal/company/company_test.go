package company

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/itxkarthik/jobhunt/internal/model"
)

const src = "https://news.ycombinator.com/item?id=1"

// days parses "90d", standing in for app.ParseAge (app imports this package).
func days(s string) (time.Duration, error) {
	n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
	return time.Duration(n) * 24 * time.Hour, err
}

func TestClean(t *testing.T) {
	tests := []struct {
		name    string
		in      model.Company
		wantErr string
		check   func(*testing.T, model.Company)
	}{
		{"minimal", model.Company{Name: "  Acme, Inc. "}, "", func(t *testing.T, c model.Company) {
			if c.Key != "acme" || c.Name != "Acme, Inc." || c.Stage != model.StageUnknown || c.Headcount != model.HeadcountUnknown {
				t.Errorf("got %+v", c)
			}
		}},
		{"control characters stripped", model.Company{Name: "Ev\x1b[31mil\u202e Co", Notes: "line1\nline2\x07"}, "", func(t *testing.T, c model.Company) {
			if c.Name != "Ev[31mil Co" || c.Notes != "line1\nline2" {
				t.Errorf("name %q notes %q", c.Name, c.Notes)
			}
		}},
		{"enums and country", model.Company{Name: "A", Stage: "Series A", Headcount: "35", Country: "india"}, "", func(t *testing.T, c model.Company) {
			if c.Stage != model.StageSeriesA || c.Headcount != model.Headcount11to50 || c.Country != "IN" {
				t.Errorf("got %+v", c)
			}
		}},
		{"email domain lowercased", model.Company{Name: "A", Contacts: []model.Contact{{Name: "Jo", Email: "Jo@Acme.COM", SourceURL: src}}}, "", func(t *testing.T, c model.Company) {
			if c.Contacts[0].Email != "Jo@acme.com" {
				t.Errorf("email %q", c.Contacts[0].Email)
			}
		}},
		{"tags deduped", model.Company{Name: "A", Tags: []string{"AI", "ai ", "", "fintech"}}, "", func(t *testing.T, c model.Company) {
			if strings.Join(c.Tags, ",") != "ai,fintech" {
				t.Errorf("tags %v", c.Tags)
			}
		}},
		{"no name", model.Company{Name: " \x1b "}, "name is required", nil},
		{"punctuation only", model.Company{Name: "!!!"}, "no letters", nil},
		{"javascript website", model.Company{Name: "A", Website: "javascript:alert(1)"}, "http(s)", nil},
		{"remote-global is not a country", model.Company{Name: "A", Country: "remote-global"}, "unknown country", nil},
		{"bad stage", model.Company{Name: "A", Stage: "unicorn"}, "unknown stage", nil},
		{"headcount source must be a URL", model.Company{Name: "A", Headcount: "5", HeadcountSource: "linkedin"}, "estimate", nil},
		{"contact without source", model.Company{Name: "A", Contacts: []model.Contact{{Name: "Jo", Email: "jo@a.com"}}}, "source_url is required", nil},
		{"contact with javascript source", model.Company{Name: "A", Contacts: []model.Contact{{Email: "jo@a.com", SourceURL: "javascript:x"}}}, "http(s)", nil},
		{"display-name email", model.Company{Name: "A", Contacts: []model.Contact{{Email: "Jo <jo@a.com>", SourceURL: src}}}, "plain address", nil},
		{"empty contact", model.Company{Name: "A", Contacts: []model.Contact{{SourceURL: src}}}, "needs a name", nil},
		{"round without source", model.Company{Name: "A", Rounds: []model.Round{{Stage: "seed", Amount: 1e6}}}, "source_url is required", nil},
		{"round bad currency", model.Company{Name: "A", Rounds: []model.Round{{Amount: 1, Currency: "dollars", SourceURL: src}}}, "currency", nil},
		{"bad board", model.Company{Name: "A", Boards: []model.Board{{ATS: "workday", Slug: "x"}}}, "unknown ats", nil},
		{"bad slug", model.Company{Name: "A", Boards: []model.Board{{ATS: "lever", Slug: "../x"}}}, "bad lever slug", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.in
			err := Clean(&c)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, c)
		})
	}
}

func TestParseBoard(t *testing.T) {
	if b, err := ParseBoard(" Greenhouse:acme "); err != nil || b.String() != "greenhouse:acme" {
		t.Errorf("got %v, %v", b, err)
	}
	for _, bad := range []string{"acme", "greenhouse:", "nope:acme"} {
		if _, err := ParseBoard(bad); err == nil {
			t.Errorf("ParseBoard(%q) accepted", bad)
		}
	}
}

func TestMerge(t *testing.T) {
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(24 * time.Hour)
	old := Merge(nil, model.Company{
		Key: "acme", Name: "Acme", Website: "https://acme.example", Stage: model.StageSeed, Headcount: model.Headcount1to10,
		Rounds:   []model.Round{{Stage: model.StageSeed, Amount: 2e6, Currency: "USD", Date: t0.AddDate(-1, 0, 0), SourceURL: "https://a/1"}},
		Contacts: []model.Contact{{Name: "Jo", Role: "CEO", Email: "jo@acme.example", SourceURL: src}},
		Tags:     []string{"ai"}, Sources: []string{"manual"}, Notes: "first",
	}, Removal{}, t0)
	if !old.CreatedAt.Equal(t0) || old.Contacts[0].AddedAt != t0 {
		t.Fatalf("new record times: %+v", old)
	}

	got := Merge(&old, model.Company{
		Key: "acme", Name: "ACME", Stage: model.StageUnknown, Headcount: model.HeadcountUnknown,
		Rounds:   []model.Round{{Stage: model.StageSeriesA, Amount: 10e6, Currency: "USD", Date: t0, SourceURL: "https://a/2"}},
		Contacts: []model.Contact{{Name: "Jo", Role: "Founder & CEO", Email: "JO@acme.example", SourceURL: "https://a/3"}, {Name: "Sam", Role: "CTO", SourceURL: src}},
		Tags:     []string{"ai", "fintech"}, Sources: []string{"agent"},
	}, Removal{}, t1)

	switch {
	case got.Name != "ACME" || got.Website != "https://acme.example" || got.Headcount != model.Headcount1to10:
		t.Errorf("scalars: %+v", got)
	case got.Stage != model.StageSeriesA:
		t.Errorf("stage = %s, want series-a from the newer round", got.Stage)
	case len(got.Rounds) != 2 || got.Rounds[0].Stage != model.StageSeriesA:
		t.Errorf("rounds = %+v", got.Rounds)
	case len(got.Contacts) != 2 || got.Contacts[0].Role != "Founder & CEO" || !got.Contacts[0].AddedAt.Equal(t0):
		t.Errorf("contacts = %+v", got.Contacts)
	case strings.Join(got.Tags, ",") != "ai,fintech" || strings.Join(got.Sources, ",") != "manual,agent":
		t.Errorf("tags %v sources %v", got.Tags, got.Sources)
	case got.Notes != "first" || !got.CreatedAt.Equal(t0) || !got.UpdatedAt.Equal(t1):
		t.Errorf("notes/times: %+v", got)
	}
	if len(old.Contacts) != 1 || old.Contacts[0].Role != "CEO" {
		t.Errorf("merge changed the old record: %+v", old.Contacts)
	}

	rm := Merge(&got, model.Company{Key: "acme"}, Removal{Contacts: []string{"jo@ACME.example"}, Tags: []string{"AI"}}, t1)
	if len(rm.Contacts) != 1 || rm.Contacts[0].Name != "Sam" || strings.Join(rm.Tags, ",") != "fintech" {
		t.Errorf("removal: contacts %+v tags %v", rm.Contacts, rm.Tags)
	}
}

func TestFilter(t *testing.T) {
	now := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	cs := []model.Company{
		{Name: "Acme", Country: "US", Stage: model.StageSeed, Headcount: model.Headcount11to50, Tags: []string{"ai"},
			Rounds: []model.Round{{Date: now.AddDate(0, 0, -30), SourceURL: src}}, Contacts: []model.Contact{{Name: "Jo", SourceURL: src}}},
		{Name: "Beta Labs", Country: "IN", Stage: model.StageSeriesB, Headcount: model.Headcount201to500,
			Rounds: []model.Round{{Date: now.AddDate(-1, 0, 0), SourceURL: src}}},
		{Name: "Gamma", Stage: model.StageUnknown, Headcount: model.HeadcountUnknown, Remote: true},
		{Name: "Delta", Country: "IN", City: "Pune", Stage: model.StageUnknown, Headcount: model.HeadcountUnknown},
	}
	tests := []struct {
		name string
		in   FilterInput
		want string
	}{
		{"everything", FilterInput{}, "Acme,Beta Labs,Gamma,Delta"},
		{"stage", FilterInput{Stages: []string{"seed", "series a"}}, "Acme"},
		{"headcount", FilterInput{Headcounts: []string{"1-10", "11-50"}}, "Acme"},
		{"country", FilterInput{Countries: []string{"india"}}, "Beta Labs,Delta"},
		{"city", FilterInput{Countries: []string{"IN"}, Cities: []string{"pune"}}, "Delta"},
		{"country or remote", FilterInput{Countries: []string{"IN"}, Cities: []string{"Bengaluru"}, RemoteOK: true}, "Gamma"},
		{"funded within", FilterInput{FundedWithin: "90d"}, "Acme"},
		{"funded 3-9 months ago", FilterInput{FundedWithin: "270d", FundedBefore: "60d"}, ""},
		{"funded over 6 months ago", FilterInput{FundedBefore: "180d"}, "Beta Labs"},
		{"has contact", FilterInput{HasContact: true}, "Acme"},
		{"tag", FilterInput{Tags: []string{"AI"}}, "Acme"},
		{"query", FilterInput{Query: "LABS"}, "Beta Labs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := ParseFilter(tt.in, days)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for i := range cs {
				if f.Match(&cs[i], now) {
					got = append(got, cs[i].Name)
				}
			}
			if strings.Join(got, ",") != tt.want {
				t.Errorf("got %v, want %s", got, tt.want)
			}
		})
	}
	if _, err := ParseFilter(FilterInput{Stages: []string{"unicorn"}}, days); err == nil {
		t.Error("bad stage accepted")
	}
	if _, err := ParseFilter(FilterInput{FundedWithin: "90d", FundedBefore: "90d"}, days); err == nil {
		t.Error("empty funding window accepted")
	}

	Sort(cs)
	if cs[0].Name != "Acme" || cs[1].Name != "Beta Labs" {
		t.Errorf("sort: %s %s", cs[0].Name, cs[1].Name)
	}
}

func TestFillOnly(t *testing.T) {
	old := &model.Company{Website: "https://acme.io", Country: "IN", Stage: model.StageSeed, Headcount: model.HeadcountUnknown}
	in := model.Company{Website: "https://acme.com", Country: "US", City: "Austin", Stage: model.StageSeriesA, Headcount: model.Headcount11to50, Tags: []string{"hn-hiring"}}
	got := FillOnly(old, in)
	if got.Website != "" || got.Country != "" || got.Stage != "" || got.City != "Austin" || got.Headcount != model.Headcount11to50 || len(got.Tags) != 1 {
		t.Errorf("FillOnly = %+v", got)
	}
	if got := FillOnly(nil, in); got.Website != in.Website {
		t.Errorf("new company: %+v", got)
	}
}

func TestDraft(t *testing.T) {
	now := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	src := "https://acme.ai/careers"
	c := &model.Company{Name: "Acme AI", Stage: model.StageSeed,
		Rounds: []model.Round{{Stage: model.StageSeriesA, Date: now.AddDate(0, -4, 0), SourceURL: src}},
		Contacts: []model.Contact{
			{Email: "jobs@acme.ai", Role: "hiring contact", SourceURL: src},
			{Name: "Asha Rao", Role: "CEO & Co-founder", Email: "asha@acme.ai", SourceURL: src},
			{Name: "Ravi", Role: "CTO", URL: "https://x.com/ravi", SourceURL: src}, // no email: never the recipient
		}}
	job := &model.Job{Title: "Backend Intern", URL: "https://jobs.ashbyhq.com/acme/1"}
	d := NewDraft(c, job, DraftOptions{Stack: []string{"go", "rust", "python"}, Now: now})
	if d.To != "asha@acme.ai" || d.SourceURL != src || d.Subject != "Backend Intern at Acme AI" {
		t.Errorf("draft = %+v", d)
	}
	for _, want := range []string{"Hi Asha,", "the Backend Intern role", "https://jobs.ashbyhq.com/acme/1", "recent series a round", "Go and Rust", "[Your name]"} {
		if !strings.Contains(d.Body, want) {
			t.Errorf("body lacks %q:\n%s", want, d.Body)
		}
	}
	if !strings.HasPrefix(d.Mailto, "mailto:asha@acme.ai?subject=Backend%20Intern%20at%20Acme%20AI&body=Hi%20Asha%2C") || strings.Contains(d.Mailto, "+") {
		t.Errorf("mailto = %s", d.Mailto)
	}

	// No published email: a draft to copy, without a recipient or mailto.
	d = NewDraft(&model.Company{Name: "Kestrel"}, nil, DraftOptions{Now: now})
	if d.To != "" || d.Mailto != "" || !strings.HasPrefix(d.Body, "Hi Kestrel team,") || strings.Contains(d.Body, "round") {
		t.Errorf("no-contact draft = %+v", d)
	}
}

func TestMergeNoChangeKeepsUpdatedAt(t *testing.T) {
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	in := model.Company{Key: "acme", Name: "Acme", Country: "IN", Tags: []string{"hn-hiring"}, Sources: []string{"hn"},
		Contacts: []model.Contact{{Email: "jobs@acme.ai", SourceURL: src}}}
	first := Merge(nil, in, Removal{}, t0)
	again := Merge(&first, in, Removal{}, t0.Add(time.Hour))
	if !again.UpdatedAt.Equal(t0) {
		t.Errorf("no-op merge moved UpdatedAt to %v", again.UpdatedAt)
	}
	changed := Merge(&first, model.Company{Key: "acme", City: "Pune"}, Removal{}, t0.Add(time.Hour))
	if !changed.UpdatedAt.Equal(t0.Add(time.Hour)) {
		t.Errorf("real change kept UpdatedAt %v", changed.UpdatedAt)
	}
}
