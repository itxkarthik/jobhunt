// Package ats queries companies' public job boards on Greenhouse, Lever, and
// Ashby. Each ATS is one Source that fans out over the boards in
// companies.yaml relevant to the requested countries.
package ats

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
	"gopkg.in/yaml.v3"

	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources"
)

//go:embed companies.yaml
var defaultCompanies []byte

// Company is one board in companies.yaml.
type Company struct {
	Name      string   `yaml:"name"`
	ATS       string   `yaml:"ats"`
	Slug      string   `yaml:"slug"`
	Countries []string `yaml:"countries"`
	Remote    bool     `yaml:"remote"`
	// Added marks a board from the companies list in the database rather
	// than companies.yaml.
	Added bool `yaml:"-"`
}

// Boards is the set of boards the ATS sources fetch: companies.yaml plus
// boards added at runtime to the companies list. It is shared by all ATS
// sources and safe to update while searches run.
type Boards struct {
	mu    sync.RWMutex
	base  []Company
	added []Company
}

// NewBoards returns a set holding base (usually companies.yaml).
func NewBoards(base []Company) *Boards { return &Boards{base: base} }

// SetAdded replaces the runtime boards. Boards already in the base list
// are skipped, so a company in both is fetched once.
func (b *Boards) SetAdded(cs []Company) {
	var out []Company
	b.mu.RLock()
	for _, c := range cs {
		if !slices.ContainsFunc(b.base, func(x Company) bool { return sameBoard(x, c) }) &&
			!slices.ContainsFunc(out, func(x Company) bool { return sameBoard(x, c) }) {
			c.Added = true
			out = append(out, c)
		}
	}
	b.mu.RUnlock()
	b.mu.Lock()
	b.added = out
	b.mu.Unlock()
}

// All returns every board, companies.yaml first.
func (b *Boards) All() []Company {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return slices.Concat(b.base, b.added)
}

// For returns the boards on one ATS.
func (b *Boards) For(atsName string) []Company {
	var out []Company
	for _, c := range b.All() {
		if c.ATS == atsName {
			out = append(out, c)
		}
	}
	return out
}

func sameBoard(a, b Company) bool {
	return a.ATS == b.ATS && strings.EqualFold(a.Slug, b.Slug)
}

// Check fetches one board and returns how many jobs it lists, to vet a
// board before it is saved. client may be nil for a default client.
func Check(ctx context.Context, client *sources.Client, c Company) (int, error) {
	p, ok := providers[c.ATS]
	if !ok {
		return 0, fmt.Errorf("unknown ats %q", c.ATS)
	}
	if client == nil {
		client = sources.NewClient("", rate.Limit(4))
	}
	jobs, err := p.fetchBoard(ctx, client, c)
	return len(jobs), err
}

// Gone reports whether a board fetch failed because the ATS says the
// board doesn't exist (404 or 410), as opposed to a failure that may pass.
func Gone(err error) bool {
	se, ok := errors.AsType[*sources.StatusError](err)
	return ok && (se.Code == http.StatusNotFound || se.Code == http.StatusGone)
}

// LoadCompanies reads path if it exists, else the embedded starter list.
func LoadCompanies(path string) ([]Company, error) {
	b := defaultCompanies
	if path != "" {
		data, err := os.ReadFile(path) //nolint:gosec // G304: the user's own companies file from config
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return nil, err
		default:
			b = data
		}
	}
	var doc struct {
		Companies []Company `yaml:"companies"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("companies.yaml: %w", err)
	}
	for i, c := range doc.Companies {
		if c.Slug == "" || c.Name == "" {
			return nil, fmt.Errorf("companies.yaml entry %d: name and slug are required", i+1)
		}
		if _, ok := providers[c.ATS]; !ok {
			return nil, fmt.Errorf("companies.yaml %s: unknown ats %q (want %s)", c.Name, c.ATS, strings.Join(ATSNames, ", "))
		}
		for j, cc := range c.Countries {
			code, ok := model.NormalizeCountry(cc)
			if !ok {
				return nil, fmt.Errorf("companies.yaml %s: unknown country %q", c.Name, cc)
			}
			doc.Companies[i].Countries[j] = code
		}
	}
	return doc.Companies, nil
}

// provider fetches and parses one board.
type provider interface {
	fetchBoard(ctx context.Context, cl *sources.Client, c Company) ([]model.Job, error)
	boardURL(c Company) string
}

var providers = map[string]provider{
	"greenhouse": greenhouse{base: "https://boards-api.greenhouse.io/v1/boards"},
	"lever":      lever{base: "https://api.lever.co/v0/postings"},
	"ashby":      ashby{base: "https://api.ashbyhq.com/posting-api/job-board"},
	"workable":   workable{base: "https://apply.workable.com/api/v1/widget/accounts"},
	"recruitee":  recruitee{},
}

// ATSNames lists the supported ATS providers.
var ATSNames = []string{"greenhouse", "lever", "ashby", "workable", "recruitee"}

// Source is one ATS across many company boards.
type Source struct {
	ATS         string
	Client      *sources.Client
	Concurrency int
	list        *Boards
	prov        provider
}

// New returns a Source for ats over the matching companies. base overrides
// the API base URL (tests); pass "" for the real one.
func New(atsName string, all []Company, client *sources.Client, base string) *Source {
	return NewShared(atsName, NewBoards(all), client, base)
}

// NewShared returns a Source reading its boards from list, which the
// caller may update between searches.
func NewShared(atsName string, list *Boards, client *sources.Client, base string) *Source {
	p := providers[atsName]
	if base != "" {
		switch atsName {
		case "greenhouse":
			p = greenhouse{base: base}
		case "lever":
			p = lever{base: base}
		case "ashby":
			p = ashby{base: base}
		case "workable":
			p = workable{base: base}
		case "recruitee":
			p = recruitee{base: base}
		}
	}
	return &Source{ATS: atsName, Client: client, Concurrency: 4, list: list, prov: p}
}

// Companies returns the boards on this ATS.
func (s *Source) Companies() []Company { return s.list.For(s.ATS) }

func (s *Source) Name() string              { return s.ATS }
func (s *Source) Rate() rate.Limit          { return rate.Limit(4) }
func (s *Source) DefaultTTL() time.Duration { return 6 * time.Hour }
func (s *Source) Timeout() time.Duration    { return 2 * time.Minute }

func (s *Source) Supports(f model.Filters) bool { return len(s.boards(f)) > 0 }

func (s *Source) CacheKey(f model.Filters) string {
	var slugs []string
	for _, c := range s.boards(f) {
		slugs = append(slugs, c.Slug)
	}
	sort.Strings(slugs)
	return strings.Join(slugs, ",")
}

// boards returns companies hiring in any requested country. Remote-friendly
// boards are included too unless the search is strict, since their
// worldwide-remote jobs pass the country filter.
func (s *Source) boards(f model.Filters) []Company {
	all := s.Companies()
	if len(f.Countries) == 0 {
		return all
	}
	var out []Company
	for _, c := range all {
		for _, want := range f.Countries {
			if (c.Remote && (want == model.RemoteGlobal || !f.StrictCountry)) || slices.Contains(c.Countries, want) {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

func (s *Source) Fetch(ctx context.Context, f model.Filters) ([]model.Job, error) {
	boards := s.boards(f)
	var (
		mu       sync.Mutex
		all      []model.Job
		failed   []string
		firstErr error
	)
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(s.Concurrency)
	for _, c := range boards {
		g.Go(func() error {
			jobs, err := s.prov.fetchBoard(gctx, s.Client, c)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed = append(failed, c.Slug)
				if firstErr == nil {
					firstErr = fmt.Errorf("%s: %w", c.Slug, err)
				}
				return nil
			}
			all = append(all, jobs...)
			return nil
		})
	}
	_ = g.Wait()
	if firstErr != nil {
		sort.Strings(failed)
		if len(all) == 0 {
			return nil, firstErr
		}
		return all, &sources.PartialError{Failed: failed, Total: len(boards), Err: firstErr}
	}
	return all, nil
}

// BoardResult is one row of `jobhunt sources --check-boards`.
type BoardResult struct {
	Company Company
	Count   int
	Err     error
}

// CheckBoards fetches every board and reports job counts or errors, to find
// slugs that went stale (companies switch ATS often).
func (s *Source) CheckBoards(ctx context.Context) []BoardResult {
	all := s.Companies()
	out := make([]BoardResult, len(all))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(s.Concurrency)
	for i, c := range all {
		g.Go(func() error {
			jobs, err := s.prov.fetchBoard(gctx, s.Client, c)
			out[i] = BoardResult{Company: c, Count: len(jobs), Err: err}
			return nil
		})
	}
	_ = g.Wait()
	return out
}

func (s *Source) SearchURL(f model.Filters) string {
	if b := s.boards(f); len(b) > 0 {
		return s.prov.boardURL(b[0])
	}
	return ""
}

// locate merges structured country info with location text.
func locate(texts ...string) (countries []string, city string) {
	seen := map[string]bool{}
	for _, t := range texts {
		cs, ct := model.ParseLocation(t)
		for _, c := range cs {
			if !seen[c] {
				seen[c] = true
				countries = append(countries, c)
			}
		}
		if city == "" {
			city = ct
		}
	}
	return countries, city
}

const maxDesc = 5000
