package mca

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/itxkarthik/jobhunt/internal/model"
)

// Options bound one import.
type Options struct {
	States     []string      // codes or names; default KA
	NIC        []string      // industry code prefixes; default DefaultNIC
	Since      time.Duration // back from the newest registration in each state; default 90 days
	MaxRecords int           // records read per state at most; default 5000
}

// Lead is a company to save, with what to remember about it.
type Lead struct {
	CIN      string
	Capital  float64
	Company  model.Company
	Jump     bool    // capital rose enough to suggest a raise
	Previous float64 // capital at the last import, when Jump
}

// Result is what a run found.
type Result struct {
	Read     int // records read
	Matched  int // records in the chosen industries
	Leads    []Lead
	Errors   []string
	Complete bool
}

// Run reads each state's newest registrations. known returns the capital
// stored for a CIN at an earlier import; unchanged companies are skipped.
func (c *Client) Run(ctx context.Context, o Options, known func(cin string) (float64, bool)) (*Result, error) {
	if len(o.States) == 0 {
		o.States = []string{"KA"}
	}
	if len(o.NIC) == 0 {
		o.NIC = DefaultNIC
	}
	if o.Since <= 0 {
		o.Since = 90 * 24 * time.Hour
	}
	if o.MaxRecords <= 0 {
		o.MaxRecords = 5000
	}
	res := &Result{Complete: true}
	for _, s := range o.States {
		state := StateName(s)
		if err := c.state(ctx, state, o, known, res); err != nil {
			if ctx.Err() != nil || errors.Is(err, ErrNoKey) {
				return res, err
			}
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", state, err))
		}
	}
	return res, nil
}

func (c *Client) state(ctx context.Context, state string, o Options, known func(string) (float64, bool), res *Result) error {
	var cutoff time.Time
	for offset := 0; offset < o.MaxRecords; offset += PageSize {
		recs, total, err := c.Page(ctx, state, offset, min(PageSize, o.MaxRecords-offset))
		if err != nil {
			return err
		}
		if offset == 0 && total == 0 {
			return errors.New("no active private companies found; check the state's spelling")
		}
		for _, r := range recs {
			d := r.Date()
			if cutoff.IsZero() && !d.IsZero() {
				cutoff = d.Add(-o.Since)
			}
			if !d.IsZero() && d.Before(cutoff) {
				return nil // sorted newest first: the rest are older
			}
			res.Read++
			if !MatchNIC(r.CIN, o.NIC) {
				continue
			}
			res.Matched++
			capital := r.Capital()
			prev, seen := known(r.CIN)
			l := Lead{CIN: r.CIN, Capital: capital, Company: r.Company()}
			switch {
			case !seen:
			case Jumped(prev, capital):
				l.Jump, l.Previous = true, prev
				l.Company.Tags = append(l.Company.Tags, "capital-increase")
				l.Company.Notes = "" // the registration note is already stored
			default:
				continue
			}
			res.Leads = append(res.Leads, l)
		}
		if len(recs) == 0 || offset+len(recs) >= total {
			return nil
		}
	}
	res.Complete = false
	return nil
}

// CapitalNote describes a capital jump; it's never a confirmed round.
func (l Lead) CapitalNote() string {
	return "Paid-up capital rose from ₹" + money(l.Previous) + " to ₹" + money(l.Capital) +
		" between MCA imports: possibly a raise, not confirmed (" + SourceURL + ")"
}

// Value is what to remember for a lead.
func (l Lead) Value() string { return strconv.FormatFloat(l.Capital, 'f', 0, 64) }
