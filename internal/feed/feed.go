// Package feed turns search results into versioned, machine-readable
// events for headless use: pipelines, queues, databases and agents.
//
// The event format is a public contract (docs/feed.schema.json). Adding
// a field is fine; removing or changing one requires bumping Version.
package feed

import (
	"bytes"
	"encoding/json"
	"io"
	"time"

	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/search"
)

// Version is the event schema version, sent as "v" on every event.
const Version = 1

type Type string

const (
	TypeSource Type = "source" // one per source, in source order
	TypeJob    Type = "job"    // one per emitted job, best score first
	TypeRun    Type = "run"    // last event of a run; marks the batch complete
)

// Event is one NDJSON line. Exactly one of Source, Job or Run is set,
// matching Type.
type Event struct {
	V      int          `json:"v"`
	Type   Type         `json:"type"`
	RunID  string       `json:"run_id"`
	At     time.Time    `json:"at"`
	Source *SourceEvent `json:"source,omitempty"`
	Job    *model.Job   `json:"job,omitempty"`
	Run    *RunEvent    `json:"run,omitempty"`
}

// SourceEvent reports what one source did during the run.
type SourceEvent struct {
	Name       string        `json:"name"`
	Status     search.Status `json:"status"`
	Count      int           `json:"count"` // jobs returned before filtering
	DurationMS int64         `json:"duration_ms"`
	Error      string        `json:"error,omitempty"`
}

// RunEvent summarises the run.
type RunEvent struct {
	Fetched      int `json:"fetched"` // jobs from all sources before filtering
	Matched      int `json:"matched"` // jobs matching the filters, after dedup
	Emitted      int `json:"emitted"` // job events in this batch
	SourceErrors int `json:"source_errors"`
}

// Options choose which matched jobs become events.
type Options struct {
	// MinScore drops jobs scoring below it (0–100).
	MinScore float64
	// Since keeps only jobs first seen at or after it. Zero means no limit.
	Since time.Time
	// NoDescription omits job descriptions to keep events small.
	NoDescription bool

	// Delivered holds keys the consumer already has; they're left out.
	Delivered map[string]bool
	// Pending jobs went out in an earlier batch that wasn't accepted. They
	// were already selected, so they're emitted first and never filtered
	// again, even if they've since left the source. A pending job that's
	// still in the results is sent with the fresh record.
	Pending []model.Job
}

// Build converts one search result into a batch of events: every source,
// then pending redeliveries, then the selected jobs in result order, then
// the run summary.
func Build(res *search.Result, runID string, opts Options) []Event {
	at := utc(res.RunAt)
	ev := func(t Type) Event { return Event{V: Version, Type: t, RunID: runID, At: at} }

	events := make([]Event, 0, len(res.Reports)+len(res.Jobs)+1)
	run := &RunEvent{Fetched: res.Total, Matched: len(res.Jobs)}
	for _, r := range res.Reports {
		if r.Status == search.StatusError {
			run.SourceErrors++
		}
		e := ev(TypeSource)
		e.Source = &SourceEvent{
			Name: r.Source, Status: r.Status, Count: r.Count,
			DurationMS: r.Duration.Milliseconds(), Error: r.Err,
		}
		events = append(events, e)
	}
	emit := func(j model.Job) { // j is a copy: Build must not modify the result
		if opts.NoDescription {
			j.Description = ""
		}
		j.PostedAt, j.FirstSeenAt = utc(j.PostedAt), utc(j.FirstSeenAt)
		e := ev(TypeJob)
		e.Job = &j
		events = append(events, e)
		run.Emitted++
	}
	current := make(map[string]int, len(res.Jobs))
	for i, j := range res.Jobs {
		current[j.Key] = i
	}
	pending := make(map[string]bool, len(opts.Pending))
	for _, j := range opts.Pending {
		pending[j.Key] = true
		if i, ok := current[j.Key]; ok {
			emit(res.Jobs[i])
		} else {
			emit(j)
		}
	}
	for i := range res.Jobs {
		j := &res.Jobs[i]
		if !pending[j.Key] && !opts.Delivered[j.Key] && opts.keep(j) {
			emit(*j)
		}
	}
	e := ev(TypeRun)
	e.Run = run
	return append(events, e)
}

// utc normalises event timestamps; the zero time stays zero so it's omitted.
func utc(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return t.UTC()
}

func (o Options) keep(j *model.Job) bool {
	if j.Score < o.MinScore {
		return false
	}
	return o.Since.IsZero() || !j.FirstSeenAt.Before(o.Since)
}

// Encode writes events as NDJSON: one compact JSON object per line.
// Untrusted text can't break the framing because JSON escapes newlines.
func Encode(w io.Writer, events []Event) error {
	enc := json.NewEncoder(w)
	for i := range events {
		if err := enc.Encode(&events[i]); err != nil {
			return err
		}
	}
	return nil
}

// Marshal returns the NDJSON encoding of events.
func Marshal(events []Event) ([]byte, error) {
	var buf bytes.Buffer
	err := Encode(&buf, events)
	return buf.Bytes(), err
}
