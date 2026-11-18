// Package edgar imports startup funding rounds from SEC Form D filings.
//
// A US company raising a private round files Form D with the SEC within
// 15 days of the first sale. EDGAR publishes a daily index of every
// filing and each Form D as XML, which gives the issuer, its industry,
// the amount sold, the date of first sale and its executive officers.
//
// Access (checked 2026-11-19): https://www.sec.gov/os/accessing-edgar-data
// allows automated access at up to 10 requests per second and asks every
// client to declare a User-Agent naming who runs it with a contact
// address ("Sample Company Name AdminContact@<sample company domain>.com").
// robots.txt allows /Archives/edgar/. This package stays off until the
// user sets that User-Agent, never invents one, keeps to 5 requests a
// second and fetches only the daily index and the Form D documents of
// likely startups.
//
// Most Form D filers are investment funds and SPVs. Names that look like
// funds, and (by default) LLCs and LPs, are skipped before their filing is
// fetched; after parsing, only corporations in the chosen industries are
// kept.
package edgar

import (
	"bufio"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources"
)

// Base is EDGAR's address.
const Base = "https://www.sec.gov"

// Rate keeps well under EDGAR's 10 requests per second.
const Rate = 5

// DefaultIndustries are the Form D industry groups kept unless configured
// otherwise. Many software startups file as "Other".
var DefaultIndustries = []string{"Computers", "Telecommunications", "Other Technology", "Other"}

// uaRe is a loose check that the User-Agent names someone and gives an
// email address, as EDGAR's fair-access policy asks.
var uaRe = regexp.MustCompile(`^\S.*\s\S+@\S+\.\S+$`)

// CheckUserAgent reports whether ua looks like "Name contact@example.com".
func CheckUserAgent(ua string) error {
	if !uaRe.MatchString(strings.TrimSpace(ua)) {
		return errors.New(`EDGAR needs a User-Agent with your name and a contact email, e.g. "Jane Doe jane@example.com": set importers.edgar.user_agent in config.yaml or JOBHUNT_EDGAR_USER_AGENT`)
	}
	return nil
}

// Filing is one line of a daily form index.
type Filing struct {
	Form      string
	Company   string
	CIK       string
	Filed     time.Time
	Accession string // 0001994140-26-000006
}

func (f Filing) folder(base string) string {
	return base + "/Archives/edgar/data/" + f.CIK + "/" + strings.ReplaceAll(f.Accession, "-", "") + "/"
}

// DocURL is the Form D XML document.
func (f Filing) DocURL(base string) string { return f.folder(base) + "primary_doc.xml" }

// IndexURL is the filing's page on EDGAR, the source link for its round.
func (f Filing) IndexURL() string { return f.folder(Base) + f.Accession + "-index.htm" }

// Importer reads Form D filings.
type Importer struct {
	Client *sources.Client // must carry the declared User-Agent
	Base   string          // Base, or a test server
}

// ErrBlocked means EDGAR refused the client, usually for an undeclared
// User-Agent or too many requests. Stop and don't retry soon.
var ErrBlocked = errors.New("EDGAR refused the request (check the User-Agent; wait before retrying)")

// ErrNoIndex means there is no index for the day: a weekend, a holiday,
// or not published yet.
var ErrNoIndex = errors.New("no index for that day")

func (im *Importer) get(ctx context.Context, url string) ([]byte, error) {
	b, err := im.Client.Get(ctx, url, nil) // Go asks for gzip and decodes it itself
	if se, ok := errors.AsType[*sources.StatusError](err); ok {
		if se.Code == http.StatusForbidden && (strings.Contains(se.Body, "Undeclared Automated Tool") || strings.Contains(se.Body, "Request Rate Threshold")) {
			return nil, ErrBlocked
		}
	}
	return b, err
}

// DailyIndex returns the new Form D filings (not amendments) of one day.
func (im *Importer) DailyIndex(ctx context.Context, day time.Time) ([]Filing, error) {
	q := (int(day.Month())-1)/3 + 1
	url := fmt.Sprintf("%s/Archives/edgar/daily-index/%d/QTR%d/form.%s.idx", im.Base, day.Year(), q, day.Format("20060102"))
	b, err := im.get(ctx, url)
	if se, ok := errors.AsType[*sources.StatusError](err); ok && (se.Code == http.StatusNotFound || se.Code == http.StatusForbidden) {
		return nil, ErrNoIndex
	}
	if err != nil {
		return nil, err
	}
	return ParseIndex(b, "D")
}

// indexRow matches "D  Lyzr, Inc.  1994140  20261117  edgar/data/1994140/0001994140-26-000006.txt".
var indexRow = regexp.MustCompile(`^(\S+)\s{2,}(.+?)\s{2,}(\d{1,10})\s+(\d{8})\s+(edgar/data/\d+/(\d{10}-\d{2}-\d{6})\.txt)\s*$`)

// ParseIndex reads a daily form index and returns the rows of one form type.
func ParseIndex(b []byte, form string) ([]Filing, error) {
	var out []Filing
	rows := 0
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		m := indexRow.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		rows++
		if m[1] != form {
			continue
		}
		filed, err := time.Parse("20060102", m[4])
		if err != nil {
			continue
		}
		out = append(out, Filing{Form: m[1], Company: strings.TrimSpace(m[2]), CIK: m[3], Filed: filed, Accession: m[6]})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if rows == 0 && len(bytes.TrimSpace(b)) > 0 {
		return nil, errors.New("daily index has no rows in the expected format")
	}
	return out, nil
}

// fundRe matches issuer names that are almost always funds or SPVs.
var fundRe = regexp.MustCompile(`(?i)\b(fund|funds|series of|a series|spv|co-?invest\w*|feeder|master fund|reit|real estate|properties|realty|apartments|portfolio|investors|capital partners|ventures? (?:fund|partners)|opportunity zone|trust|aggregator)\b|\bl\.?p\.?$|\blp\b`)
var llcRe = regexp.MustCompile(`(?i)\bl\.?l\.?c\.?$`)

// Candidate reports whether a filing's issuer name looks like an operating
// company worth fetching. Without allEntities, LLC names are skipped too.
func Candidate(name string, allEntities bool) bool {
	n := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(name), ".,"))
	if fundRe.MatchString(n) {
		return false
	}
	return allEntities || !llcRe.MatchString(n)
}

// FormD is the part of a Form D filing jobhunt uses.
type FormD struct {
	Name         string
	EntityType   string
	City, State  string
	Industry     string
	Offering     float64 // total offering amount; 0 if indefinite
	Sold         float64 // total amount sold so far
	FirstSale    time.Time
	Amendment    bool
	Officers     []Officer
	Jurisdiction string
}

// Officer is a related person listed on the filing.
type Officer struct {
	Name  string
	Roles []string // Executive Officer, Director, Promoter
}

type xmlFormD struct {
	SubmissionType string `xml:"submissionType"`
	Issuer         struct {
		EntityName   string `xml:"entityName"`
		EntityType   string `xml:"entityType"`
		Jurisdiction string `xml:"jurisdictionOfInc"`
		Address      struct {
			City  string `xml:"city"`
			State string `xml:"stateOrCountry"`
		} `xml:"issuerAddress"`
	} `xml:"primaryIssuer"`
	Persons []struct {
		First  string   `xml:"relatedPersonName>firstName"`
		Middle string   `xml:"relatedPersonName>middleName"`
		Last   string   `xml:"relatedPersonName>lastName"`
		Roles  []string `xml:"relatedPersonRelationshipList>relationship"`
	} `xml:"relatedPersonsList>relatedPersonInfo"`
	Offering struct {
		Industry  string `xml:"industryGroup>industryGroupType"`
		Amendment string `xml:"typeOfFiling>newOrAmendment>isAmendment"`
		FirstSale string `xml:"typeOfFiling>dateOfFirstSale>value"`
		Total     string `xml:"offeringSalesAmounts>totalOfferingAmount"`
		Sold      string `xml:"offeringSalesAmounts>totalAmountSold"`
	} `xml:"offeringData"`
}

// ParseFormD reads a Form D primary document.
func ParseFormD(b []byte) (*FormD, error) {
	var x xmlFormD
	if err := xml.Unmarshal(b, &x); err != nil {
		return nil, fmt.Errorf("form D: %w", err)
	}
	if !strings.HasPrefix(x.SubmissionType, "D") || x.Issuer.EntityName == "" {
		return nil, fmt.Errorf("form D: unexpected document (type %q)", x.SubmissionType)
	}
	fd := &FormD{
		Name:         strings.TrimSpace(x.Issuer.EntityName),
		EntityType:   strings.TrimSpace(x.Issuer.EntityType),
		City:         titleCase(x.Issuer.Address.City),
		State:        strings.TrimSpace(x.Issuer.Address.State),
		Jurisdiction: strings.TrimSpace(x.Issuer.Jurisdiction),
		Industry:     strings.TrimSpace(x.Offering.Industry),
		Offering:     amount(x.Offering.Total),
		Sold:         amount(x.Offering.Sold),
		Amendment:    strings.EqualFold(strings.TrimSpace(x.Offering.Amendment), "true"),
	}
	if d, err := time.Parse(time.DateOnly, strings.TrimSpace(x.Offering.FirstSale)); err == nil {
		fd.FirstSale = d
	}
	for _, p := range x.Persons {
		name := strings.Join(sources.NonEmpty(p.First, p.Middle, p.Last), " ")
		if name == "" {
			continue
		}
		fd.Officers = append(fd.Officers, Officer{Name: titleCase(name), Roles: sources.NonEmpty(p.Roles...)})
	}
	return fd, nil
}

func amount(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || v < 0 {
		return 0 // "Indefinite" and the like
	}
	return v
}

// titleCase tidies the ALL-CAPS names EDGAR often holds.
func titleCase(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || s != strings.ToUpper(s) {
		return s
	}
	words := strings.Fields(strings.ToLower(s))
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// Keep reports whether a parsed filing is a likely startup round:
// a corporation (unless allEntities) in one of industries ("any" or empty
// keeps every industry).
func (fd *FormD) Keep(industries []string, allEntities bool) bool {
	if fd.Amendment || fd.Industry == "Pooled Investment Fund" {
		return false
	}
	if !allEntities && !strings.EqualFold(fd.EntityType, "Corporation") {
		return false
	}
	if len(industries) == 0 || slices.Contains(industries, "any") {
		return true
	}
	return slices.ContainsFunc(industries, func(i string) bool { return strings.EqualFold(i, fd.Industry) })
}

// Company turns a filing into a company update: a US company with this
// round and its executive officers, all sourced to the filing. Officers
// get no email: Form D doesn't publish one.
func (fd *FormD) Company(f Filing) model.Company {
	src := f.IndexURL()
	date := fd.FirstSale
	if date.IsZero() {
		date = f.Filed
	}
	amt := fd.Sold
	if amt == 0 {
		amt = fd.Offering
	}
	c := model.Company{
		Name:    fd.Name,
		Country: "US",
		City:    fd.City,
		Rounds:  []model.Round{{Amount: amt, Currency: "USD", Date: date, SourceURL: src}},
		Tags:    []string{"form-d"},
		Sources: []string{"edgar"},
	}
	if fd.Industry != "" && fd.Industry != "Other" {
		c.Tags = append(c.Tags, strings.ToLower(fd.Industry))
	}
	for _, o := range fd.Officers {
		if slices.Contains(o.Roles, "Executive Officer") {
			c.Contacts = append(c.Contacts, model.Contact{Name: o.Name, Role: strings.Join(o.Roles, ", "), SourceURL: src})
		}
	}
	if len(c.Contacts) > 5 {
		c.Contacts = c.Contacts[:5]
	}
	return c
}

// FetchFormD fetches and parses one filing.
func (im *Importer) FetchFormD(ctx context.Context, f Filing) (*FormD, error) {
	b, err := im.get(ctx, f.DocURL(im.Base))
	if err != nil {
		return nil, err
	}
	return ParseFormD(b)
}
