// Package search fans a query out to every source concurrently, applies
// caching and health tracking, then classifies and filters the results.
package search

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"sort"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/itxkarthik/jobhunt/internal/classify"
	"github.com/itxkarthik/jobhunt/internal/dedup"
	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/rank"
	"github.com/itxkarthik/jobhunt/internal/sources"
	"github.com/itxkarthik/jobhunt/internal/store"
)

type Status string

const (
	StatusOK      Status = "ok"
	StatusCached  Status = "cached"
	StatusSkipped Status = "skipped"
	StatusError   Status = "error"
)

// Report describes what happened with one source during a search.
type Report struct {
	Source   string
	Status   Status
	Count    int // jobs returned by the source before filtering
	Err      string
	Duration time.Duration
	Link     string // browser fallback URL, if the source has one
}

type Result struct {
	Jobs    []model.Job
	Reports []Report
	Total   int       // jobs fetched across sources before filtering
	RunAt   time.Time // when results were processed; new jobs' FirstSeenAt
}

type Engine struct {
	Sources     []sources.Source
	Store       *store.Store // may be nil (no cache/health)
	Classifier  *classify.Classifier
	CacheTTL    time.Duration
	SourceTTL   map[string]time.Duration // per-source overrides from config
	Timeout     time.Duration
	Concurrency int
	Refresh     bool // bypass cache
	Weights     rank.Weights
	SortBy      string // "score" (default) or "date"
	ShowHidden  bool   // keep jobs the user hid
	NewOnly     bool   // keep only jobs never seen in an earlier run
	NoMarkSeen  bool   // don't record results as seen (dry runs)
	// Prepare, if set, runs before each search, e.g. to reload boards
	// added since the engine was built. An error is not fatal: the search
	// runs with what it has.
	Prepare func(context.Context) error
	// Progress, if set, is called as each source finishes.
	Progress func(Report)
}

// Run executes the search. It only returns an error for problems that make
// the whole search impossible; individual source failures go in Reports.
func (e *Engine) Run(ctx context.Context, f model.Filters) (*Result, error) {
	if e.Prepare != nil {
		_ = e.Prepare(ctx) // a stale board list is better than no search
	}
	var (
		mu      sync.Mutex
		all     []model.Job
		reports = make([]Report, len(e.Sources))
	)
	g, gctx := errgroup.WithContext(ctx)
	if e.Concurrency > 0 {
		g.SetLimit(e.Concurrency)
	}
	for i, src := range e.Sources {
		g.Go(func() error {
			r, jobs := e.runOne(gctx, src, f)
			mu.Lock()
			reports[i] = r
			all = append(all, jobs...)
			mu.Unlock()
			if e.Progress != nil {
				e.Progress(r)
			}
			return nil // never fail the group: one source must not break the search
		})
	}
	_ = g.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	res := &Result{Reports: reports, Total: len(all)}
	// Sources are untrusted: strip terminal escapes and non-http(s) URLs
	// before anything is printed, exported, or stored.
	for i := range all {
		sources.SanitizeJob(&all[i])
	}
	classifyAll(e.Classifier, all)
	now := time.Now()
	res.RunAt = now
	res.Jobs = dedup.Merge(Filter(all, f, e.Classifier, now))
	rk := &rank.Ranker{W: e.Weights, Classifier: e.Classifier, Now: now}
	kw := classify.NewTermSet(f.Keywords)
	for i := range res.Jobs {
		res.Jobs[i].Score, res.Jobs[i].ScoreParts = rk.Explain(&res.Jobs[i], f, kw)
	}
	Sort(res.Jobs, e.SortBy)
	if f.WantsCompanies() {
		if e.Store == nil {
			return nil, errors.New("company filters need the database")
		}
		if err := e.byCompany(ctx, res, f); err != nil {
			return nil, err
		}
	}
	if e.Store != nil {
		if err := e.track(ctx, res, now); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// byCompany keeps jobs at companies on the companies list whose stage and
// headcount pass f.
func (e *Engine) byCompany(ctx context.Context, res *Result, f model.Filters) error {
	cs, err := e.Store.Companies(ctx)
	if err != nil {
		return err
	}
	ok := map[string]bool{}
	for _, c := range cs {
		if (len(f.CompanyStages) == 0 || slices.Contains(f.CompanyStages, c.Stage)) &&
			(len(f.CompanyHeadcounts) == 0 || slices.Contains(f.CompanyHeadcounts, c.Headcount)) {
			ok[c.Key] = true
		}
	}
	res.Jobs = keep(res.Jobs, func(j *model.Job) bool { return ok[dedup.NormCompany(j.Company)] })
	return nil
}

// track drops hidden jobs, records the rest as seen, and marks new ones.
func (e *Engine) track(ctx context.Context, res *Result, now time.Time) error {
	if !e.ShowHidden {
		hidden, err := e.Store.HiddenKeys(ctx)
		if err != nil {
			return err
		}
		res.Jobs = keep(res.Jobs, func(j *model.Job) bool { return !hidden[j.Key] })
	}
	if !e.NoMarkSeen {
		if err := e.Store.MarkSeen(ctx, res.Jobs, now); err != nil {
			return fmt.Errorf("record seen jobs: %w", err)
		}
	}
	if e.NewOnly {
		res.Jobs = keep(res.Jobs, func(j *model.Job) bool { return j.New })
	}
	return nil
}

func keep(jobs []model.Job, ok func(*model.Job) bool) []model.Job {
	out := jobs[:0]
	for i := range jobs {
		if ok(&jobs[i]) {
			out = append(out, jobs[i])
		}
	}
	return out
}

func (e *Engine) runOne(ctx context.Context, src sources.Source, f model.Filters) (r Report, _ []model.Job) {
	r.Source = src.Name()
	if l, ok := src.(sources.Linker); ok {
		r.Link = l.SearchURL(f)
	}
	start := time.Now()
	defer func() {
		r.Duration = time.Since(start)
		if e.Store != nil {
			_ = e.Store.RecordRun(context.WithoutCancel(ctx), store.SourceRun{
				Source: r.Source, RunAt: start, Status: string(r.Status),
				Count: r.Count, Error: r.Err, Duration: r.Duration,
			})
		}
	}()

	if rd, ok := src.(sources.Readier); ok {
		if err := rd.Ready(); err != nil {
			r.Status, r.Err = StatusSkipped, err.Error()
			return r, nil
		}
	}
	if !src.Supports(f) {
		r.Status, r.Err = StatusSkipped, "does not cover these countries/modes"
		return r, nil
	}

	key := cacheKey(src, f)
	ttl := e.ttlFor(src)
	if e.Store != nil && ttl > 0 {
		jobs, _, ok, err := e.Store.CachedJobs(ctx, r.Source, key, ttl)
		if err == nil && ok {
			r.Status, r.Count = StatusCached, len(jobs)
			return r, jobs
		}
	}

	timeout := e.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	if t, ok := src.(sources.Timeouter); ok && t.Timeout() > timeout {
		timeout = t.Timeout()
	}
	fctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	jobs, err := src.Fetch(fctx, f)
	var partial *sources.PartialError
	if err != nil && len(jobs) > 0 && errors.As(err, &partial) {
		r.Err = "partial: " + partial.Error()
		err = nil
	}
	if err != nil {
		r.Status, r.Err = StatusError, errMessage(err, timeout)
		// Serve stale cache rather than nothing.
		if e.Store != nil {
			if stale, _, ok, _ := e.Store.CachedJobs(ctx, r.Source, key, 30*24*time.Hour); ok {
				r.Err += fmt.Sprintf(" (showing %d stale cached results)", len(stale))
				r.Count = len(stale)
				return r, stale
			}
		}
		return r, nil
	}
	for i := range jobs {
		if len(jobs[i].Sources) == 0 {
			jobs[i].Sources = []string{jobs[i].Source}
		}
	}
	r.Status, r.Count = StatusOK, len(jobs)
	if e.Store != nil {
		_ = e.Store.PutCache(ctx, r.Source, key, jobs)
	}
	return r, jobs
}

// ttlFor picks the cache TTL: config per-source > source default > global,
// raised to the source's terms-imposed minimum. --refresh skips everything
// but that minimum.
func (e *Engine) ttlFor(src sources.Source) time.Duration {
	ttl := e.CacheTTL
	if d, ok := src.(sources.DefaultTTLer); ok {
		ttl = d.DefaultTTL()
	}
	if t, ok := e.SourceTTL[src.Name()]; ok && t > 0 {
		ttl = t
	}
	if e.Refresh {
		ttl = 0
	}
	if m, ok := src.(sources.MinTTLer); ok && m.MinTTL() > ttl {
		ttl = m.MinTTL()
	}
	return ttl
}

func errMessage(err error, timeout time.Duration) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Sprintf("timed out after %s", timeout)
	}
	return err.Error()
}

func cacheKey(src sources.Source, f model.Filters) string {
	var raw string
	if k, ok := src.(sources.CacheKeyer); ok {
		raw = k.CacheKey(f)
	} else {
		b, _ := json.Marshal(f.Fetched())
		raw = string(b)
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:8])
}

// classifyAll classifies jobs in parallel; each job is independent.
func classifyAll(c *classify.Classifier, jobs []model.Job) {
	workers := runtime.GOMAXPROCS(0)
	chunk := (len(jobs) + workers - 1) / workers
	var wg sync.WaitGroup
	for start := 0; start < len(jobs); start += chunk {
		part := jobs[start:min(start+chunk, len(jobs))]
		wg.Go(func() {
			for i := range part {
				c.Classify(&part[i])
			}
		})
	}
	wg.Wait()
}

// Sort orders jobs by score (ties: newest first) or by date.
func Sort(jobs []model.Job, by string) {
	sort.SliceStable(jobs, func(i, j int) bool {
		a, b := jobs[i], jobs[j]
		if by != "date" && a.Score != b.Score {
			return a.Score > b.Score
		}
		return a.PostedAt.After(b.PostedAt)
	})
}
