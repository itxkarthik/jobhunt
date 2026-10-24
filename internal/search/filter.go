package search

import (
	"regexp"
	"strings"
	"time"

	"github.com/itxkarthik/jobhunt/internal/classify"
	"github.com/itxkarthik/jobhunt/internal/model"
)

// Filter returns the jobs matching f. Jobs whose relevant field is unknown
// are dropped unless f.IncludeUnknown is set.
func Filter(jobs []model.Job, f model.Filters, c *classify.Classifier, now time.Time) []model.Job {
	kw := compileTerms(f.Keywords)
	ex := compileTerms(f.Exclude)
	out := jobs[:0:0]
	for i := range jobs {
		j := &jobs[i]
		if matches(j, f, c, kw, ex, now) {
			out = append(out, *j)
		}
	}
	return out
}

func matches(j *model.Job, f model.Filters, c *classify.Classifier, kw, ex []*regexp.Regexp, now time.Time) bool {
	unk := f.IncludeUnknown

	if len(f.Countries) > 0 {
		if len(j.Countries) == 0 {
			if !unk {
				return false
			}
		} else {
			ok := false
			remote := j.WorkMode == model.ModeRemote
			for _, c := range f.Countries {
				if c == model.RemoteGlobal {
					ok = ok || (remote && contains(j.Countries, model.RemoteGlobal))
					continue
				}
				ok = ok || model.CountryAllows(j.Countries, c, remote, f.StrictCountry)
			}
			if !ok {
				return false
			}
		}
	}

	if len(f.Modes) > 0 && !f.WantsMode(j.WorkMode) && !(unk && j.WorkMode == model.ModeUnknown) {
		return false
	}

	if len(f.Types) > 0 && !f.WantsType(j.JobType) {
		// new-grad searches also want entry-level full-time roles.
		entryFT := f.WantsType(model.TypeNewGrad) && j.JobType == model.TypeFullTime && j.Experience == model.ExpEntry
		if !entryFT && !(unk && j.JobType == model.TypeUnknown) {
			return false
		}
	}

	if len(f.Experience) > 0 {
		ok := false
		for _, e := range f.Experience {
			ok = ok || j.Experience == e
		}
		if !ok && !(unk && j.Experience == model.ExpUnknown) {
			return false
		}
	}

	if len(f.Areas) > 0 {
		ok := false
		for _, a := range f.Areas {
			ok = ok || c.MatchesArea(j, a)
		}
		if !ok && !(unk && len(j.Areas) == 0) {
			return false
		}
	}

	text := strings.ToLower(j.Title + "\n" + j.Description + "\n" + strings.Join(j.Tags, " "))
	if len(kw) > 0 && !anyMatch(kw, text) {
		return false
	}
	if len(ex) > 0 && anyMatch(ex, strings.ToLower(j.Title+"\n"+j.Description)) {
		return false
	}

	if f.PostedWithin > 0 && !j.PostedAt.IsZero() && now.Sub(j.PostedAt) > f.PostedWithin {
		return false
	}

	if f.VisaOnly && (j.VisaSponsorship == nil || !*j.VisaSponsorship) {
		return false
	}

	if f.MinSalary > 0 {
		max := j.SalaryMax
		if max == nil {
			max = j.SalaryMin
		}
		if max == nil {
			return unk
		}
		if f.Currency != "" && j.Currency != "" && !strings.EqualFold(f.Currency, j.Currency) {
			return unk // no FX conversion; don't compare across currencies
		}
		if *max < f.MinSalary {
			return false
		}
	}
	return true
}

func compileTerms(terms []string) []*regexp.Regexp {
	var out []*regexp.Regexp
	for _, t := range terms {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" {
			continue
		}
		out = append(out, regexp.MustCompile(`(?:^|[^a-z0-9+#])`+regexp.QuoteMeta(t)+`(?:$|[^a-z0-9+#])`))
	}
	return out
}

func anyMatch(res []*regexp.Regexp, s string) bool {
	for _, re := range res {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
