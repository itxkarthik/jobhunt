// Package company checks, merges and filters the companies list: startups
// and other companies the user watches, with their funding, size, job
// boards and published contacts.
//
// Everything stored here may come from an agent or a fetched page, so it
// is treated as untrusted: text is sanitized like job text, URLs must be
// http(s), and a contact is only kept with the URL where the person
// published it. Nothing here guesses an address or sends email.
package company

import (
	"errors"
	"fmt"
	"math"
	"net/mail"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/itxkarthik/jobhunt/internal/dedup"
	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources"
)

// Limits keep one record from growing without bound.
const (
	MaxName     = 200
	MaxText     = 300
	MaxNotes    = 4000
	MaxTags     = 20
	MaxTag      = 40
	MaxContacts = 20
	MaxRounds   = 20
	MaxBoards   = 5
)

// Key is the identity of a company name, shared with job dedup so a
// company's jobs can be found by their company field.
func Key(name string) string { return dedup.NormCompany(name) }

// ATS lists the job-board providers a Board may name.
var ATS = []string{"greenhouse", "lever", "ashby", "workable", "recruitee"}

var slugRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

// ParseBoard reads "ats:slug", as in "greenhouse:acme".
func ParseBoard(s string) (model.Board, error) {
	ats, slug, ok := strings.Cut(strings.TrimSpace(s), ":")
	b := model.Board{ATS: strings.ToLower(strings.TrimSpace(ats)), Slug: strings.TrimSpace(slug)}
	if !ok {
		return b, fmt.Errorf("board %q: want ats:slug, e.g. greenhouse:acme", s)
	}
	return b, checkBoard(b)
}

func checkBoard(b model.Board) error {
	if !slices.Contains(ATS, b.ATS) {
		return fmt.Errorf("unknown ats %q (want %s)", b.ATS, strings.Join(ATS, ", "))
	}
	if !slugRe.MatchString(b.Slug) {
		return fmt.Errorf("bad %s slug %q", b.ATS, b.Slug)
	}
	return nil
}

// Clean validates c in place: it sanitizes text, checks URLs, enums and
// contacts, and fills Key. It rejects what it can't make safe rather than
// dropping it quietly, so the caller (often an agent) learns what was wrong.
func Clean(c *model.Company) error {
	var err error
	c.Name = clip(sources.SafeText(c.Name, false), MaxName)
	if c.Name == "" {
		return errors.New("name is required")
	}
	if c.Key = Key(c.Name); c.Key == "" {
		return fmt.Errorf("name %q has no letters or digits", c.Name)
	}
	if c.Website, err = optURL("website", c.Website); err != nil {
		return err
	}
	if c.Country != "" {
		code, ok := model.NormalizeCountry(c.Country)
		if !ok || code == model.RemoteGlobal {
			return fmt.Errorf("unknown country %q", c.Country)
		}
		c.Country = code
	}
	c.City = clip(sources.SafeText(c.City, false), MaxText)
	if c.Stage, err = model.ParseStage(string(c.Stage)); err != nil {
		return err
	}
	if c.Headcount, err = model.ParseHeadcount(string(c.Headcount)); err != nil {
		return err
	}
	if c.HeadcountSource = strings.TrimSpace(c.HeadcountSource); c.HeadcountSource != "" && c.HeadcountSource != "estimate" {
		if c.HeadcountSource, err = optURL("headcount source", c.HeadcountSource); err != nil {
			return fmt.Errorf("%w (or \"estimate\")", err)
		}
	}
	if len(c.Rounds) > MaxRounds || len(c.Contacts) > MaxContacts || len(c.Boards) > MaxBoards || len(c.Tags) > MaxTags {
		return fmt.Errorf("too many entries (at most %d rounds, %d contacts, %d boards, %d tags)", MaxRounds, MaxContacts, MaxBoards, MaxTags)
	}
	for i := range c.Rounds {
		if err := cleanRound(&c.Rounds[i]); err != nil {
			return fmt.Errorf("round %d: %w", i+1, err)
		}
	}
	for i := range c.Contacts {
		if err := cleanContact(&c.Contacts[i]); err != nil {
			return fmt.Errorf("contact %d: %w", i+1, err)
		}
	}
	for i := range c.Boards {
		b := &c.Boards[i]
		b.ATS, b.Slug = strings.ToLower(strings.TrimSpace(b.ATS)), strings.TrimSpace(b.Slug)
		if err := checkBoard(*b); err != nil {
			return err
		}
	}
	tags := c.Tags[:0]
	for _, t := range c.Tags {
		t = strings.ToLower(clip(sources.SafeText(t, false), MaxTag))
		if t != "" && !slices.Contains(tags, t) {
			tags = append(tags, t)
		}
	}
	c.Tags = tags
	c.Notes = clip(sources.SafeText(c.Notes, true), MaxNotes)
	for i, s := range c.Sources {
		c.Sources[i] = strings.ToLower(clip(sources.SafeText(s, false), MaxTag))
	}
	c.Sources = slices.DeleteFunc(c.Sources, func(s string) bool { return s == "" })
	return nil
}

func cleanRound(r *model.Round) error {
	var err error
	if r.Stage, err = model.ParseStage(string(r.Stage)); err != nil {
		return err
	}
	if r.Amount < 0 || math.IsNaN(r.Amount) || math.IsInf(r.Amount, 0) {
		return fmt.Errorf("amount %v must be a finite number, 0 or more", r.Amount)
	}
	r.Currency = strings.ToUpper(strings.TrimSpace(r.Currency))
	if r.Currency != "" && !currencyRe.MatchString(r.Currency) {
		return fmt.Errorf("currency %q: want a 3-letter code like USD or INR", r.Currency)
	}
	if !r.Date.IsZero() {
		r.Date = r.Date.UTC().Truncate(24 * time.Hour)
	}
	if r.SourceURL, err = needURL("source_url", r.SourceURL); err != nil {
		return err
	}
	return nil
}

var currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)

func cleanContact(ct *model.Contact) error {
	var err error
	ct.Name = clip(sources.SafeText(ct.Name, false), MaxText)
	ct.Role = clip(sources.SafeText(ct.Role, false), MaxText)
	if ct.Email = strings.TrimSpace(ct.Email); ct.Email != "" {
		a, perr := mail.ParseAddress(ct.Email)
		if perr != nil || a.Address != ct.Email || a.Name != "" || len(ct.Email) > 254 {
			return fmt.Errorf("email %q is not a plain address", ct.Email)
		}
		local, domain, _ := strings.Cut(ct.Email, "@")
		ct.Email = local + "@" + strings.ToLower(domain)
	}
	if ct.URL, err = optURL("url", ct.URL); err != nil {
		return err
	}
	if ct.Name == "" && ct.Email == "" && ct.URL == "" {
		return errors.New("needs a name, email or url")
	}
	// The rule that keeps contacts honest: where did the person publish it?
	if ct.SourceURL, err = needURL("source_url", ct.SourceURL); err != nil {
		return fmt.Errorf("%w: the page where the person published this contact", err)
	}
	return nil
}

func optURL(field, u string) (string, error) {
	if u = strings.TrimSpace(u); u == "" {
		return "", nil
	}
	if sources.SafeURL(u) == "" || len(u) > 2000 {
		return "", fmt.Errorf("%s %q must be an http(s) URL", field, clip(u, 80))
	}
	return u, nil
}

func needURL(field, u string) (string, error) {
	if strings.TrimSpace(u) == "" {
		return "", fmt.Errorf("%s is required", field)
	}
	return optURL(field, u)
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return strings.TrimSpace(s[:n])
}
