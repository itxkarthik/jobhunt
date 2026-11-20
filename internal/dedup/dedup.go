// Package dedup merges the same posting seen on several sources (a company's
// Greenhouse board, Himalayas, HN, …) into one record.
package dedup

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"slices"
	"strings"

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

// Key is a stable identity for a posting: company + title + primary country.
func Key(j *model.Job) string {
	country := ""
	if len(j.Countries) > 0 {
		country = j.Countries[0]
	}
	sum := sha256.Sum256([]byte(NormCompany(j.Company) + "|" + NormTitle(j.Title) + "|" + country + "|" + strings.ToLower(j.City)))
	return hex.EncodeToString(sum[:6])
}

// tokenSet is a normalized title's distinct words, sorted.
func tokenSet(title string) []string {
	ts := strings.Fields(title)
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
// listing every source it came from. Input order is preserved for the
// surviving records.
func Merge(jobs []model.Job) []model.Job {
	type entry struct {
		job  model.Job
		toks []string // tokenSet of the normalized title
	}
	byCompany := map[string][]int{} // normalized company -> indices into out
	var out []entry
	for _, j := range jobs {
		if j.Key == "" {
			j.Key = Key(&j)
		}
		if len(j.Sources) == 0 {
			j.Sources = []string{j.Source}
		}
		co, toks := NormCompany(j.Company), tokenSet(NormTitle(j.Title))
		merged := false
		if co != "" {
			for _, idx := range byCompany[co] {
				e := &out[idx]
				if mayMatch(len(e.toks), len(toks)) && jaccard(e.toks, toks) >= Threshold && compatible(&e.job, &j) {
					e.job = combine(e.job, j)
					merged = true
					break
				}
			}
		}
		if !merged {
			byCompany[co] = append(byCompany[co], len(out))
			out = append(out, entry{job: j, toks: toks})
		}
	}
	res := make([]model.Job, len(out))
	for i, e := range out {
		res[i] = e.job
	}
	return res
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
