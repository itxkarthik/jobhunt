// Package mca imports recently registered Indian companies from the
// Ministry of Corporate Affairs company master data.
//
// India has no free equivalent of the SEC's Form D, so this is the
// closest official source: the "Registrars of Companies (RoC)-wise
// Company Master Data" dataset on the Open Government Data platform,
// read through its API (api.data.gov.in) with a free key. Checked
// 2026-11-19:
//
//   - www.data.gov.in's robots.txt disallows crawling the website, so
//     only the API is used, never the website.
//   - data.gov.in publishes its datasets under the Government Open Data
//     License – India (GODL), which allows reuse with attribution; every
//     imported company carries the dataset's API URL as its source.
//   - The dataset is updated about monthly and lags: in November 2026
//     its newest registrations were from early August. The --since window
//     therefore counts back from the newest registration in the data,
//     not from today.
//
// What it gives: active private companies registered recently, with their
// industry code (from the CIN), state, city and paid-up capital. There is
// no funding data. A large jump in paid-up capital between imports is
// flagged as a possible raise (tag capital-increase), never a confirmed
// round. The registered office address is personal data for many small
// companies; only the city is kept, and nothing goes into contacts.
package mca

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources"
)

// Base is the API; Resource the company master dataset.
const (
	Base     = "https://api.data.gov.in"
	Resource = "4dbe5667-7b6b-41d7-82af-211562424d9a"
	Rate     = 2 // requests per second
	PageSize = 500
)

// SourceURL is the source link stored on imported companies.
const SourceURL = Base + "/resource/" + Resource

// DefaultNIC are the industry codes kept by default: computer
// programming and consultancy (62) and information services (63).
var DefaultNIC = []string{"62", "63"}

// states maps state codes to the names the dataset uses.
var states = map[string]string{
	"AP": "andhra pradesh", "AS": "assam", "BR": "bihar", "CH": "chandigarh", "CT": "chhattisgarh",
	"DL": "delhi", "GA": "goa", "GJ": "gujarat", "HR": "haryana", "HP": "himachal pradesh",
	"JH": "jharkhand", "KA": "karnataka", "KL": "kerala", "MP": "madhya pradesh", "MH": "maharashtra",
	"OR": "odisha", "PB": "punjab", "RJ": "rajasthan", "TN": "tamil nadu", "TG": "telangana",
	"UP": "uttar pradesh", "UK": "uttarakhand", "WB": "west bengal",
}

// StateName turns a code (KA) or a name (Karnataka) into the dataset's value.
func StateName(s string) string {
	s = strings.TrimSpace(s)
	if n, ok := states[strings.ToUpper(s)]; ok {
		return n
	}
	return strings.ToLower(s)
}

// Record is one company row.
type Record struct {
	CIN            string `json:"CIN"`
	Name           string `json:"CompanyName"`
	Class          string `json:"CompanyClass"`
	Status         string `json:"CompanyStatus"`
	PaidUp         string `json:"PaidupCapital"`
	Registered     string `json:"CompanyRegistrationdate_date"`
	State          string `json:"CompanyStateCode"`
	Address        string `json:"Registered_Office_Address"`
	Classification string `json:"CompanyIndustrialClassification"`
}

type page struct {
	Status  string   `json:"status"`
	Message string   `json:"message"`
	Error   string   `json:"error"`
	Total   int      `json:"total"`
	Records []Record `json:"records"`
}

// Client reads the dataset.
type Client struct {
	HTTP *sources.Client
	Base string
	Key  string
}

// ErrNoKey means the API key is missing.
var ErrNoKey = errors.New("the MCA importer needs a free data.gov.in API key: set importers.mca.api_key in config.yaml or JOBHUNT_DATAGOV_API_KEY")

// Page fetches one page of a state's active private companies, newest
// registrations first.
func (c *Client) Page(ctx context.Context, state string, offset, limit int) ([]Record, int, error) {
	if c.Key == "" {
		return nil, 0, ErrNoKey
	}
	q := url.Values{}
	q.Set("api-key", c.Key)
	q.Set("format", "json")
	q.Set("offset", strconv.Itoa(offset))
	q.Set("limit", strconv.Itoa(limit))
	q.Set("filters[CompanyStateCode]", state)
	q.Set("filters[CompanyStatus]", "Active")
	q.Set("filters[CompanyClass]", "Private")
	q.Set("sort[CompanyRegistrationdate_date]", "desc")
	var p page
	if err := c.HTTP.GetJSON(ctx, c.Base+"/resource/"+Resource+"?"+q.Encode(), nil, &p); err != nil {
		return nil, 0, err
	}
	if p.Error != "" {
		return nil, 0, fmt.Errorf("data.gov.in: %s", sources.Truncate(p.Error, 120))
	}
	if p.Status != "" && p.Status != "ok" {
		return nil, 0, fmt.Errorf("data.gov.in: %s", sources.Truncate(p.Message, 120))
	}
	return p.Records, p.Total, nil
}

// cinRe reads a CIN: listing (L/U), 5-digit NIC code, state, year,
// company type, number.
var cinRe = regexp.MustCompile(`^[LU](\d{5})([A-Z]{2})(\d{4})([A-Z]{3})(\d{6})$`)

// NIC returns the 5-digit industry code in a CIN, or "".
func NIC(cin string) string {
	m := cinRe.FindStringSubmatch(strings.ToUpper(strings.TrimSpace(cin)))
	if m == nil {
		return ""
	}
	return m[1]
}

// MatchNIC reports whether the CIN's industry code starts with one of
// prefixes ("any" matches everything).
func MatchNIC(cin string, prefixes []string) bool {
	if slices.Contains(prefixes, "any") {
		return true
	}
	nic := NIC(cin)
	return nic != "" && slices.ContainsFunc(prefixes, func(p string) bool { return strings.HasPrefix(nic, strings.TrimSpace(p)) })
}

// Date parses the registration date.
func (r Record) Date() time.Time {
	t, err := time.Parse(time.DateOnly, strings.TrimSpace(r.Registered))
	if err != nil {
		return time.Time{}
	}
	return t
}

// Capital parses the paid-up capital in rupees.
func (r Record) Capital() float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(r.PaidUp), 64)
	if err != nil || v < 0 {
		return 0
	}
	return v
}

// City reads the city from the registered office address, which ends
// "…, City, State, PIN-India". The rest of the address is dropped.
func (r Record) City() string {
	parts := strings.Split(r.Address, ",")
	for i := len(parts) - 1; i >= 1; i-- {
		if strings.EqualFold(strings.TrimSpace(parts[i]), r.State) {
			city := strings.TrimSpace(parts[i-1])
			if city != "" && !strings.ContainsAny(city, "0123456789") {
				return titleCase(city)
			}
			return ""
		}
	}
	return ""
}

// Company turns a record into a company lead.
func (r Record) Company() model.Company {
	nic := NIC(r.CIN)
	c := model.Company{
		Name:    titleCase(r.Name),
		Country: "IN",
		City:    r.City(),
		Tags:    []string{"mca"},
		Sources: []string{"mca"},
		Notes: fmt.Sprintf("Registered %s in %s (CIN %s, %s, paid-up capital ₹%s). Source: MCA company master data, %s",
			r.Registered, titleCase(r.State), r.CIN, r.Classification, money(r.Capital()), SourceURL),
	}
	if len(nic) >= 2 {
		c.Tags = append(c.Tags, "nic-"+nic[:2])
	}
	return c
}

func money(v float64) string {
	s := strconv.FormatFloat(v, 'f', 0, 64)
	// Indian grouping: 12,34,56,789
	if len(s) <= 3 {
		return s
	}
	head, tail := s[:len(s)-3], s[len(s)-3:]
	var parts []string
	for len(head) > 2 {
		parts = append([]string{head[len(head)-2:]}, parts...)
		head = head[:len(head)-2]
	}
	if head != "" {
		parts = append([]string{head}, parts...)
	}
	return strings.Join(parts, ",") + "," + tail
}

func titleCase(s string) string {
	words := strings.Fields(strings.ToLower(s))
	for i, w := range words {
		if w == "(opc)" {
			words[i] = "(OPC)"
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// Jumped reports whether paid-up capital rose enough between imports to
// suggest a raise: at least doubled and up by ₹10 lakh or more.
func Jumped(before, now float64) bool {
	return before > 0 && now >= 2*before && now-before >= 1_000_000
}
