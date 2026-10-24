package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/itxkarthik/jobhunt/internal/app"
	"github.com/itxkarthik/jobhunt/internal/sources"
	"github.com/itxkarthik/jobhunt/internal/sources/ats"
)

func newSourcesCmd() *cobra.Command {
	var checkBoards bool
	cmd := &cobra.Command{
		Use:   "sources",
		Short: "Show each source's status, last fetch, result count, and errors",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := app.Open()
			if err != nil {
				return err
			}
			defer a.Close()
			if checkBoards {
				return runCheckBoards(cmd, a)
			}
			health, err := a.Store.Health(cmd.Context())
			if err != nil {
				return err
			}
			enabled := map[string]sources.Source{}
			for _, s := range a.Sources {
				enabled[s.Name()] = s
			}
			now := time.Now()
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "SOURCE\tENABLED\tLAST RUN\tSTATUS\tJOBS\tLAST OK\tERR/10\tNOTE")
			for _, info := range app.Catalog {
				src, on := enabled[info.Name]
				state := "no"
				if on {
					state = "yes"
				}
				h, ran := health[info.Name]
				lastRun, status, count, lastOK, errs := "never", "-", "-", "-", "-"
				note := info.Description
				if ran {
					lastRun = app.Since(h.Last.RunAt, now) + " ago"
					status, count = h.Last.Status, fmt.Sprint(h.Last.Count)
					errs = fmt.Sprint(h.RecentErrors)
					if !h.LastSuccess.IsZero() {
						lastOK = app.Since(h.LastSuccess, now) + " ago"
					}
					if h.Last.Error != "" && on {
						note = trunc(h.Last.Error, 90)
					}
				}
				if on {
					if r, ok := src.(sources.Readier); ok {
						if err := r.Ready(); err != nil {
							state, note = "no key", err.Error()
						}
					}
				} else if info.Key != "" {
					note += " — needs " + info.Key
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", info.Name, state, lastRun, status, count, lastOK, errs, note)
			}
			w.Flush()
			fmt.Fprintf(cmd.OutOrStdout(), "\n%d company boards in %s (built-in list unless that file exists)\n", len(a.Companies), a.Config.CompaniesFile)
			return nil
		},
	}
	cmd.Flags().BoolVar(&checkBoards, "check-boards", false, "fetch every company board in companies.yaml and report dead slugs")
	return cmd
}

func runCheckBoards(cmd *cobra.Command, a *app.App) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var results []ats.BoardResult
	for _, s := range a.Sources {
		if as, ok := s.(*ats.Source); ok {
			fmt.Fprintf(cmd.ErrOrStderr(), "checking %d %s boards…\n", len(as.Companies), as.ATS)
			results = append(results, as.CheckBoards(ctx)...)
		}
	}
	sort.Slice(results, func(i, j int) bool {
		if (results[i].Err != nil) != (results[j].Err != nil) {
			return results[i].Err != nil
		}
		return results[i].Count < results[j].Count
	})
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "COMPANY\tATS\tSLUG\tJOBS\tSTATUS")
	bad := 0
	for _, r := range results {
		status := "ok"
		switch {
		case r.Err != nil:
			status, bad = "ERROR: "+trunc(r.Err.Error(), 80), bad+1
		case r.Count == 0:
			status, bad = "empty (slug moved?)", bad+1
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n", r.Company.Name, r.Company.ATS, r.Company.Slug, r.Count, status)
	}
	w.Flush()
	fmt.Fprintf(cmd.OutOrStdout(), "\n%d/%d boards need attention\n", bad, len(results))
	return nil
}
