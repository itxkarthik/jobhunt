package app

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/itxkarthik/jobhunt/internal/config"
	"github.com/itxkarthik/jobhunt/internal/model"
)

// FilterInput is the user-facing form of model.Filters, shared by CLI
// flags and the HTTP API so both apply the same parsing and defaults.
type FilterInput struct {
	Countries      []string `json:"countries,omitempty"`
	Modes          []string `json:"modes,omitempty"`
	Types          []string `json:"types,omitempty"`
	Areas          []string `json:"areas,omitempty"`
	Experience     []string `json:"experience,omitempty"`
	Keywords       []string `json:"keywords,omitempty"`
	Exclude        []string `json:"exclude,omitempty"`
	PostedWithin   string   `json:"posted_within,omitempty"` // 24h, 3d, 7d, 30d
	Visa           bool     `json:"visa,omitempty"`
	MinSalary      float64  `json:"min_salary,omitempty"`
	Currency       string   `json:"currency,omitempty"`
	StrictCountry  bool     `json:"strict_country,omitempty"`
	IncludeUnknown bool     `json:"include_unknown,omitempty"`
}

// Any is the filter value meaning "no restriction". As the only value of
// countries, modes, types or areas it lifts the config default, which an
// empty field would apply.
const Any = "any"

// ParseFilters validates in and fills empty countries, modes, types and
// areas from the config defaults.
func ParseFilters(d config.Defaults, in FilterInput) (model.Filters, error) {
	var f model.Filters
	countries, err := orDefault(in.Countries, d.Countries)
	if err != nil {
		return f, err
	}
	for _, c := range countries {
		code, ok := model.NormalizeCountry(c)
		if !ok {
			return f, fmt.Errorf("unknown country %q", c)
		}
		f.Countries = append(f.Countries, code)
	}
	if f.Modes, err = parseDefault(in.Modes, d.Modes, model.ParseWorkMode); err != nil {
		return f, err
	}
	if f.Types, err = parseDefault(in.Types, d.Types, model.ParseJobType); err != nil {
		return f, err
	}
	if f.Areas, err = parseDefault(in.Areas, d.Areas, model.ParseArea); err != nil {
		return f, err
	}
	if f.Experience, err = parseAll(in.Experience, model.ParseExperience); err != nil {
		return f, err
	}
	f.Keywords, f.Exclude = trimAll(in.Keywords), trimAll(in.Exclude)
	if in.PostedWithin != "" {
		if f.PostedWithin, err = ParseAge(in.PostedWithin); err != nil {
			return f, err
		}
	}
	// NaN would silently disable the filter and +Inf would drop every job.
	if in.MinSalary < 0 || math.IsNaN(in.MinSalary) || math.IsInf(in.MinSalary, 0) {
		return f, fmt.Errorf("min salary %v must be a finite number, 0 or more", in.MinSalary)
	}
	f.VisaOnly, f.MinSalary = in.Visa, in.MinSalary
	f.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	f.StrictCountry, f.IncludeUnknown = in.StrictCountry, in.IncludeUnknown
	return f, nil
}

// InputOf converts filters back to their user-facing form, e.g. to show a
// saved preset. ParseFilters(d, InputOf(f)) returns f.
func InputOf(f model.Filters) FilterInput {
	return FilterInput{
		Countries:      f.Countries,
		Modes:          strs(f.Modes),
		Types:          strs(f.Types),
		Areas:          strs(f.Areas),
		Experience:     strs(f.Experience),
		Keywords:       f.Keywords,
		Exclude:        f.Exclude,
		PostedWithin:   FormatAge(f.PostedWithin),
		Visa:           f.VisaOnly,
		MinSalary:      f.MinSalary,
		Currency:       f.Currency,
		StrictCountry:  f.StrictCountry,
		IncludeUnknown: f.IncludeUnknown,
	}
}

// ParseAge accepts Go durations plus a "d" (days) suffix: 24h, 3d, 7d, 30d.
func ParseAge(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if before, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(before)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("bad age %q (use 24h, 3d, 7d, 30d)", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("bad age %q (use 24h, 3d, 7d, 30d)", s)
	}
	return d, nil
}

// FormatAge is the inverse of ParseAge: whole days print as "7d".
func FormatAge(d time.Duration) string {
	switch {
	case d <= 0:
		return ""
	case d%(24*time.Hour) == 0:
		return strconv.Itoa(int(d/(24*time.Hour))) + "d"
	default:
		return d.String()
	}
}

func parseAll[T any](xs []string, parse func(string) (T, error)) ([]T, error) {
	var out []T
	for _, x := range xs {
		v, err := parse(x)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func strs[T ~string](xs []T) []string {
	var out []string
	for _, x := range xs {
		out = append(out, string(x))
	}
	return out
}

// orDefault returns v, or def when v is empty, or nothing when v is
// just Any.
func orDefault(v, def []string) ([]string, error) {
	if slices.ContainsFunc(v, func(s string) bool { return strings.EqualFold(strings.TrimSpace(s), Any) }) {
		if len(v) > 1 {
			return nil, fmt.Errorf("%q can't be combined with other values", Any)
		}
		return nil, nil
	}
	if len(v) > 0 {
		return v, nil
	}
	return def, nil
}

func parseDefault[T any](v, def []string, parse func(string) (T, error)) ([]T, error) {
	vals, err := orDefault(v, def)
	if err != nil {
		return nil, err
	}
	return parseAll(vals, parse)
}

func trimAll(xs []string) []string {
	var out []string
	for _, x := range xs {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}
