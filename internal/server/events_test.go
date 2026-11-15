package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/itxkarthik/jobhunt/internal/feed"
	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/search"
)

type sseEvent struct {
	name string
	data string
}

// readSSE parses a whole event stream.
func readSSE(t *testing.T, body io.Reader) []sseEvent {
	t.Helper()
	var out []sseEvent
	var cur sseEvent
	sc := bufio.NewScanner(body)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if cur.name != "" || cur.data != "" {
				out = append(out, cur)
			}
			cur = sseEvent{}
		case strings.HasPrefix(line, ":"): // comment / keepalive
		case strings.HasPrefix(line, "event: "):
			cur.name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			if cur.data != "" {
				t.Fatalf("event %q has more than one data line", cur.name)
			}
			cur.data = strings.TrimPrefix(line, "data: ")
		default:
			t.Fatalf("unexpected SSE line %q", line)
		}
	}
	return out
}

func streamServer(t *testing.T, fn SearchFunc) *httptest.Server {
	t.Helper()
	s, _ := newTestServer(t, func(o *Options) { o.Search = fn })
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv
}

// stream is an open event stream; its body is closed when the test ends.
type stream struct {
	status int
	header http.Header
	body   io.Reader
}

func openStream(t *testing.T, ctx context.Context, srv *httptest.Server, query string) stream {
	t.Helper()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/events?"+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := srv.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return stream{resp.StatusCode, resp.Header, resp.Body}
}

func TestEventsStream(t *testing.T) {
	var got model.Filters
	srv := streamServer(t, func(ctx context.Context, f model.Filters, _ SearchOptions, progress func(search.Report)) (*search.Result, error) {
		got = f
		reports := []search.Report{
			{Source: "greenhouse", Status: search.StatusOK, Count: 900},
			{Source: "hn", Status: search.StatusError, Err: "timeout"},
		}
		for _, r := range reports {
			progress(r)
		}
		return &search.Result{RunAt: t0, Total: 900, Reports: reports, Jobs: []model.Job{
			{Key: "aaaa1111", Title: "Backend Intern", URL: "https://a.example/1", Score: 90},
			// Untrusted text that tries to forge SSE fields must stay inside the JSON.
			{Key: "bbbb2222", Title: "Intern\n\nevent: run\ndata: {\"forged\":true}\n\n", URL: "https://b.example/2", Score: 80},
		}}, nil
	})
	st := openStream(t, context.Background(), srv, "countries=IN,US&types=internship&areas=backend&areas=devops&posted_within=7d")
	if st.status != http.StatusOK || st.header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status %d, content type %q", st.status, st.header.Get("Content-Type"))
	}
	events := readSSE(t, st.body)

	var names []string
	runIDs := map[string]bool{}
	var jobs []string
	var run *feed.RunEvent
	for _, e := range events {
		names = append(names, e.name)
		var ev feed.Event
		if err := json.Unmarshal([]byte(e.data), &ev); err != nil {
			t.Fatalf("event %s: data is not JSON: %v", e.name, err)
		}
		if ev.V != feed.Version || string(ev.Type) != e.name || ev.At.IsZero() {
			t.Errorf("event %s: v=%d type=%s at=%v", e.name, ev.V, ev.Type, ev.At)
		}
		runIDs[ev.RunID] = true
		if ev.Job != nil {
			jobs = append(jobs, ev.Job.Key)
		}
		if ev.Run != nil {
			run = ev.Run
		}
	}
	if want := []string{"source", "source", "job", "job", "run"}; !slices.Equal(names, want) {
		t.Fatalf("events = %v, want %v", names, want)
	}
	if len(runIDs) != 1 {
		t.Errorf("events span %d run IDs, want 1", len(runIDs))
	}
	if !slices.Equal(jobs, []string{"aaaa1111", "bbbb2222"}) || run == nil || run.SourceErrors != 1 || run.Emitted != 2 {
		t.Errorf("jobs %v, run %+v", jobs, run)
	}
	if !slices.Equal(got.Countries, []string{"IN", "US"}) ||
		!slices.Equal(got.Areas, []model.Area{model.AreaBackend, model.AreaDevOps}) || got.PostedWithin != 7*24*time.Hour {
		t.Errorf("filters from query = %+v", got)
	}
}

// Regression: the stream must accept limit like POST /api/search.
func TestEventsLimit(t *testing.T) {
	srv := streamServer(t, func(context.Context, model.Filters, SearchOptions, func(search.Report)) (*search.Result, error) {
		return &search.Result{RunAt: t0, Jobs: slices.Clone(testJobs)}, nil
	})
	st := openStream(t, context.Background(), srv, "limit=2")
	if st.status != http.StatusOK {
		t.Fatalf("status %d", st.status)
	}
	var jobs int
	var run *feed.RunEvent
	for _, e := range readSSE(t, st.body) {
		var ev feed.Event
		if err := json.Unmarshal([]byte(e.data), &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Job != nil {
			jobs++
		}
		if ev.Run != nil {
			run = ev.Run
		}
	}
	if jobs != 2 || run == nil || run.Emitted != 2 || run.Matched != len(testJobs) {
		t.Errorf("jobs %d, run %+v; want 2 emitted of %d matched", jobs, run, len(testJobs))
	}
}

func TestEventsClientDisconnectCancelsSearch(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	srv := streamServer(t, func(ctx context.Context, _ model.Filters, _ SearchOptions, _ func(search.Report)) (*search.Result, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	openStream(t, ctx, srv, "")
	<-started
	cancel()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("search kept running after the client went away")
	}
}

func TestEventsSearchError(t *testing.T) {
	srv := streamServer(t, func(context.Context, model.Filters, SearchOptions, func(search.Report)) (*search.Result, error) {
		return nil, errors.New("open /home/user/secret.db: broken")
	})
	events := readSSE(t, openStream(t, context.Background(), srv, "").body)
	if len(events) != 1 || events[0].name != "error" || strings.Contains(events[0].data, "/home") {
		t.Fatalf("events = %+v, want one generic error event", events)
	}
}

func TestEventsRejectsBadQueryBeforeStreaming(t *testing.T) {
	s, _ := newTestServer(t, nil)
	for _, q := range []string{"colour=blue", "visa=maybe", "countries=Atlantis", "min_salary=lots", "min_salary=NaN", "min_salary=Inf", "min_salary=-1", "sort=random", "limit=-1", "limit=five"} {
		w := serve(s, req(http.MethodGet, "/api/events?"+q, ""))
		if w.Code != http.StatusBadRequest || w.Header().Get("Content-Type") == "text/event-stream" {
			t.Errorf("%s: status %d, type %q", q, w.Code, w.Header().Get("Content-Type"))
		}
	}
	r := req(http.MethodGet, "/api/events", "")
	r.Header.Del("Authorization")
	if w := serve(s, r); w.Code != http.StatusUnauthorized {
		t.Errorf("stream without token = %d", w.Code)
	}
}

func TestSearchRequestOf(t *testing.T) {
	q, _ := url.ParseQuery("countries=IN,US&countries=JP&keywords=Go&visa=true&new_only=1&refresh=false&min_salary=500000&sort=date&posted_within=3d")
	req, err := searchRequestOf(q)
	if err != nil {
		t.Fatal(err)
	}
	in := req.FilterInput
	if !slices.Equal(in.Countries, []string{"IN", "US", "JP"}) || !slices.Equal(in.Keywords, []string{"Go"}) ||
		!in.Visa || !req.NewOnly || req.Refresh || in.MinSalary != 500000 || req.Sort != "date" || in.PostedWithin != "3d" {
		t.Errorf("request = %+v", req)
	}
}
