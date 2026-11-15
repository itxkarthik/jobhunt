package server

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/itxkarthik/jobhunt/internal/feed"
	"github.com/itxkarthik/jobhunt/internal/search"
)

const keepalive = 15 * time.Second

// events streams a search as Server-Sent Events. Each SSE event is named
// after its v1 feed type and carries the same JSON as a `jobhunt feed`
// line: a "source" event as each source finishes, then "job" events, then
// "run". A search that fails mid-stream ends with an "error" event.
// The client disconnecting cancels the search.
func (s *Server) events(w http.ResponseWriter, r *http.Request) error {
	req, err := searchRequestOf(r.URL.Query())
	if err != nil {
		return err
	}
	f, o, err := s.parseSearch(req)
	if err != nil {
		return err
	}

	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{}) // the stream may outlive the server's write timeout
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("X-Accel-Buffering", "no") // don't let a reverse proxy buffer the stream
	w.WriteHeader(http.StatusOK)
	if err := rc.Flush(); err != nil {
		return nil // can't stream to this client
	}
	// From here on the status is sent: errors end the stream instead of
	// being returned.

	ctx := r.Context()
	runID := rand.Text()
	reports := make(chan search.Report, 16)
	type outcome struct {
		res *search.Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := s.runSearch(ctx, f, o, func(rp search.Report) {
			select { // called from the engine's goroutines; the loop below writes
			case reports <- rp:
			case <-ctx.Done():
			}
		})
		done <- outcome{res, err}
	}()

	send := func(e feed.Event) bool {
		return writeEvent(w, rc, string(e.Type), e) == nil
	}
	source := func(rp search.Report) bool {
		src := feed.SourceOf(rp)
		return send(feed.Event{V: feed.Version, Type: feed.TypeSource, RunID: runID, At: time.Now().UTC(), Source: &src})
	}
	tick := time.NewTicker(keepalive)
	defer tick.Stop()
	for {
		select {
		case rp := <-reports:
			if !source(rp) {
				return nil
			}
		case <-tick.C:
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil || rc.Flush() != nil {
				return nil
			}
		case <-ctx.Done():
			return nil
		case out := <-done:
			// Run has returned, so every progress callback has too; send
			// any reports still queued before the results.
			for len(reports) > 0 {
				if !source(<-reports) {
					return nil
				}
			}
			if out.err != nil {
				if ctx.Err() == nil {
					s.log.Error("event stream search failed", "err", out.err)
					_ = writeEvent(w, rc, "error", map[string]string{"error": "search failed"})
				}
				return nil
			}
			for _, e := range feed.Build(out.res, runID, feed.Options{}) {
				if e.Type != feed.TypeSource && !send(e) { // sources were streamed already
					return nil
				}
			}
			return nil
		}
	}
}

// writeEvent writes one SSE event. JSON never contains a raw newline, so
// untrusted text in v can't inject SSE fields or split the event.
func writeEvent(w io.Writer, rc *http.ResponseController, name string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b); err != nil {
		return err
	}
	return rc.Flush()
}

// searchRequestOf reads a search from query parameters, named like the
// JSON fields of POST /api/search. Lists are comma-separated or repeated.
func searchRequestOf(q url.Values) (searchRequest, error) {
	var req searchRequest
	in := &req.FilterInput
	lists := map[string]*[]string{
		"countries": &in.Countries, "modes": &in.Modes, "types": &in.Types, "areas": &in.Areas,
		"experience": &in.Experience, "keywords": &in.Keywords, "exclude": &in.Exclude,
	}
	strs := map[string]*string{"posted_within": &in.PostedWithin, "currency": &in.Currency, "sort": &req.Sort}
	bools := map[string]*bool{
		"visa": &in.Visa, "strict_country": &in.StrictCountry, "include_unknown": &in.IncludeUnknown,
		"refresh": &req.Refresh, "new_only": &req.NewOnly, "show_hidden": &req.ShowHidden,
	}
	for name, vals := range q {
		last := vals[len(vals)-1]
		switch {
		case lists[name] != nil:
			for _, v := range vals {
				*lists[name] = append(*lists[name], strings.Split(v, ",")...)
			}
		case strs[name] != nil:
			*strs[name] = last
		case bools[name] != nil:
			b, err := strconv.ParseBool(last)
			if err != nil {
				return req, badRequest("%s must be true or false", name)
			}
			*bools[name] = b
		case name == "min_salary":
			n, err := strconv.ParseFloat(last, 64)
			if err != nil {
				return req, badRequest("min_salary must be a number")
			}
			in.MinSalary = n
		default:
			return req, badRequest("unknown parameter %q", name)
		}
	}
	return req, nil
}
