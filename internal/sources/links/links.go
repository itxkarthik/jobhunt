// Package links builds pre-filled browser search URLs for sites whose terms
// forbid automated access (LinkedIn, Indeed, Naukri, Internshala, …). These
// URLs are only printed, never fetched.
package links

import (
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources"
	"github.com/itxkarthik/jobhunt/internal/sources/xposts"
)

// Link is one site to open in a browser.
type Link struct {
	Site string
	URL  string
}

// For returns the links relevant to f, in a stable order.
func For(f model.Filters) []Link {
	countries := f.Countries
	if len(countries) == 0 {
		countries = []string{"IN"}
	}
	var out []Link
	add := func(site, u string) {
		if u != "" {
			out = append(out, Link{site, u})
		}
	}
	for _, c := range countries {
		add("LinkedIn"+suffix(countries, c), linkedIn(f, c))
	}
	for _, c := range countries {
		add("Indeed"+suffix(countries, c), indeed(f, c))
	}
	if slices.Contains(countries, "IN") {
		if wantsInternships(f) {
			add("Internshala", internshala(f))
		}
		add("Naukri", naukri(f))
		add("Cutshort", "https://cutshort.io/jobs/"+slug(topicOr(f, "software developer"))+"-jobs")
		add("Instahyre", "https://www.instahyre.com/search-jobs/?q="+url.QueryEscape(topicOr(f, "software engineer")))
	}
	if slices.Contains(countries, "JP") {
		add("TokyoDev", "https://www.tokyodev.com/jobs")
		add("Japan Dev", japanDev(f))
	}
	add("YC startup jobs", ycJobs(countries))
	add("Work at a Startup", "https://www.workatastartup.com/jobs")
	add("Wellfound", wellfound(f, countries[0]))
	add("Glassdoor", glassdoor(f, countries[0]))
	add("Google Jobs", googleJobs(f, countries[0]))
	add("X search", xposts.SearchURL(f))
	return out
}

func suffix(all []string, c string) string {
	if len(all) == 1 {
		return ""
	}
	return " (" + c + ")"
}

// LinkedIn guest search. geoId pins the country; f_E experience
// (1 internship, 2 entry, 3 associate, 4 mid-senior), f_JT type, f_WT
// workplace (1 on-site, 2 remote, 3 hybrid), f_TPR recency in seconds.
func linkedIn(f model.Filters, c string) string {
	q := url.Values{}
	q.Set("keywords", strings.TrimSpace(topicOr(f, "software engineer")))
	geo := map[string]string{"IN": "102713980", "US": "103644278", model.RemoteGlobal: "92000000"}
	if c == model.RemoteGlobal {
		q.Set("location", "Worldwide")
		q.Set("f_WT", "2")
	} else {
		q.Set("location", sources.CountryName(c))
	}
	if g, ok := geo[c]; ok {
		q.Set("geoId", g)
	}
	var exp, jt []string
	for _, t := range f.Types {
		switch t {
		case model.TypeInternship:
			exp, jt = append(exp, "1"), append(jt, "I")
		case model.TypeNewGrad:
			exp = append(exp, "2")
		case model.TypeFullTime:
			jt = append(jt, "F")
		case model.TypePartTime:
			jt = append(jt, "P")
		case model.TypeContract:
			jt = append(jt, "C")
		}
	}
	for _, e := range f.Experience {
		switch e {
		case model.ExpIntern:
			exp = append(exp, "1")
		case model.ExpEntry:
			exp = append(exp, "2")
		case model.ExpMid:
			exp = append(exp, "3", "4")
		case model.ExpSenior:
			exp = append(exp, "4")
		}
	}
	if len(exp) > 0 {
		q.Set("f_E", strings.Join(dedupe(exp), ","))
	}
	if len(jt) > 0 {
		q.Set("f_JT", strings.Join(dedupe(jt), ","))
	}
	if c != model.RemoteGlobal && len(f.Modes) > 0 {
		var wt []string
		for _, m := range f.Modes {
			wt = append(wt, map[model.WorkMode]string{model.ModeOnsite: "1", model.ModeRemote: "2", model.ModeHybrid: "3"}[m])
		}
		q.Set("f_WT", strings.Join(wt, ","))
	}
	if f.PostedWithin > 0 {
		q.Set("f_TPR", fmt.Sprintf("r%d", int(f.PostedWithin/time.Second)))
	}
	return "https://www.linkedin.com/jobs/search/?" + q.Encode()
}

var indeedHosts = map[string]string{
	"IN": "in.indeed.com", "US": "www.indeed.com", "JP": "jp.indeed.com", "GB": "uk.indeed.com",
	"DE": "de.indeed.com", "CA": "ca.indeed.com", "SG": "sg.indeed.com", "NL": "nl.indeed.com",
	"AU": "au.indeed.com", "AE": "ae.indeed.com",
}

func indeed(f model.Filters, c string) string {
	if c == model.RemoteGlobal {
		c = "US"
	}
	h, ok := indeedHosts[c]
	if !ok {
		return ""
	}
	q := url.Values{}
	q.Set("q", sources.Phrase(f))
	if f.OnlyRemote() || slices.Contains(f.Countries, model.RemoteGlobal) {
		q.Set("l", "Remote")
	}
	switch {
	case f.WantsType(model.TypeInternship):
		q.Set("jt", "internship")
	case f.WantsType(model.TypeContract):
		q.Set("jt", "contract")
	case f.WantsType(model.TypePartTime):
		q.Set("jt", "parttime")
	}
	if d := int(f.PostedWithin.Hours() / 24); d > 0 {
		q.Set("fromage", strconv.Itoa(d))
	}
	return "https://" + h + "/jobs?" + q.Encode()
}

func internshala(f model.Filters) string {
	kw := slug(topicOr(f, ""))
	if kw == "" {
		kw = "computer-science"
	}
	if f.OnlyRemote() {
		return "https://internshala.com/internships/work-from-home-" + kw + "-internships/"
	}
	return "https://internshala.com/internships/keywords-" + kw + "/"
}

func naukri(f model.Filters) string {
	phrase := topicOr(f, "software developer")
	if wantsInternships(f) {
		phrase += " internship"
	}
	q := url.Values{}
	q.Set("k", phrase)
	if wantsInternships(f) || f.WantsType(model.TypeNewGrad) {
		q.Set("experience", "0")
	}
	return "https://www.naukri.com/" + slug(phrase) + "-jobs?" + q.Encode()
}

func japanDev(f model.Filters) string {
	if len(f.Areas) == 1 {
		switch f.Areas[0] {
		case model.AreaBackend, model.AreaFrontend, model.AreaFullStack, model.AreaMobile, model.AreaDevOps:
			return "https://japan-dev.com/" + string(f.Areas[0]) + "-jobs-in-japan"
		}
	}
	return "https://japan-dev.com/jobs"
}

func wellfound(f model.Filters, c string) string {
	role := slug(topicOr(f, "software"))
	if !strings.Contains(role, "engineer") && !strings.Contains(role, "developer") {
		role += "-engineer"
	}
	if c == model.RemoteGlobal {
		return "https://wellfound.com/role/r/" + role
	}
	return "https://wellfound.com/role/l/" + role + "/" + slug(sources.CountryName(c))
}

func glassdoor(f model.Filters, c string) string {
	host := "www.glassdoor.com"
	switch c {
	case "IN":
		host = "www.glassdoor.co.in"
	case "GB":
		host = "www.glassdoor.co.uk"
	case "CA":
		host = "www.glassdoor.ca"
	case "DE":
		host = "www.glassdoor.de"
	case "AU":
		host = "www.glassdoor.com.au"
	}
	return "https://" + host + "/Job/jobs.htm?sc.keyword=" + url.QueryEscape(sources.Phrase(f))
}

func googleJobs(f model.Filters, c string) string {
	q := sources.Phrase(f) + " jobs"
	if c == model.RemoteGlobal {
		q += " remote"
	} else {
		q += " " + sources.CountryName(c)
	}
	return "https://www.google.com/search?ibp=htl;jobs&q=" + url.QueryEscape(q)
}

func topicOr(f model.Filters, def string) string {
	if t := sources.Topic(f); t != "" {
		return t
	}
	return def
}

func wantsInternships(f model.Filters) bool {
	return f.WantsType(model.TypeInternship) || (len(f.Types) == 0 && len(f.Experience) == 0)
}

func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	dash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

func dedupe(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// ycJobs links Y Combinator's job board (no API; browse only). It has
// pages per location for India and remote; other countries get the
// global list.
func ycJobs(countries []string) string {
	base := "https://www.ycombinator.com/jobs/role/software-engineer"
	switch {
	case slices.Contains(countries, "IN"):
		return base + "/india"
	case slices.Contains(countries, model.RemoteGlobal):
		return base + "/remote"
	}
	return base
}
