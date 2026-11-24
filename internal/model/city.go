package model

import (
	"slices"
	"strings"
	"unicode"
)

// CanonicalCity returns the name jobhunt uses for a city, whatever the
// spelling or case: "bangalore", "BENGALURU" and "Bengaluru" are all
// "Bengaluru", "Calicut" is "Kozhikode", "NaviMumbai" is "Navi Mumbai". A
// city jobhunt doesn't know comes back trimmed, as typed.
func CanonicalCity(s string) string {
	s = strings.TrimSpace(s)
	if c, ok := cityIndex[squash(s)]; ok {
		return c
	}
	return s
}

// cityIndex maps each spelling, lower-cased with spaces and punctuation
// removed ("navimumbai"), to the city's name.
var cityIndex = func() map[string]string {
	m := make(map[string]string, len(cities))
	for name, c := range cities {
		m[squash(name)] = c[0]
	}
	return m
}()

// CityMatcher tells whether a job is in one of a set of cities, under any
// of their spellings.
type CityMatcher struct {
	names []string // " bengaluru ", " bangalore ", …: lower-cased, padded words
}

// NewCityMatcher matches the given cities; nil when there are none.
func NewCityMatcher(want []string) *CityMatcher {
	var m CityMatcher
	for _, w := range want {
		canon := CanonicalCity(w)
		if canon == "" {
			continue
		}
		add := func(n string) {
			if n = " " + n + " "; !slices.Contains(m.names, n) {
				m.names = append(m.names, n)
			}
		}
		add(words(canon))
		add(squash(canon)) // "NaviMumbai" in a posting
		for name, c := range cities {
			if c[0] == canon {
				add(words(name))
				add(squash(name))
			}
		}
	}
	if len(m.names) == 0 {
		return nil
	}
	return &m
}

// In reports whether text (a job's city and location) names one of the
// cities as whole words, e.g. "Bengaluru, Karnataka" or "Pune / Bangalore".
func (m *CityMatcher) In(text string) bool {
	t := " " + words(text) + " "
	for _, n := range m.names {
		if strings.Contains(t, n) {
			return true
		}
	}
	return false
}

// Allows reports whether j passes a city filter. A job in one of the
// cities passes. Unless strict, so does a remote job whose location names
// only a country or region ("India", "Remote", "Worldwide"), since you can
// do it from any of the cities. A remote job that names another place
// ("Sitamarhi", "London") doesn't, nor does one whose mode is unknown.
func (m *CityMatcher) Allows(j *Job, strict bool) bool {
	if m.In(j.City + " " + j.Location) {
		return true
	}
	return !strict && j.WorkMode == ModeRemote && j.City == "" && broadOnly(j.Location)
}

// broadOnly reports whether loc names no place smaller than a country or a
// region of one: every word is a country, region, Indian state or one of
// the words postings put around them ("Remote - India", "IN; Worldwide").
func broadOnly(loc string) bool {
	rest := " " + words(loc) + " "
	for _, names := range [][]string{broadNames, keys(countryNames), keys(regionNames)} {
		for _, n := range names {
			if n = words(n); n != "" {
				rest = strings.ReplaceAll(rest, " "+n+" ", "  ")
			}
		}
	}
	return strings.TrimSpace(rest) == ""
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// broadNames are words that don't name a place, plus country codes and
// Indian states and union territories.
var broadNames = []string{
	"remote", "remotely", "job", "jobs", "work", "from", "home", "wfh", "anywhere", "only", "based", "first",
	"distributed", "flexible", "fully", "100", "in", "and", "or", "the", "of", "within", "across", "time", "zone", "zones",
	"us", "uk", "uae", "eu", "usa", "gb", "de", "jp", "sg", "nl", "au", "ca", "ae", "ie", "fr",
	"andhra pradesh", "arunachal pradesh", "assam", "bihar", "chhattisgarh", "goa", "gujarat", "haryana",
	"himachal pradesh", "jharkhand", "karnataka", "kerala", "madhya pradesh", "maharashtra", "manipur",
	"meghalaya", "mizoram", "nagaland", "odisha", "punjab", "rajasthan", "sikkim", "tamil nadu", "telangana",
	"tripura", "uttar pradesh", "uttarakhand", "west bengal", "jammu and kashmir", "ladakh", "delhi ncr", "ncr",
}

// squash lower-cases s and drops everything but letters and digits.
func squash(s string) string {
	return strings.ReplaceAll(words(s), " ", "")
}

// words lower-cases s and joins its letter and digit runs with single
// spaces.
func words(s string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}), " ")
}
