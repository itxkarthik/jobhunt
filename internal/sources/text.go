package sources

import (
	"html"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	tagRe      = regexp.MustCompile(`(?s)<[^>]*>`)
	blockTagRe = regexp.MustCompile(`(?i)<(br|/p|/li|/div|/h[1-6]|p|li)\b[^>]*>`)
	spaceRe    = regexp.MustCompile(`[ \t\f\r]+`)
	blankRe    = regexp.MustCompile(`\n\s*\n\s*\n+`)
)

// StripHTML converts an HTML fragment to plain text, keeping paragraph breaks.
func StripHTML(s string) string {
	if s == "" {
		return ""
	}
	// Some APIs double-escape their HTML (&lt;p&gt;).
	if strings.Contains(s, "&lt;") && !strings.Contains(s, "<") {
		s = html.UnescapeString(s)
	}
	s = blockTagRe.ReplaceAllString(s, "\n")
	s = tagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, " ", " ")
	s = spaceRe.ReplaceAllString(s, " ")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	s = strings.Join(lines, "\n")
	s = blankRe.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// CleanText strips HTML and collapses all whitespace to single spaces; used
// for titles and company names.
func CleanText(s string) string {
	s = StripHTML(s)
	return strings.Join(strings.Fields(s), " ")
}

// ParseTime tries the timestamp layouts seen across sources.
func ParseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range []string{
		time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05",
		time.RFC1123Z, time.RFC1123, "2006-01-02",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// redact hides credential query params in URLs used in error messages.
func redact(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	changed := false
	for k := range q {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "key") || strings.Contains(lk, "token") || lk == "app_id" {
			q.Set(k, "REDACTED")
			changed = true
		}
	}
	if changed {
		u.RawQuery = q.Encode()
	}
	return u.String()
}

// Float returns a pointer to v, or nil when v is zero.
func Float(v float64) *float64 {
	if v == 0 {
		return nil
	}
	return &v
}
