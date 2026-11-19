package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/spf13/cobra"

	"github.com/itxkarthik/jobhunt/internal/app"
)

func newCompanySyncCmd() *cobra.Command {
	var (
		o      app.SyncOptions
		every  time.Duration
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Run every configured company importer: EDGAR, MCA, HN leads, board checks",
		Long: `sync runs each importer in turn and stores what it finds in the companies
list. Every importer keeps its own position, so a run fetches only what's
new and a repeat run costs few or no requests. A failing importer is
reported and the others still run. Importers that need a key or contact
(EDGAR, MCA) are skipped until configured.

Publish what changed with jobhunt feed --companies --cursor NAME, which
gives a queue, database or agent at-least-once delivery of new and updated
companies, over stdout or the signed webhook.

Run it from cron or a systemd user timer (see docs/examples), or keep it
running with --every.`,
		Example: `  jobhunt companies sync
  jobhunt companies sync --only hn,boards --json
  jobhunt companies sync --every 6h`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if every != 0 {
				if err := checkInterval("every", every); err != nil {
					return err
				}
			}
			for _, n := range o.Only {
				switch n {
				case "edgar", "mca", "hn", "boards":
				default:
					return fmt.Errorf("unknown importer %q (edgar, mca, hn, boards)", n)
				}
			}
			a, err := app.Open()
			if err != nil {
				return err
			}
			defer a.Close()
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			once := func(ctx context.Context) error {
				res, err := app.Sync(ctx, a.Importers(o))
				if asJSON {
					if jerr := writeJSON(cmd.OutOrStdout(), res); jerr != nil {
						return jerr
					}
				} else {
					printSync(cmd.OutOrStdout(), res)
				}
				if err == nil {
					err = a.RefreshBoards(ctx) // boards found by the importers join the next search
				}
				return err
			}
			if every == 0 {
				return once(ctx)
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "companies sync every %s — Ctrl+C to stop\n", every)
			repeat(ctx, every, false, once, func(err error) {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s  sync error: %v\n", stamp(time.Now()), err)
			})
			return nil
		},
	}
	fs := cmd.Flags()
	fs.StringSliceVar(&o.Only, "only", nil, "run only these importers: edgar, mca, hn, boards")
	fs.BoolVar(&o.Prune, "prune", false, "remove added boards the ATS reports as gone (404/410)")
	fs.DurationVar(&every, "every", 0, "keep running and sync every interval (min 15m); default: run once")
	fs.BoolVar(&asJSON, "json", false, "print the per-importer summary as JSON")
	return cmd
}

func printSync(w io.Writer, res []app.SyncResult) {
	fmt.Fprintf(w, "%s  companies sync\n", stamp(time.Now()))
	for _, r := range res {
		switch {
		case r.Skipped != "":
			fmt.Fprintf(w, "  %-7s skipped: %s\n", r.Name, r.Skipped)
		case r.Summary == nil:
			fmt.Fprintf(w, "  %-7s error: %s\n", r.Name, r.Error)
		default:
			s := r.Summary
			fmt.Fprintf(w, "  %-7s read %d, found %d, fetched %d, added %d, updated %d", r.Name, s.Read, s.Found, s.Fetched, len(s.Added), len(s.Updated))
			if n := len(s.Errors); n > 0 {
				fmt.Fprintf(w, ", %d errors", n)
			}
			if r.Error != "" {
				fmt.Fprintf(w, "; stopped: %s", r.Error)
			}
			fmt.Fprintln(w)
		}
	}
}
