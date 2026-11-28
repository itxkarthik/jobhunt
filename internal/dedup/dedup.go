// Package dedup merges the same posting seen on several sources (a company's
// Greenhouse board, Himalayas, HN, …) into one record.
package dedup

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/itxkarthik/jobhunt/internal/classify"
	"github.com/itxkarthik/jobhunt/internal/model"
)

var (
	parenRe   = regexp.MustCompile(`\([^)]*\)|\[[^\]]*\]`)
	companyRe = regexp.MustCompile(`\b(inc|llc|ltd|limited|pvt|private|corp|corporation|co|gmbh|ag|bv|plc|technologies|technology|tech|software|solutions|labs|group|holdings|india|global)\b`)
)

var titleSynonyms = map[string]string{
	"sr": "senior", "jr": "junior", "swe": "software engineer", "sde": "software engineer",
	"eng": "engineer", "dev": "developer", "internship": "intern", "interns": "intern",
	"backend": "back end", "frontend": "front end", "fullstack": "full stack",
}

// NormCompany lowercases and strips legal suffixes: "Razorpay Software Pvt Ltd" -> "razorpay".
func NormCompany(s string) string {
	s = strings.ToLower(s)
	s = parenRe.ReplaceAllString(s, " ")
	s = companyRe.ReplaceAllString(strings.Join(classify.Tokenize(s), " "), " ")
	return strings.Join(strings.Fields(s), " ")
}

// NormTitle removes parentheticals ("(Remote)", "[2027]") and expands abbreviations.
func NormTitle(s string) string {
	s = parenRe.ReplaceAllString(strings.ToLower(s), " ")
	var out []string
	for _, t := range classify.Tokenize(s) {
		if syn, ok := titleSynonyms[t]; ok {
			out = append(out, strings.Fields(syn)...)
		} else {
			out = append(out, t)
		}
	}
	return strings.Join(out, " ")
}

// titleForms maps word forms that name the same role to one form, so
// "Backend Engineering Intern" matches "Backend Engineer Intern". Level
// markers keep their number: "SDE I" and "SDE 2" are different jobs.
var titleForms = map[string]string{
	"engineering": "engineer", "engineers": "engineer",
	"development": "developer", "developers": "developer",
	"i": "1", "l1": "1", "ii": "2", "l2": "2", "iii": "3", "l3": "3",
}

// titleFiller are words that never tell two roles apart.
var titleFiller = map[string]bool{"the": true, "and": true, "for": true, "of": true, "a": true, "an": true}

// devFields are words after which "development" is a field of its own
// ("business development", "research and development"), not software.
var devFields = map[string]bool{"business": true, "research": true}

// matchTokens is the set of words Merge compares titles by: NormTitle's,
// with word forms merged and filler dropped. Key keeps using NormTitle
// alone, since stored tracker and hidden keys depend on it.
func matchTokens(title string) []string {
	toks := strings.Fields(NormTitle(title))
	out := toks[:0]
	prev := ""
	for _, t := range toks {
		if titleFiller[t] || !strings.ContainsFunc(t, isAlnum) {
			continue
		}
		w := t
		if f, ok := titleForms[t]; ok && !(t == "development" && devFields[prev]) {
			w = f
		}
		prev = t
		out = append(out, w)
	}
	return tokenSet(out)
}

func isAlnum(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// Key is a stable identity for a posting: company + title + primary country.
func Key(j *model.Job) string {
	country := ""
	if len(j.Countries) > 0 {
		country = j.Countries[0]
	}
	sum := sha256.Sum256([]byte(NormCompany(j.Company) + "|" + NormTitle(j.Title) + "|" + country + "|" + strings.ToLower(j.City)))
	return hex.EncodeToString(sum[:6])
}

// tokenSet is the distinct words, sorted.
func tokenSet(ts []string) []string {
	slices.Sort(ts)
	return slices.Compact(ts)
}

// jaccard is |a ∩ b| / |a ∪ b| for sorted sets.
func jaccard(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for i, j := 0, 0; i < len(a) && j < len(b); {
		switch {
		case a[i] == b[j]:
			inter++
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return float64(inter) / float64(len(a)+len(b)-inter)
}

// mayMatch is a cheap bound: the Jaccard similarity of sets of sizes m and
// n is at most min/max, so sizes that far apart can't reach Threshold.
func mayMatch(m, n int) bool {
	return float64(min(m, n)) >= Threshold*float64(max(m, n))
}

// Threshold is the title similarity at which two postings at the same company
// with compatible locations are considered one.
const Threshold = 0.85

// Merge deduplicates jobs, keeping the richest record of each group and
// listing every source it came from. Two jobs are one when they link to
// the same posting (see postingURLs), or are at the same company with
// similar titles and compatible locations. Input order is preserved for
// the surviving records.
func Merge(jobs []model.Job) []model.Job {
	type entry struct {
		job  model.Job
		toks []string // matchTokens of the title
	}
	byCompany := map[string][]int{} // normalized company -> indices into out
	byURL := map[string]int{}       // postingURLs -> index into out
	urls := postingURLs(jobs)
	var out []entry
	for i, j := range jobs {
		if j.Key == "" {
			j.Key = Key(&j)
		}
		if len(j.Sources) == 0 {
			j.Sources = []string{j.Source}
		}
		co, toks := NormCompany(j.Company), matchTokens(j.Title)
		at := -1
		if idx, ok := byURL[urls[i]]; ok && urls[i] != "" {
			at = idx
		} else if co != "" {
			for _, idx := range byCompany[co] {
				e := &out[idx]
				if mayMatch(len(e.toks), len(toks)) && jaccard(e.toks, toks) >= Threshold && compatible(&e.job, &j) {
					at = idx
					break
				}
			}
		}
		if at >= 0 {
			out[at].job = combine(out[at].job, j)
		} else {
			at = len(out)
			byCompany[co] = append(byCompany[co], at)
			out = append(out, entry{job: j, toks: toks})
		}
		if _, ok := byURL[urls[i]]; !ok && urls[i] != "" {
			byURL[urls[i]] = at
		}
	}
	res := make([]model.Job, len(out))
	for i, e := range out {
		res[i] = e.job
	}
	return res
}

// trackingParams are query parameters that say where a click came from,
// not which posting it is.
var trackingParams = []string{"ref", "refid", "ref_src", "referrer", "src", "source", "trk", "trackingid",
	"gh_src", "lever-source", "lever-origin", "lever-via", "fbclid", "gclid"}

// postingURLs returns each job's URL normalized for matching, or "" when
// it can't name one posting: a site's front page or a one-segment path
// like /careers, or a URL some source gives for several of its jobs (a
// careers page every listing links to). Such a URL is dropped for every
// source, since it doesn't tell their jobs apart either.
func postingURLs(jobs []model.Job) []string {
	out := make([]string, len(jobs))
	perSource := map[[2]string]int{}
	for i := range jobs {
		out[i] = postingURL(jobs[i].URL)
		if out[i] != "" {
			perSource[[2]string{jobs[i].Source, out[i]}]++
		}
	}
	shared := map[string]bool{}
	for k, n := range perSource {
		if n > 1 {
			shared[k[1]] = true
		}
	}
	for i := range out {
		if shared[out[i]] {
			out[i] = ""
		}
	}
	return out
}

// postingURL lowercases the host and drops "www.", the fragment, a
// trailing slash or /apply, and tracking parameters.
func postingURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	if host == "job-boards.greenhouse.io" {
		host = "boards.greenhouse.io"
	}
	path := strings.TrimSuffix(strings.TrimRight(u.EscapedPath(), "/"), "/apply")
	q := u.Query()
	for k := range q {
		if lk := strings.ToLower(k); strings.HasPrefix(lk, "utm_") || slices.Contains(trackingParams, lk) {
			q.Del(k)
		}
	}
	if strings.Count(path, "/") < 2 && len(q) == 0 {
		return ""
	}
	return host + path + "?" + q.Encode()
}

// compatible: same city, or overlapping countries, or one side unknown.
func compatible(a, b *model.Job) bool {
	if a.City != "" && b.City != "" {
		return strings.EqualFold(a.City, b.City)
	}
	if len(a.Countries) == 0 || len(b.Countries) == 0 {
		return true
	}
	for _, x := range a.Countries {
		if slices.Contains(b.Countries, x) {
			return true
		}
	}
	return false
}

// richness scores how complete a record is.
func richness(j *model.Job) int {
	n := min(len(j.Description), 3000)
	if j.SalaryMin != nil || j.SalaryMax != nil {
		n += 500
	}
	if j.City != "" {
		n += 100
	}
	if !j.PostedAt.IsZero() {
		n += 100
	}
	for _, unknown := range []bool{j.WorkMode == model.ModeUnknown, j.JobType == model.TypeUnknown, j.Experience == model.ExpUnknown} {
		if !unknown {
			n += 100
		}
	}
	return n
}

func combine(a, b model.Job) model.Job {
	keep, other := a, b
	if richness(&b) > richness(&a) {
		keep, other = b, a
	}
	keep.Key = a.Key // first-seen key stays stable for the tracker
	keep.Sources = union(a.Sources, b.Sources)
	keep.Tags = union(keep.Tags, other.Tags)
	keep.Countries = union(keep.Countries, other.Countries)
	if keep.PostedAt.IsZero() || (!other.PostedAt.IsZero() && other.PostedAt.Before(keep.PostedAt)) {
		keep.PostedAt = other.PostedAt // earliest posting date
	}
	if !keep.HasSalary() && other.HasSalary() {
		keep.SalaryMin, keep.SalaryMax, keep.Currency, keep.PayPeriod = other.SalaryMin, other.SalaryMax, other.Currency, other.PayPeriod
	}
	if keep.Paid == nil {
		keep.Paid = other.Paid
	}
	if keep.City == "" {
		keep.City = other.City
	}
	if keep.Location == "" {
		keep.Location = other.Location
	}
	if keep.WorkMode == model.ModeUnknown || keep.WorkMode == "" {
		keep.WorkMode = other.WorkMode
	}
	if keep.JobType == model.TypeUnknown || keep.JobType == "" {
		keep.JobType = other.JobType
	}
	if len(keep.Areas) == 0 {
		keep.Areas = other.Areas
	}
	if keep.VisaSponsorship == nil {
		keep.VisaSponsorship = other.VisaSponsorship
	}
	return keep
}

func union(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range append(append([]string(nil), a...), b...) {
		if x != "" && !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
