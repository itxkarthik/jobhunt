package mca

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/itxkarthik/jobhunt/internal/sources/sourcetest"
)

func TestCIN(t *testing.T) {
	tests := []struct {
		cin, nic string
		match    bool
	}{
		{"U62013KA2026PTC225546", "62013", true},
		{"u63119ka2026ptc224001", "63119", true},
		{"U43900KA2026PTC225573", "43900", false},
		{"L72200MH1995PLC084781", "72200", false},
		{"garbage", "", false},
	}
	for _, tt := range tests {
		if got := NIC(tt.cin); got != tt.nic {
			t.Errorf("NIC(%s) = %q", tt.cin, got)
		}
		if got := MatchNIC(tt.cin, DefaultNIC); got != tt.match {
			t.Errorf("MatchNIC(%s) = %v", tt.cin, got)
		}
	}
	if !MatchNIC("U43900KA2026PTC225573", []string{"any"}) || !MatchNIC("U62013KA2026PTC225546", []string{"62013"}) {
		t.Error("any / full code")
	}
}

func TestRecord(t *testing.T) {
	r := Record{CIN: "U62013KA2026PTC225546", Name: "FUTURE WORK LABS (OPC) PRIVATE LIMITED", PaidUp: "2500000.00", Registered: "2026-08-05",
		State: "karnataka", Address: "NO 12, 2ND FLOOR, 80 FEET ROAD,KORAMANGALA,Bengaluru,karnataka,560034-India", Classification: "Business Services"}
	if r.City() != "Bengaluru" || r.Capital() != 2.5e6 || !r.Date().Equal(time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("city %q capital %v date %v", r.City(), r.Capital(), r.Date())
	}
	c := r.Company()
	if c.Name != "Future Work Labs (OPC) Private Limited" || c.Country != "IN" || !slices.Contains(c.Tags, "nic-62") || len(c.Contacts) != 0 {
		t.Errorf("company %+v", c)
	}
	// The address beyond the city never reaches the record.
	if strings.Contains(c.Notes, "KORAMANGALA") || strings.Contains(c.Notes, "560034") || !strings.Contains(c.Notes, "₹25,00,000") {
		t.Errorf("notes = %q", c.Notes)
	}
	if (Record{Address: "12, 3RD CROSS, 560001-India", State: "karnataka"}).City() != "" {
		t.Error("city from a malformed address")
	}
	if StateName("ka") != "karnataka" || StateName("Tamil Nadu") != "tamil nadu" {
		t.Error("StateName")
	}
}

func TestJumped(t *testing.T) {
	for _, tt := range []struct {
		before, now float64
		want        bool
	}{
		{100000, 2500000, true},
		{500000, 600000, false}, // small rise
		{100000, 150000, false}, // not doubled
		{10000, 30000, false},   // doubled but tiny
		{0, 5000000, false},     // nothing to compare
	} {
		if got := Jumped(tt.before, tt.now); got != tt.want {
			t.Errorf("Jumped(%v, %v) = %v", tt.before, tt.now, got)
		}
	}
}

func client(t *testing.T, file string) (*Client, *sourcetest.Server) {
	srv := sourcetest.Serve(t, map[string]string{"/resource/" + Resource: file})
	return &Client{HTTP: sourcetest.Client(), Base: srv.URL, Key: "testkey"}, srv
}

func TestRun(t *testing.T) {
	ctx := context.Background()
	c, srv := client(t, "testdata/karnataka.json")
	o := Options{States: []string{"KA"}, Since: 30 * 24 * time.Hour}
	res, err := c.Run(ctx, o, func(string) (float64, bool) { return 0, false })
	if err != nil {
		t.Fatal(err)
	}
	// The construction company is outside NIC 62/63; Old Soft is older
	// than 30 days before the newest registration (2026-08-05).
	var names []string
	for _, l := range res.Leads {
		names = append(names, l.Company.Name)
	}
	if strings.Join(names, ",") != "Future Work Labs Private Limited,Kestrel Data Private Limited" || res.Read != 3 || res.Matched != 2 {
		t.Errorf("leads %v, %+v", names, res)
	}
	q := url.Values(srv.Requests()[0].Query)
	if q.Get("api-key") != "testkey" || q.Get("filters[CompanyStateCode]") != "karnataka" || q.Get("filters[CompanyStatus]") != "Active" ||
		q.Get("filters[CompanyClass]") != "Private" || q.Get("sort[CompanyRegistrationdate_date]") != "desc" {
		t.Errorf("query = %v", q)
	}

	// A later import: one company's capital jumped, one rose a little,
	// one is new.
	c, _ = client(t, "testdata/karnataka_later.json")
	stored := map[string]float64{"U62013KA2026PTC225546": 100000, "U63119KA2026PTC224001": 500000}
	res, err = c.Run(ctx, o, func(cin string) (float64, bool) { v, ok := stored[cin]; return v, ok })
	if err != nil || len(res.Leads) != 2 {
		t.Fatalf("later: %+v, %v", res, err)
	}
	jump, fresh := res.Leads[0], res.Leads[1]
	if !jump.Jump || jump.Previous != 100000 || !slices.Contains(jump.Company.Tags, "capital-increase") || jump.Company.Notes != "" ||
		!strings.Contains(jump.CapitalNote(), "possibly a raise, not confirmed") {
		t.Errorf("jump lead %+v", jump)
	}
	if fresh.Jump || fresh.Company.City != "Hubballi" {
		t.Errorf("new lead %+v", fresh)
	}
}

func TestRunErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := (&Client{}).Run(ctx, Options{}, nil); !errors.Is(err, ErrNoKey) {
		t.Errorf("no key: %v", err)
	}
	c, _ := client(t, "testdata/ratelimit.json")
	res, err := c.Run(ctx, Options{States: []string{"KA", "MH"}}, func(string) (float64, bool) { return 0, false })
	if err != nil || len(res.Errors) != 2 || !strings.Contains(res.Errors[0], "Rate limit") || strings.Contains(strings.Join(res.Errors, " "), "testkey") {
		t.Errorf("rate limit: %+v, %v", res, err)
	}
}
