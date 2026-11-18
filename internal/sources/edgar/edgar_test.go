package edgar

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/itxkarthik/jobhunt/internal/sources/sourcetest"
)

func TestCheckUserAgent(t *testing.T) {
	for ua, ok := range map[string]bool{
		"Jane Doe jane@example.com": true,
		"Acme Research ops@acme.io": true,
		"":                          false,
		"jobhunt/0.1":               false,
		"jane@example.com":          false,
		"Jane Doe":                  false,
	} {
		if err := CheckUserAgent(ua); (err == nil) != ok {
			t.Errorf("CheckUserAgent(%q) = %v", ua, err)
		}
	}
}

func TestParseIndex(t *testing.T) {
	b, err := os.ReadFile("testdata/form.20261117.idx")
	if err != nil {
		t.Fatal(err)
	}
	fs, err := ParseIndex(b, "D")
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 5 {
		t.Fatalf("got %d D filings: %+v", len(fs), fs)
	}
	l := fs[2]
	if l.Company != "Lyzr, Inc." || l.CIK != "1994140" || l.Accession != "0001994140-26-000006" || !l.Filed.Equal(time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("lyzr = %+v", l)
	}
	if got := l.DocURL(Base); got != "https://www.sec.gov/Archives/edgar/data/1994140/000199414026000006/primary_doc.xml" {
		t.Errorf("DocURL = %s", got)
	}
	if got := l.IndexURL(); got != "https://www.sec.gov/Archives/edgar/data/1994140/000199414026000006/0001994140-26-000006-index.htm" {
		t.Errorf("IndexURL = %s", got)
	}
	if _, err := ParseIndex([]byte("<html>Access denied</html>"), "D"); err == nil {
		t.Error("a page that isn't an index parsed")
	}
}

func TestCandidate(t *testing.T) {
	tests := []struct {
		name        string
		allEntities bool
		want        bool
	}{
		{"Lyzr, Inc.", false, true},
		{"Brightloop Robotics Inc", false, true},
		{"AC-004 Fund V, a Series of SVP Partners Fund 2025 LLC", true, false},
		{"ARC Greenway Office Investor, LLC", false, false},
		{"ARC Greenway Office Investor, LLC", true, true},
		{"Sunrise Real Estate Holdings", true, false},
		{"Acme Ventures Fund II, L.P.", true, false},
		{"Northwind Capital Partners LP", true, false},
		{"Hiker Labs, Inc.", false, true},
	}
	for _, tt := range tests {
		if got := Candidate(tt.name, tt.allEntities); got != tt.want {
			t.Errorf("Candidate(%q, %v) = %v", tt.name, tt.allEntities, got)
		}
	}
}

func TestParseFormD(t *testing.T) {
	b, _ := os.ReadFile("testdata/lyzr.xml")
	fd, err := ParseFormD(b)
	if err != nil {
		t.Fatal(err)
	}
	if fd.Name != "Lyzr, Inc." || fd.City != "Jersey City" || fd.State != "NJ" || fd.EntityType != "Corporation" ||
		fd.Industry != "Other" || fd.Offering != 33899914 || fd.Sold != 12899955 || fd.Amendment ||
		!fd.FirstSale.Equal(time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("parsed %+v", fd)
	}
	if len(fd.Officers) != 2 || fd.Officers[1].Name != "Madhu Shalini Iyer" || !slices.Equal(fd.Officers[0].Roles, []string{"Executive Officer", "Director"}) {
		t.Errorf("officers %+v", fd.Officers)
	}

	f := Filing{CIK: "1994140", Accession: "0001994140-26-000006", Filed: time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)}
	c := fd.Company(f)
	if c.Country != "US" || c.City != "Jersey City" || len(c.Rounds) != 1 || c.Rounds[0].Amount != 12899955 || c.Rounds[0].SourceURL != f.IndexURL() {
		t.Errorf("company %+v", c)
	}
	// Only executive officers become contacts, with the filing as source and no email.
	if len(c.Contacts) != 1 || c.Contacts[0].Name != "Surendirasiva Selvaraj" || c.Contacts[0].Email != "" || c.Contacts[0].SourceURL != f.IndexURL() {
		t.Errorf("contacts %+v", c.Contacts)
	}

	b, _ = os.ReadFile("testdata/brightloop.xml")
	fd, err = ParseFormD(b)
	if err != nil {
		t.Fatal(err)
	}
	c = fd.Company(f)
	if fd.Name != "BRIGHTLOOP ROBOTICS INC" || fd.Offering != 0 || !c.Rounds[0].Date.Equal(f.Filed) || fd.Officers[0].Name != "Ada Lovelace" {
		t.Errorf("indefinite offering, no first sale yet: %+v, round %+v", fd, c.Rounds[0])
	}
	if _, err := ParseFormD([]byte("<edgarSubmission><submissionType>10-K</submissionType></edgarSubmission>")); err == nil {
		t.Error("non-D document parsed")
	}
}

func TestKeep(t *testing.T) {
	corp := func(industry string) *FormD { return &FormD{EntityType: "Corporation", Industry: industry} }
	tests := []struct {
		fd          *FormD
		industries  []string
		allEntities bool
		want        bool
	}{
		{corp("Other Technology"), DefaultIndustries, false, true},
		{corp("Biotechnology"), DefaultIndustries, false, false},
		{corp("Biotechnology"), []string{"any"}, false, true},
		{corp("other technology"), DefaultIndustries, false, true},
		{&FormD{EntityType: "Limited Liability Company", Industry: "Computers"}, DefaultIndustries, false, false},
		{&FormD{EntityType: "Limited Liability Company", Industry: "Computers"}, DefaultIndustries, true, true},
		{&FormD{EntityType: "Limited Partnership", Industry: "Pooled Investment Fund"}, []string{"any"}, true, false},
		{&FormD{EntityType: "Corporation", Industry: "Computers", Amendment: true}, DefaultIndustries, false, false},
	}
	for i, tt := range tests {
		if got := tt.fd.Keep(tt.industries, tt.allEntities); got != tt.want {
			t.Errorf("%d: Keep = %v", i, got)
		}
	}
}

func TestDays(t *testing.T) {
	// Wednesday 2026-11-18 03:00 UTC: Tuesday's index (built ~10 pm ET)
	// isn't counted yet; Monday's is.
	now := time.Date(2026, 11, 18, 3, 0, 0, 0, time.UTC)
	var got []string
	for _, d := range Days(now, 7*24*time.Hour) {
		got = append(got, d.Format("Mon 02"))
	}
	if strings.Join(got, ",") != "Wed 11,Thu 12,Fri 13,Mon 16" {
		t.Errorf("days = %v", got)
	}
	if d := Days(now.Add(2*time.Hour), 24*time.Hour); len(d) != 1 || d[0].Day() != 17 {
		t.Errorf("after 04:00 UTC, Tuesday counts: %v", d)
	}
}

func TestRun(t *testing.T) {
	srv := sourcetest.Serve(t, map[string]string{
		"/Archives/edgar/daily-index/2026/QTR4/form.20261117.idx":         "testdata/form.20261117.idx",
		"/Archives/edgar/data/1994140/000199414026000006/primary_doc.xml": "testdata/lyzr.xml",
		"/Archives/edgar/data/2170001/000217000126000001/primary_doc.xml": "testdata/brightloop.xml",
		"/Archives/edgar/data/2170002/000217000226000001/primary_doc.xml": "testdata/kettle.xml",
	})
	im := &Importer{Client: sourcetest.Client(), Base: srv.URL}
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC) // Mon 16 and Wed 18 have no index here: 404s
	o := Options{Since: 3 * 24 * time.Hour, Now: now}

	seen := map[string]bool{}
	res, err := im.Run(context.Background(), o, func(item string) bool { return seen[item] })
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range res.Companies {
		names = append(names, c.Name)
	}
	// The fund and the LLC are never fetched; Kettle (biotech) is fetched but dropped.
	if strings.Join(names, ",") != "Lyzr, Inc.,BRIGHTLOOP ROBOTICS INC" || res.Days != 1 || res.Filings != 5 || res.Candidates != 3 || res.Fetched != 3 {
		t.Errorf("run: %v, %+v", names, res)
	}
	if len(srv.Requests()) != 6 { // three indexes (two missing) + three filings
		t.Errorf("requests: %+v", srv.Requests())
	}
	if !slices.Contains(res.Done, "day:20261117") || slices.Contains(res.Done, "day:20261116") || !slices.Contains(res.Done, "acc:0001994140-26-000006") {
		t.Errorf("done = %v (a recent missing day must be retried)", res.Done)
	}

	// A second run with the same items marked done only retries the two
	// recent days that had no index.
	for _, it := range res.Done {
		seen[it] = true
	}
	before := len(srv.Requests())
	res, err = im.Run(context.Background(), o, func(item string) bool { return seen[item] })
	if err != nil || len(res.Companies) != 0 || len(srv.Requests())-before != 2 {
		t.Errorf("second run: %+v, %v, %d requests", res, err, len(srv.Requests())-before)
	}

	// MaxFilings leaves the day unfinished for next time.
	o.MaxFilings = 1
	res, _ = im.Run(context.Background(), o, func(string) bool { return false })
	if res.Complete || res.Fetched != 1 || slices.Contains(res.Done, "day:20261117") {
		t.Errorf("capped run: %+v", res)
	}
}

func TestBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<html>Your Request Originates from an Undeclared Automated Tool</html>"))
	}))
	defer srv.Close()
	im := &Importer{Client: sourcetest.Client(), Base: srv.URL}
	_, err := im.Run(context.Background(), Options{Since: 3 * 24 * time.Hour, Now: time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)}, func(string) bool { return false })
	if !errors.Is(err, ErrBlocked) {
		t.Errorf("err = %v, want ErrBlocked", err)
	}
}
