package edgar

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/itxkarthik/jobhunt/internal/model"
)

// Options bound one import run.
type Options struct {
	Since       time.Duration // look back this far (default 7 days)
	MaxFilings  int           // fetch at most this many Form D documents (default 200)
	Industries  []string      // default DefaultIndustries; "any" keeps all
	AllEntities bool          // keep LLCs and LPs too
	Now         time.Time     // default time.Now()
}

// Result is what a run found.
type Result struct {
	Days       int // daily indexes read
	Filings    int // new Form D filings in them
	Candidates int // left after the name check
	Fetched    int // Form D documents fetched
	Companies  []model.Company
	// Done lists items finished for good, to skip next time: "day:20261117"
	// for a fully read day, "acc:<accession>" for a fetched filing.
	Done     []string
	Errors   []string
	Complete bool // false when MaxFilings cut the run short
}

// Run reads the daily indexes from Since ago up to the latest published
// day and fetches the Form D documents of likely startups. seen reports
// items finished in an earlier run, so they cost no requests.
func (im *Importer) Run(ctx context.Context, o Options, seen func(item string) bool) (*Result, error) {
	if o.Since <= 0 {
		o.Since = 7 * 24 * time.Hour
	}
	if o.MaxFilings <= 0 {
		o.MaxFilings = 200
	}
	if len(o.Industries) == 0 {
		o.Industries = DefaultIndustries
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	res := &Result{Complete: true}
	for _, day := range Days(o.Now, o.Since) {
		dayItem := "day:" + day.Format("20060102")
		if seen(dayItem) {
			continue
		}
		filings, err := im.DailyIndex(ctx, day)
		switch {
		case errors.Is(err, ErrNoIndex):
			// A holiday, once it's clearly past; otherwise try again later.
			if o.Now.Sub(day) > 4*24*time.Hour {
				res.Done = append(res.Done, dayItem)
			}
			continue
		case errors.Is(err, ErrBlocked), ctx.Err() != nil:
			return res, err
		case err != nil:
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", day.Format(time.DateOnly), err))
			continue
		}
		res.Days++
		res.Filings += len(filings)
		dayDone := true
		for _, f := range filings {
			accItem := "acc:" + f.Accession
			if seen(accItem) || !Candidate(f.Company, o.AllEntities) {
				continue
			}
			res.Candidates++
			if res.Fetched >= o.MaxFilings {
				dayDone, res.Complete = false, false
				continue
			}
			res.Fetched++
			fd, err := im.FetchFormD(ctx, f)
			if errors.Is(err, ErrBlocked) || ctx.Err() != nil {
				return res, errors.Join(err, ctx.Err())
			}
			if err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("%s (%s): %v", f.Company, f.Accession, err))
				dayDone = false
				continue
			}
			res.Done = append(res.Done, accItem)
			if fd.Keep(o.Industries, o.AllEntities) {
				res.Companies = append(res.Companies, fd.Company(f))
			}
		}
		if dayDone {
			res.Done = append(res.Done, dayItem)
		}
	}
	return res, nil
}

// Days lists the weekdays from since ago up to the last day whose index
// should be out. EDGAR builds a day's index from about 10 pm US Eastern,
// so a day counts once 04:00 UTC the next morning has passed.
func Days(now time.Time, since time.Duration) []time.Time {
	now = now.UTC()
	last := now.Add(-28 * time.Hour).Truncate(24 * time.Hour) // day + 1d + 4h <= now
	first := now.Add(-since).Truncate(24 * time.Hour)
	var out []time.Time
	for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
		if wd := d.Weekday(); wd != time.Saturday && wd != time.Sunday {
			out = append(out, d)
		}
	}
	return out
}
