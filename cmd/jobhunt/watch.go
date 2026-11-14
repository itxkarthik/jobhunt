package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/itxkarthik/jobhunt/internal/app"
	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/notify"
)

func newWatchCmd() *cobra.Command {
	var (
		ff       filterFlags
		every    time.Duration
		minScore float64
		once     bool
		quiet    bool
	)
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Re-run a search on an interval and notify about new matches",
		Long: `watch runs the search every --every, and sends a desktop notification
(notify-send / osascript) when jobs appear that no earlier search has shown.
It runs in the foreground; see the README for a systemd user service.`,
		Example: `  jobhunt watch --preset india-backend --every 6h
  jobhunt watch -p india-backend --min-score 60 --once   # one check, e.g. from cron`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkInterval("every", every); err != nil {
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
			w := cmd.OutOrStdout()
			send := notify.Send
			if quiet {
				send = func(string, string) error { return nil }
			}
			search := func(ctx context.Context) ([]model.Job, error) {
				so := searchOpts{newOnly: true}
				res, err := so.run(ctx, a, f)
				if err != nil {
					return nil, err
				}
				return res.Jobs, nil
			}
			fmt.Fprintf(w, "watching %s every %s (min score %.0f) — Ctrl+C to stop\n", summary(f), every, minScore)
			repeat(cmd.Context(), every, once,
				func(ctx context.Context) error { return watchOnce(ctx, w, search, minScore, send) },
				func(err error) { fmt.Fprintf(w, "%s  error: %v\n", time.Now().Format("2006-01-02 15:04"), err) })
			return nil
		},
	}
	ff.register(cmd)
	cmd.Flags().DurationVar(&every, "every", 6*time.Hour, "how often to search (min 15m)")
	cmd.Flags().Float64Var(&minScore, "min-score", 0, "only notify about jobs scoring at least this (0-100)")
	cmd.Flags().BoolVar(&once, "once", false, "check once and exit (for cron/systemd timers)")
	cmd.Flags().BoolVar(&quiet, "no-notify", false, "print only, no desktop notification")
	return cmd
}

// watchOnce runs one search, prints new jobs at or above minScore, and
// notifies with a count and the top three.
func watchOnce(ctx context.Context, w io.Writer, search func(context.Context) ([]model.Job, error),
	minScore float64, send func(title, body string) error) error {
	jobs, err := search(ctx)
	if err != nil {
		return err
	}
	var hits []model.Job
	for _, j := range jobs {
		if j.New && j.Score >= minScore {
			hits = append(hits, j)
		}
	}
	stamp := time.Now().Format("2006-01-02 15:04")
	if len(hits) == 0 {
		fmt.Fprintf(w, "%s  no new jobs\n", stamp)
		return nil
	}
	fmt.Fprintf(w, "%s  %d new:\n", stamp, len(hits))
	var top []string
	for i, j := range hits {
		fmt.Fprintf(w, "  %3.0f  %s  %s — %s  %s\n", j.Score, j.Key, j.Title, j.Company, j.URL)
		if i < 3 {
			top = append(top, fmt.Sprintf("%.0f · %s — %s", j.Score, j.Title, j.Company))
		}
	}
	if len(hits) > 3 {
		top = append(top, fmt.Sprintf("…and %d more (jobhunt tui)", len(hits)-3))
	}
	title := fmt.Sprintf("jobhunt: %d new job", len(hits))
	if len(hits) > 1 {
		title += "s"
	}
	if err := send(title, strings.Join(top, "\n")); err != nil {
		fmt.Fprintf(w, "  (notification failed: %v)\n", err)
	}
	return nil
}
