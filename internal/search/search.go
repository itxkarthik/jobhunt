// Package search fans a query out to every source concurrently, applies
// caching and health tracking, then classifies and filters the results.
package search

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/itxkarthik/jobhunt/internal/classify"
	"github.com/itxkarthik/jobhunt/internal/model"
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
	Total   int // jobs fetched across sources before filtering
}

type Engine struct {
	Sources     []sources.Source
	Store       *store.Store // may be nil (no cache/health)
	Classifier  *classify.Classifier
	CacheTTL    time.Duration
	Timeout     time.Duration
	Concurrency int
	Refresh     bool // bypass cache
	// Progress, if set, is called as each source finishes.
	Progress func(Report)
}

// Run executes the search. It only returns an error for problems that make
// the whole search impossible; individual source failures go in Reports.
func (e *Engine) Run(ctx context.Context, f model.Filters) (*Result, error) {
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
	for i := range all {
		e.Classifier.Classify(&all[i])
	}
	res.Jobs = Filter(all, f, e.Classifier, time.Now())
	sort.SliceStable(res.Jobs, func(i, j int) bool {
		return res.Jobs[i].PostedAt.After(res.Jobs[j].PostedAt)
	})
	return res, nil
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
	ttl := e.CacheTTL
	if m, ok := src.(sources.MinTTLer); ok && m.MinTTL() > ttl {
		ttl = m.MinTTL()
	}
	if e.Store != nil && !e.Refresh {
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
	fctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	jobs, err := src.Fetch(fctx, f)
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
		b, _ := json.Marshal(f)
		raw = string(b)
	}
	sum := sha1.Sum([]byte(raw))
	return hex.EncodeToString(sum[:8])
}
