package model

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Stage is a company's funding stage.
type Stage string

const (
	StageUnknown      Stage = "unknown"
	StagePreSeed      Stage = "pre-seed"
	StageSeed         Stage = "seed"
	StageSeriesA      Stage = "series-a"
	StageSeriesB      Stage = "series-b"
	StageSeriesC      Stage = "series-c"
	StageGrowth       Stage = "growth" // series D and later, or late private rounds
	StagePublic       Stage = "public"
	StageBootstrapped Stage = "bootstrapped"
)

// AllStages lists the known stages, earliest first.
var AllStages = []Stage{StagePreSeed, StageSeed, StageSeriesA, StageSeriesB, StageSeriesC, StageGrowth, StagePublic, StageBootstrapped}

var stageAliases = map[string]Stage{
	"angel": StagePreSeed, "preseed": StagePreSeed,
	"a": StageSeriesA, "b": StageSeriesB, "c": StageSeriesC,
	"series-d": StageGrowth, "series-e": StageGrowth, "series-f": StageGrowth, "late": StageGrowth,
	"ipo": StagePublic, "listed": StagePublic,
	"self-funded": StageBootstrapped,
}

// ParseStage accepts the stage names plus spellings like "Series A",
// "series_b" and "angel". An empty string is unknown.
func ParseStage(s string) (Stage, error) {
	s = strings.NewReplacer(" ", "-", "_", "-").Replace(norm(s))
	if s == "" || s == string(StageUnknown) {
		return StageUnknown, nil
	}
	if st, ok := stageAliases[s]; ok {
		return st, nil
	}
	for _, st := range AllStages {
		if string(st) == s {
			return st, nil
		}
	}
	return "", fmt.Errorf("unknown stage %q (want %s)", s, joinStrs(AllStages))
}

// Headcount is a company-size bucket.
type Headcount string

const (
	HeadcountUnknown  Headcount = "unknown"
	Headcount1to10    Headcount = "1-10"
	Headcount11to50   Headcount = "11-50"
	Headcount51to200  Headcount = "51-200"
	Headcount201to500 Headcount = "201-500"
	Headcount501to1k  Headcount = "501-1000"
	Headcount1kPlus   Headcount = "1000+"
)

// AllHeadcounts lists the buckets, smallest first.
var AllHeadcounts = []Headcount{Headcount1to10, Headcount11to50, Headcount51to200, Headcount201to500, Headcount501to1k, Headcount1kPlus}

// ParseHeadcount accepts a bucket name or a plain number of people, which
// is put in its bucket. An empty string is unknown.
func ParseHeadcount(s string) (Headcount, error) {
	s = norm(s)
	if s == "" || s == string(HeadcountUnknown) {
		return HeadcountUnknown, nil
	}
	for _, h := range AllHeadcounts {
		if string(h) == s {
			return h, nil
		}
	}
	if n, err := strconv.Atoi(strings.TrimSuffix(s, "+")); err == nil && n > 0 {
		switch {
		case n <= 10:
			return Headcount1to10, nil
		case n <= 50:
			return Headcount11to50, nil
		case n <= 200:
			return Headcount51to200, nil
		case n <= 500:
			return Headcount201to500, nil
		case n <= 1000:
			return Headcount501to1k, nil
		default:
			return Headcount1kPlus, nil
		}
	}
	return "", fmt.Errorf("unknown headcount %q (want a number or %s)", s, joinStrs(AllHeadcounts))
}

// Round is one funding round, with where it was reported.
type Round struct {
	Stage     Stage     `json:"stage,omitempty"`
	Amount    float64   `json:"amount,omitempty"`
	Currency  string    `json:"currency,omitempty"`
	Date      time.Time `json:"date,omitzero"`
	SourceURL string    `json:"source_url"`
}

// Contact is a person at a company. It is only kept with the URL where the
// person published it, so every address can be traced to its source.
type Contact struct {
	Name      string    `json:"name,omitempty"`
	Role      string    `json:"role,omitempty"`
	Email     string    `json:"email,omitempty"`
	URL       string    `json:"url,omitempty"`
	SourceURL string    `json:"source_url"`
	AddedAt   time.Time `json:"added_at,omitzero"`
}

// Board is a company's job board on an ATS.
type Board struct {
	ATS  string `json:"ats"`
	Slug string `json:"slug"`
}

func (b Board) String() string { return b.ATS + ":" + b.Slug }

// Company is a company to watch, usually a startup.
type Company struct {
	Key             string    `json:"key"` // dedup.NormCompany(Name)
	Name            string    `json:"name"`
	Website         string    `json:"website,omitempty"`
	Country         string    `json:"country,omitempty"`
	City            string    `json:"city,omitempty"`
	Stage           Stage     `json:"stage"`
	Rounds          []Round   `json:"rounds,omitempty"` // newest first
	Headcount       Headcount `json:"headcount"`
	HeadcountSource string    `json:"headcount_source,omitempty"` // a URL, or "estimate"
	Boards          []Board   `json:"boards,omitempty"`
	Contacts        []Contact `json:"contacts,omitempty"`
	Tags            []string  `json:"tags,omitempty"`
	Notes           string    `json:"notes,omitempty"`
	Sources         []string  `json:"sources,omitempty"` // how it was found: manual, agent, edgar, hn
	CheckedAt       time.Time `json:"checked_at,omitzero"`
	CreatedAt       time.Time `json:"created_at,omitzero"`
	UpdatedAt       time.Time `json:"updated_at,omitzero"`
}

// LatestRound returns the newest round, or nil.
func (c *Company) LatestRound() *Round {
	if len(c.Rounds) == 0 {
		return nil
	}
	return &c.Rounds[0]
}

func joinStrs[T ~string](xs []T) string {
	s := make([]string, len(xs))
	for i, x := range xs {
		s[i] = string(x)
	}
	return strings.Join(s, ", ")
}
