package model

import (
	"cmp"
	"regexp"
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

// spelling is one way of writing a city, as padded words (" navi mumbai ")
// or squashed (" navimumbai ").
type spelling struct {
	text, city string
}

// spellings lists every known city spelling, longest first, so "Navi
// Mumbai" is read as itself before "Mumbai" can match inside it.
var spellings = func() []spelling {
	var out []spelling
	for name, c := range cities {
		out = append(out, spelling{" " + words(name) + " ", c[0]})
		if sq := " " + squash(name) + " "; sq != out[len(out)-1].text {
			out = append(out, spelling{sq, c[0]})
		}
	}
	sortLongest(out, func(s spelling) string { return s.text })
	return out
}()

// CityMatcher tells whether a job is in one of a set of cities, under any
// of their spellings.
type CityMatcher struct {
	want  map[string]bool // canonical names of known cities
	other []string        // cities jobhunt doesn't know, as padded words
	broad []string        // place words a remote job may name: see broadOnly
}

// NewCityMatcher matches the given cities; nil when there are none.
func NewCityMatcher(want []string) *CityMatcher {
	m := CityMatcher{want: map[string]bool{}}
	broad := slices.Clone(broadWords)
	for _, w := range want {
		canon := CanonicalCity(w)
		switch _, known := cityIndex[squash(canon)]; {
		case canon == "":
		case known:
			m.want[canon] = true
			broad = append(broad, cityStates[canon]...)
		default:
			for _, s := range []string{words(canon), squash(canon)} {
				if s = " " + s + " "; !slices.Contains(m.other, s) {
					m.other = append(m.other, s)
				}
			}
		}
	}
	if len(m.want)+len(m.other) == 0 {
		return nil
	}
	for i, b := range broad {
		broad[i] = " " + words(b) + " "
	}
	sortLongest(broad, func(s string) string { return s })
	m.broad = slices.Compact(broad)
	return &m
}

// In reports whether text (a job's city and location) names one of the
// cities, e.g. "Bengaluru, Karnataka" or "Pune / Bangalore". Each place
// is read whole: "Navi Mumbai" isn't Mumbai.
func (m *CityMatcher) In(text string) bool {
	t := " " + words(text) + " "
	// A city jobhunt doesn't know is matched once the known cities it
	// isn't part of are blanked out: "South San Francisco" keeps the San
	// Francisco inside it, but York doesn't match "New York".
	for _, o := range m.other {
		rest := t
		for _, s := range spellings {
			if !strings.Contains(o, s.text) {
				rest = blank(rest, s.text)
			}
		}
		if strings.Contains(rest, o) {
			return true
		}
	}
	for _, s := range spellings {
		if strings.Contains(t, s.text) {
			if m.want[s.city] {
				return true
			}
			t = blank(t, s.text)
		}
	}
	return false
}

// Allows reports whether j passes a city filter. A job in one of the
// cities passes. Unless strict, so does a remote job whose location names
// only a country, a region, or the state of one of the cities ("India",
// "Remote", "Kerala, India" for Kochi), since you can do it from there. A
// remote job that names another place ("Sitamarhi", "Karnataka" for Kochi)
// doesn't, nor does one whose work mode is unknown.
func (m *CityMatcher) Allows(j *Job, strict bool) bool {
	if m.In(j.City + " " + j.Location) {
		return true
	}
	if strict || j.WorkMode != ModeRemote || !m.broadOnly(j.City) {
		return false
	}
	if _, city := ParseLocation(j.Location); city != "" {
		return false
	}
	// HN puts its whole "Company | Role | Location | …" header in
	// Location. Past company and role, one part must be broad and every
	// other one broad or plainly not a place ("$120k", "Full-time"), so
	// "… | Remote (India) | Sitamarhi" still names another town.
	parts := strings.Split(j.Location, "|")
	if len(parts) >= 3 {
		parts = parts[2:]
	}
	ok := false
	for _, p := range parts {
		switch {
		case m.broadOnly(p):
			ok = true
		case !notPlace(p):
			return false
		}
	}
	return ok
}

// notPlace reports whether a header part is job details, not a place: a
// link or email address, or words that are all numbers ("120k", "2027")
// or ones like "full-time", "visa" or "equity".
func notPlace(part string) bool {
	// Drop links and email addresses, then check what's left: "Sitamarhi
	// https://acme.io" still names a town.
	var rest []string
	links := false
	for f := range strings.FieldsSeq(part) {
		if strings.Contains(f, "://") || strings.HasPrefix(strings.ToLower(f), "www.") || strings.Contains(f, "@") || domainRe.MatchString(f) {
			links = true
			continue
		}
		rest = append(rest, f)
	}
	ws := strings.Fields(words(strings.Join(rest, " ")))
	for _, w := range ws {
		if !strings.ContainsAny(w, "0123456789") && !slices.Contains(detailWords, w) {
			return false
		}
	}
	return links || len(ws) > 0
}

// domainRe matches a bare domain or URL like "acme.io" or "acme.com/jobs".
var domainRe = regexp.MustCompile(`^[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}(/\S*)?$`)

var detailWords = []string{
	"full", "part", "time", "fulltime", "parttime", "intern", "interns", "internship", "internships", "contract",
	"contractor", "freelance", "permanent", "visa", "sponsorship", "equity", "salary", "usd", "inr", "eur", "gbp",
	"lpa", "per", "year", "month", "hour", "ft", "pt", "new", "grad", "grads", "graduate", "junior", "entry", "level",
	"and", "or", "plus", "a", "to", "k",
}

// broadOnly reports whether loc names no place smaller than a country, a
// region, or the state of one of the cities: every word is one of those or
// a word postings put around them ("Remote - India", "IN; Worldwide").
func (m *CityMatcher) broadOnly(loc string) bool {
	rest := " " + words(loc) + " "
	for _, b := range m.broad {
		rest = blank(rest, b)
	}
	return strings.TrimSpace(rest) == ""
}

// broadWords are country and region names and codes, and words that don't
// name a place.
var broadWords = func() []string {
	out := []string{
		"remote", "remotely", "job", "jobs", "work", "from", "home", "wfh", "anywhere", "only", "based", "first",
		"distributed", "flexible", "fully", "100", "in", "and", "or", "the", "of", "within", "across", "time", "zone", "zones",
		"us", "uk", "uae", "eu", "usa", "gb", "de", "jp", "sg", "nl", "au", "ca", "ae", "ie", "fr",
	}
	for k := range countryNames {
		out = append(out, k)
	}
	for k := range regionNames {
		out = append(out, k)
	}
	return out
}()

// cityStates maps Indian cities to their state (or union territory), so a
// remote job open across Kerala counts for Kochi but not for Bengaluru.
var cityStates = map[string][]string{
	"Bengaluru": {"karnataka"}, "Mysuru": {"karnataka"}, "Mangaluru": {"karnataka"}, "Hubballi": {"karnataka"},
	"Mumbai": {"maharashtra"}, "Navi Mumbai": {"maharashtra"}, "Thane": {"maharashtra"}, "Pune": {"maharashtra"}, "Nagpur": {"maharashtra"},
	"Hyderabad": {"telangana"}, "Visakhapatnam": {"andhra pradesh"}, "Vijayawada": {"andhra pradesh"},
	"Chennai": {"tamil nadu"}, "Coimbatore": {"tamil nadu"}, "Madurai": {"tamil nadu"}, "Tiruchirappalli": {"tamil nadu"},
	"Kochi": {"kerala"}, "Thiruvananthapuram": {"kerala"}, "Kozhikode": {"kerala"}, "Thrissur": {"kerala"}, "Kannur": {"kerala"},
	"Delhi": {"delhi ncr", "ncr"}, "Gurugram": {"haryana", "delhi ncr", "ncr"}, "Noida": {"uttar pradesh", "delhi ncr", "ncr"},
	"Lucknow": {"uttar pradesh"}, "Kolkata": {"west bengal"}, "Ahmedabad": {"gujarat"}, "Vadodara": {"gujarat"}, "Surat": {"gujarat"},
	"Jaipur": {"rajasthan"}, "Chandigarh": {"punjab", "haryana"}, "Indore": {"madhya pradesh"}, "Bhopal": {"madhya pradesh"},
	"Bhubaneswar": {"odisha"}, "Patna": {"bihar"}, "Puducherry": {"puducherry", "pondicherry"},
}

// blank removes every padded word sequence p from padded text t. Matches
// share their padding ("navi mumbai navi mumbai"), so one ReplaceAll pass
// can miss every other one.
func blank(t, p string) string {
	for strings.Contains(t, p) {
		t = strings.ReplaceAll(t, p, " ")
	}
	return t
}

// sortLongest sorts xs by key length, longest first, then by key.
func sortLongest[T any](xs []T, key func(T) string) {
	slices.SortFunc(xs, func(a, b T) int {
		ka, kb := key(a), key(b)
		return cmp.Or(len(kb)-len(ka), strings.Compare(ka, kb))
	})
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
