package main

import (
	"cmp"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/itxkarthik/jobhunt/internal/app"
	"github.com/itxkarthik/jobhunt/internal/feed"
	"github.com/itxkarthik/jobhunt/internal/search"
	"github.com/itxkarthik/jobhunt/internal/store"
)

// feedFlags are the feed-specific flags; filters and engine options are shared with search.
type feedFlags struct {
	minScore      float64
	since         string
	cursor        string
	every         time.Duration
	webhook       string
	noDescription bool
}

func (fl *feedFlags) validate() error {
	if fl.minScore < 0 || fl.minScore > 100 {
		return fmt.Errorf("--min-score %.0f is out of range (0-100)", fl.minScore)
	}
	if fl.every != 0 {
		if err := checkInterval("every", fl.every); err != nil {
			return err
		}
	}
	if fl.cursor != "" {
		return store.ValidCursorName(fl.cursor)
	}
	return nil
}

func newFeedCmd() *cobra.Command {
	var (
		ff filterFlags
		so searchOpts
		fl feedFlags
	)
	cmd := &cobra.Command{
		Use:   "feed",
		Short: "Headless mode: emit search results as NDJSON events for pipelines and agents",
		Long: `feed runs a search and writes versioned NDJSON events to stdout, one JSON
object per line: a "source" event per source, a "job" event per selected job
(best score first), then a "run" event that completes the batch. Logs go to
stderr, so stdout can be piped straight into a queue, a database loader or
an agent. The event format is documented in docs/feed.schema.json.

--cursor NAME gives a consumer at-least-once delivery: each run emits the
jobs first seen since that consumer's last successful run, and the cursor
only advances once stdout and the webhook (if any) have accepted the batch.

--webhook (or feed.webhook_url / JOBHUNT_WEBHOOK_URL) also POSTs each batch.
Set JOBHUNT_WEBHOOK_SECRET to sign requests with HMAC-SHA256.`,
		Example: `  jobhunt feed -p india-backend --new-only | jq -c 'select(.type=="job") | .job.title'
  jobhunt feed -p india-backend --cursor agent --min-score 60 | ./agent.py
  jobhunt feed -p india-backend --cursor db --every 6h | ./load-into-postgres.sh
  JOBHUNT_WEBHOOK_URL=https://example.com/hook jobhunt feed -p india-backend --cursor n8n`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := fl.validate(); err != nil {
				return err
			}
			since, err := parseSince(fl.since, time.Now())
			if err != nil {
				return err
			}
			a, err := app.Open()
			if err != nil {
				return err
			}
			defer a.Close()
			f, err := ff.resolve(cmd, a)
			if err != nil {
				return err
			}
			log := cmd.ErrOrStderr()
			hook, err := webhookFor(fl.webhook, a, log)
			if err != nil {
				return err
			}
			fd := &feeder{
				search:  func(ctx context.Context) (*search.Result, error) { return so.run(ctx, a, f) },
				cursors: a.Store,
				out:     cmd.OutOrStdout(),
				log:     log,
				webhook: hook,
				cursor:  fl.cursor,
				opts:    feed.Options{MinScore: fl.minScore, Since: since, NoDescription: fl.noDescription},
				newID:   rand.Text,
			}
			if fl.every == 0 {
				return fd.once(cmd.Context())
			}
			fmt.Fprintf(log, "feed: %s every %s — Ctrl+C to stop\n", summary(f), fl.every)
			repeat(cmd.Context(), fl.every, false, fd.once,
				func(err error) { fmt.Fprintf(log, "%s  feed error: %v\n", stamp(time.Now()), err) })
			return nil
		},
	}
	ff.register(cmd)
	so.register(cmd)
	fs := cmd.Flags()
	fs.Float64Var(&fl.minScore, "min-score", 0, "only emit jobs scoring at least this (0-100)")
	fs.StringVar(&fl.since, "since", "", "only jobs first seen at or after this: RFC 3339 time, date (2026-11-14) or age (24h, 7d)")
	fs.StringVar(&fl.cursor, "cursor", "", "named consumer position for at-least-once delivery (e.g. agent)")
	fs.DurationVar(&fl.every, "every", 0, "keep running and emit a batch every interval (min 15m); default: run once")
	fs.StringVar(&fl.webhook, "webhook", "", "also POST each batch to this http(s) URL (prefer JOBHUNT_WEBHOOK_URL if it holds a token)")
	fs.BoolVar(&fl.noDescription, "no-description", false, "omit job descriptions from events")
	cmd.MarkFlagsMutuallyExclusive("since", "cursor")
	return cmd
}

// webhookFor builds the webhook sink from the flag or config, or returns
// nil when none is configured. Warnings go to log; secrets never do.
func webhookFor(flagURL string, a *app.App, log io.Writer) (*feed.Webhook, error) {
	u := cmp.Or(flagURL, a.Config.Feed.WebhookURL)
	if u == "" {
		return nil, nil
	}
	hook, err := feed.NewWebhook(u, []byte(a.Config.Feed.WebhookSecret), a.Config.UserAgent)
	if err != nil {
		return nil, err
	}
	if hook.Plaintext() {
		fmt.Fprintf(log, "warning: webhook %s uses plain http; batches cross the network unencrypted\n", hook)
	}
	if !hook.Signed() {
		fmt.Fprintf(log, "note: webhook %s is unsigned; set JOBHUNT_WEBHOOK_SECRET so the receiver can verify requests\n", hook)
	}
	return hook, nil
}

// cursorStore is the part of *store.Store the feeder needs.
type cursorStore interface {
	Cursor(ctx context.Context, name string) (time.Time, bool, error)
	AdvanceCursor(ctx context.Context, name string, t time.Time) error
}

// feeder runs one batch: search, select, write, deliver, advance cursor.
type feeder struct {
	search  func(context.Context) (*search.Result, error)
	cursors cursorStore
	out     io.Writer
	log     io.Writer
	webhook *feed.Webhook // nil: stdout only
	cursor  string        // empty: stateless
	opts    feed.Options
	newID   func() string
}

func (fd *feeder) once(ctx context.Context) error {
	opts := fd.opts
	if fd.cursor != "" {
		t, ok, err := fd.cursors.Cursor(ctx, fd.cursor)
		if err != nil {
			return err
		}
		if ok {
			// first_seen has one-second precision, and the cursor is the
			// last delivered run's time, so pending jobs start a second later.
			opts.Since = t.Add(time.Second)
		}
	}
	res, err := fd.search(ctx)
	if err != nil {
		return err
	}
	runID := fd.newID()
	events := feed.Build(res, runID, opts)
	body, err := feed.Marshal(events)
	if err != nil {
		return fmt.Errorf("encode events: %w", err)
	}
	if _, err := fd.out.Write(body); err != nil {
		return fmt.Errorf("write events: %w", err)
	}
	run := events[len(events)-1].Run
	var notes []string
	if fd.webhook != nil {
		if err := fd.webhook.Send(ctx, runID, body); err != nil {
			if fd.cursor != "" {
				err = fmt.Errorf("%w (cursor %s not advanced; the next run resends these jobs)", err, fd.cursor)
			}
			return err
		}
		notes = append(notes, "webhook delivered")
	}
	if fd.cursor != "" {
		if err := fd.cursors.AdvanceCursor(ctx, fd.cursor, res.RunAt); err != nil {
			return err
		}
		notes = append(notes, "cursor "+fd.cursor+" advanced")
	}
	line := fmt.Sprintf("%s  feed run %s: %d of %d matching jobs emitted", stamp(time.Now()), runID, run.Emitted, run.Matched)
	if run.SourceErrors > 0 {
		line += fmt.Sprintf(", %d source errors", run.SourceErrors)
	}
	if len(notes) > 0 {
		line += "; " + strings.Join(notes, ", ")
	}
	fmt.Fprintln(fd.log, line)
	return nil
}

// parseSince accepts an RFC 3339 time, a date (local midnight), or an age
// such as 24h or 7d meaning that long before now.
func parseSince(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation(time.DateOnly, s, now.Location()); err == nil {
		return t, nil
	}
	if d, err := parseAge(s); err == nil {
		return now.Add(-d), nil
	}
	return time.Time{}, fmt.Errorf("bad --since %q (use 2026-11-14T09:00:00+05:30, 2026-11-14, 24h or 7d)", s)
}

func stamp(t time.Time) string { return t.Format("2006-01-02 15:04") }

var _ cursorStore = (*store.Store)(nil)
