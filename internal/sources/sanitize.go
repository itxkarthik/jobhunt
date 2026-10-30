package sources

import (
	"net/url"
	"strings"
	"unicode"

	"github.com/itxkarthik/jobhunt/internal/model"
)

// SanitizeJob makes a job from an untrusted source safe to print and export:
// it removes terminal control sequences (ESC, other C0/C1 controls) and
// bidi-override characters from all text, flattens single-line fields, and
// drops URLs that aren't http(s).
func SanitizeJob(j *model.Job) {
	for _, p := range []*string{&j.Title, &j.Company, &j.City, &j.Location, &j.Currency} {
		*p = SafeText(*p, false)
	}
	j.Description = SafeText(j.Description, true)
	for i := range j.Tags {
		j.Tags[i] = SafeText(j.Tags[i], false)
	}
	j.URL = SafeURL(j.URL)
}

// SafeText drops control and bidi-override runes. With multiline, newlines
// and tabs survive; otherwise they become spaces.
func SafeText(s string, multiline bool) string {
	clean := true
	for _, r := range s {
		if unsafeRune(r) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			if multiline {
				b.WriteRune(r)
			} else {
				b.WriteByte(' ')
			}
		case unsafeRune(r):
		default:
			b.WriteRune(r)
		}
	}
	if multiline {
		return b.String()
	}
	return strings.TrimSpace(b.String())
}

func unsafeRune(r rune) bool {
	switch {
	case unicode.IsControl(r): // C0 (incl. ESC, \n, \t) and C1 controls
		return true
	case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069: // bidi overrides/isolates
		return true
	}
	return false
}

// SafeURL returns u if it is an absolute http(s) URL without control
// characters, else "".
func SafeURL(u string) string {
	u = strings.TrimSpace(u)
	if u == "" || strings.IndexFunc(u, unsafeRune) >= 0 {
		return ""
	}
	p, err := url.Parse(u)
	if err != nil || (p.Scheme != "http" && p.Scheme != "https") || p.Host == "" {
		return ""
	}
	return u
}
