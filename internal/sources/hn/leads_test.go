package hn

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/itxkarthik/jobhunt/internal/model"
)

func TestEmails(t *testing.T) {
	tests := []struct {
		text string
		want []string
	}{
		{"mail jobs@acme.io today", []string{"jobs@acme.io"}},
		{"mailto:Hiring@Acme.IO", []string{"hiring@acme.io"}},
		{"name [at] co [dot] com", []string{"name@co.com"}},
		{"name(at)co.com and x {at} y {dot} co {dot} uk", []string{"name@co.com", "x@y.co.uk"}},
		{"careers at plainview dot de", []string{"careers@plainview.de"}},
		{"we meet at noon at the office", nil},
		{"look at our site dot", nil},
		{"no contact here, apply on the site", nil},
		{"ping someone@news.ycombinator.com or a@example.com", nil},
		{"user@localhost", nil},
		{"jobs@acme.io and JOBS@acme.io.", []string{"jobs@acme.io"}},
	}
	for _, tt := range tests {
		if got := Emails(tt.text); !slices.Equal(got, tt.want) {
			t.Errorf("Emails(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

func TestBoardOf(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://jobs.ashbyhq.com/kestrel/123", "ashby:kestrel"},
		{"https://boards.greenhouse.io/plainview/jobs/9", "greenhouse:plainview"},
		{"https://job-boards.greenhouse.io/embed/job_board?for=Acme", "greenhouse:acme"},
		{"https://jobs.lever.co/orbit", "lever:orbit"},
		{"https://apply.workable.com/acme/j/ABC/", "workable:acme"},
		{"https://acme.recruitee.com/o/backend", "recruitee:acme"},
		{"https://acme.com/careers", ""},
		{"https://jobs.lever.co/", ""},
	}
	for _, tt := range tests {
		b, ok := BoardOf(tt.url)
		if got := map[bool]string{true: b.String()}[ok]; got != tt.want {
			t.Errorf("BoardOf(%s) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestLeads(t *testing.T) {
	b, err := os.ReadFile("testdata/leads.json")
	if err != nil {
		t.Fatal(err)
	}
	var it item
	if err := json.Unmarshal(b, &it); err != nil {
		t.Fatal(err)
	}
	th := &Thread{ID: "49922569", item: it}
	leads := th.Leads(func(int64) bool { return false })
	if len(leads) != 3 {
		t.Fatalf("got %d leads, want 3 (the deleted post skipped)", len(leads))
	}
	by := map[string]model.Company{}
	for _, l := range leads {
		by[l.Company.Name] = l.Company
	}

	k := by["Kestrel Labs"]
	if k.Country != "IN" || k.Website != "https://kestrel.dev" || len(k.Boards) != 1 || k.Boards[0].String() != "ashby:kestrel" {
		t.Errorf("kestrel = %+v", k)
	}
	if len(k.Contacts) != 1 || k.Contacts[0].Email != "jobs@kestrel.dev" || k.Contacts[0].SourceURL != "https://news.ycombinator.com/item?id=101" {
		t.Errorf("kestrel contacts = %+v", k.Contacts)
	}

	o := by["Orbit Data"]
	var emails []string
	for _, c := range o.Contacts {
		emails = append(emails, c.Email)
	}
	slices.Sort(emails)
	if !slices.Equal(emails, []string{"hiring@orbitdata.io", "maya@orbitdata.io"}) || !o.Remote || o.Country != "" {
		t.Errorf("orbit = %v, remote %v, country %q", emails, o.Remote, o.Country)
	}
	if o.Website != "https://orbitdata.io/careers" { // LinkedIn is skipped
		t.Errorf("orbit website = %q", o.Website)
	}

	p := by["Plainview"]
	if p.Country != "DE" || len(p.Boards) != 1 || p.Boards[0].String() != "greenhouse:plainview" || len(p.Contacts) != 1 || p.Contacts[0].Email != "careers@plainview.de" {
		t.Errorf("plainview = %+v", p)
	}

	// Posts already seen are skipped.
	if l := th.Leads(func(id int64) bool { return id != 103 }); len(l) != 1 || l[0].PostID != 103 {
		t.Errorf("seen filter: %+v", l)
	}
	if ids := th.Posts(); !slices.Equal(ids, []int64{101, 102, 103}) {
		t.Errorf("posts = %v", ids)
	}
}

func TestLeadName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Kestrel Labs", "Kestrel Labs"},
		{"*Kestrel Labs*", "Kestrel Labs"},
		{"Don't Use This Code: https://dutc.io", "Don't Use This Code"},
		{"Scale AI", "Scale AI"},
		{"Data Engineering Co", "Data Engineering Co"},
		{"Confidential", ""},
		{"*Confidential Healthcare AI Startup", ""},
		{"Stealth", ""},
		{"Early-stage Startup", ""},
		{"Software Engineer", ""},
		{"Senior Backend Engineers", ""},
	}
	for _, tt := range tests {
		got, ok := leadName(tt.in)
		if !ok {
			got = ""
		}
		if got != tt.want {
			t.Errorf("leadName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSeekerPostsSkipped(t *testing.T) {
	for _, text := range []string{
		"Location: India<p>Remote: Yes<p>Email: someone@gmail.com",
		"Location: Dorset, UK | Remote: yes<p>me@example.org",
		"Willing to relocate: no<p>a@b.io",
		"SEEKING FREELANCE | Go developer | Remote<p>me@gmail.com",
		"Backend dev for hire | Remote<p>me@gmail.com",
	} {
		if l, ok := lead(item{ID: 1, Author: "x", Text: text}); ok {
			t.Errorf("seeker post imported: %+v", l.Company)
		}
	}
}
