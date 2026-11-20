package search

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources"
	"github.com/itxkarthik/jobhunt/internal/store"
)

// Every fetched job is cleaned (sources.SanitizeJob) and classified once,
// when it's fetched; the fetch cache keeps the cleaned job and what the
// classifier decided, so a cached search does neither again. The cached
// job is stored as it was before classification, because the classifier
// only fills fields a source left empty: when its rules change, the jobs
// are classified again from that copy.

// sanitizeVersion changes whenever sources.SanitizeJob does, so cached
// jobs cleaned the old way are cleaned again.
const sanitizeVersion = "1"

// classResult is what Classify set on one job.
type classResult struct {
	Type  model.JobType    `json:"t,omitempty"`
	Exp   model.Experience `json:"e,omitempty"`
	Mode  model.WorkMode   `json:"m,omitempty"`
	Visa  *bool            `json:"v,omitempty"`
	Areas []model.Area     `json:"a,omitempty"`
	Tags  []string         `json:"g,omitempty"` // tags Classify added
}

func (e *Engine) prepVersion() string {
	return sanitizeVersion + "." + e.Classifier.Version()
}

// prepare cleans and classifies jobs in place. It returns the cleaned jobs
// as they were before classification, and the classification, both for
// the cache.
func (e *Engine) prepare(jobs []model.Job) (raw []model.Job, class []byte) {
	for i := range jobs {
		sources.SanitizeJob(&jobs[i])
	}
	// A shallow copy is enough: Classify replaces fields and appends tags,
	// which leaves the copy's own view of each slice unchanged.
	raw = slices.Clone(jobs)
	classifyAll(e.Classifier, jobs)
	res := make([]classResult, len(jobs))
	for i := range jobs {
		j := &jobs[i]
		res[i] = classResult{Type: j.JobType, Exp: j.Experience, Mode: j.WorkMode, Visa: j.VisaSponsorship,
			Areas: j.Areas, Tags: j.Tags[len(raw[i].Tags):]}
	}
	class, _ = json.Marshal(res) // can't fail: plain strings, bools and slices
	return raw, class
}

// fromCache returns a cache entry's jobs ready to filter. They're
// classified from the entry when it was stored by the current classifier,
// and otherwise prepared again, with the entry updated to match.
func (e *Engine) fromCache(ctx context.Context, source, key string, c store.Cached) []model.Job {
	ver := e.prepVersion()
	if c.ClassVersion == ver && applyClass(c.Jobs, c.Class) {
		return c.Jobs
	}
	raw, class := e.prepare(c.Jobs)
	_ = e.Store.RedoCache(ctx, source, key, raw, ver, class) // if it fails, the next search redoes it
	return c.Jobs
}

// applyClass sets each job's classification from class, and reports false
// if class doesn't fit jobs.
func applyClass(jobs []model.Job, class []byte) bool {
	var res []classResult
	if json.Unmarshal(class, &res) != nil || len(res) != len(jobs) {
		return false
	}
	for i, r := range res {
		j := &jobs[i]
		j.JobType, j.Experience, j.WorkMode, j.VisaSponsorship = r.Type, r.Exp, r.Mode, r.Visa
		if len(r.Areas) > 0 {
			j.Areas = r.Areas
		}
		j.Tags = append(j.Tags, r.Tags...)
	}
	return true
}
