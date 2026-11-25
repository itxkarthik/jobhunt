package main

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/itxkarthik/jobhunt/internal/app"
	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/store"
)

func newTrackerCmd() *cobra.Command {
	var statuses []string
	list := func(cmd *cobra.Command, _ []string) error {
		a, err := app.Open()
		if err != nil {
			return err
		}
		defer a.Close()
		var sts []store.Status
		for _, s := range statuses {
			st, err := store.ParseStatus(s)
			if err != nil {
				return err
			}
			sts = append(sts, st)
		}
		rows, err := a.Store.TrackedJobs(cmd.Context(), sts...)
		if err != nil {
			return err
		}
		w := cmd.OutOrStdout()
		if len(rows) == 0 {
			fmt.Fprintln(w, "Nothing tracked yet. Use `jobhunt tracker set <key> saved` or S/a in `jobhunt tui`.")
			return nil
		}
		now := time.Now()
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "KEY\tSTATUS\tUPDATED\tTITLE\tCOMPANY\tURL")
		for _, t := range rows {
			title, company, url := "(job no longer cached)", "", ""
			if t.Job != nil {
				title, company, url = trunc(t.Job.Title, 48), trunc(t.Job.Company, 22), t.Job.URL
			}
			status := string(t.Status)
			if t.Unlisted(now) {
				status += " (not listed " + app.Since(t.ListedAt, now) + ")"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s ago\t%s\t%s\t%s\n", t.Key, status, app.Since(t.UpdatedAt, now), title, company, url)
		}
		tw.Flush()
		counts := map[store.Status]int{}
		for _, t := range rows {
			counts[t.Status]++
		}
		var parts []string
		for _, st := range store.Statuses {
			if counts[st] > 0 {
				parts = append(parts, fmt.Sprintf("%s %d", st, counts[st]))
			}
		}
		fmt.Fprintf(w, "\n%d tracked: %s\n", len(rows), strings.Join(parts, " · "))
		return nil
	}

	cmd := &cobra.Command{
		Use:   "tracker",
		Short: "Application tracker: saved → applied → oa → interview → offer/rejected",
		Example: `  jobhunt tracker                         # list everything
  jobhunt tracker set 96ac applied --note "referral from Priya"
  jobhunt tracker set 96ac oa
  jobhunt tracker add https://www.linkedin.com/jobs/view/123 -t "Backend Intern" -c Acme
  jobhunt tracker show 96ac
  jobhunt tracker list --status applied,interview`,
		RunE: list,
	}
	cmd.Flags().StringSliceVarP(&statuses, "status", "s", nil, "only these statuses")
	listCmd := &cobra.Command{Use: "list", Short: "List tracked jobs", RunE: list}
	listCmd.Flags().StringSliceVarP(&statuses, "status", "s", nil, "only these statuses")

	var note string
	set := &cobra.Command{
		Use:   "set <key> <status>",
		Short: "Set a job's status (key prefix from the KEY column is enough)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.ParseStatus(args[1])
			if err != nil {
				return err
			}
			return withKey(cmd, args[0], func(a *app.App, key string) error {
				if err := a.Store.SetStatus(cmd.Context(), key, st, note); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s → %s\n", describe(cmd, a, key), st)
				return nil
			})
		},
	}
	set.Flags().StringVar(&note, "note", "", "note to attach (appended to the job's notes)")

	show := &cobra.Command{
		Use:   "show <key>",
		Short: "Show a tracked job with its history",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withKey(cmd, args[0], func(a *app.App, key string) error {
				w := cmd.OutOrStdout()
				j, err := a.Store.Job(cmd.Context(), key)
				if err != nil {
					return err
				}
				if j != nil {
					fmt.Fprintf(w, "%s — %s\n%s\n%s · %s · %s\n", j.Title, j.Company, j.URL, model.FullLocation(j), j.WorkMode, j.JobType)
				}
				rows, err := a.Store.TrackedJobs(cmd.Context())
				if err != nil {
					return err
				}
				for _, t := range rows {
					if t.Key == key {
						fmt.Fprintf(w, "\nStatus: %s (since %s)\n", t.Status, t.UpdatedAt.Format("2006-01-02"))
						if !t.AppliedAt.IsZero() {
							fmt.Fprintf(w, "Applied: %s\n", t.AppliedAt.Format("2006-01-02"))
						}
						if t.Notes != "" {
							fmt.Fprintf(w, "Notes:\n  %s\n", strings.ReplaceAll(t.Notes, "\n", "\n  "))
						}
					}
				}
				evs, err := a.Store.Events(cmd.Context(), key)
				if err != nil {
					return err
				}
				if len(evs) > 0 {
					fmt.Fprintln(w, "\nHistory:")
					for _, e := range evs {
						line := fmt.Sprintf("  %s  %-9s", e.At.Format("2006-01-02 15:04"), e.Status)
						if e.Note != "" {
							line += "  " + e.Note
						}
						fmt.Fprintln(w, strings.TrimRight(line, " "))
					}
				}
				return nil
			})
		},
	}
	rm := &cobra.Command{
		Use:   "rm <key>",
		Short: "Stop tracking a job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withKey(cmd, args[0], func(a *app.App, key string) error {
				if err := a.Store.Untrack(cmd.Context(), key); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), "untracked", key)
				return nil
			})
		},
	}
	var in app.ManualInput
	var addStatus, addNote string
	add := &cobra.Command{
		Use:   "add <url>",
		Short: "Track a job you found elsewhere (LinkedIn, Internshala, …) by its URL",
		Long: `Track a job from any site. Copy the title and company from the page in
your browser: jobhunt stores the link and what you typed, and never opens the
URL itself (those sites forbid automated access). o in the TUI or "Open the
posting" in the web UI opens it in your browser.

A job added this way is never marked "not listed" unless the same job also
turns up from one of jobhunt's sources.`,
		Example: `  jobhunt tracker add https://www.linkedin.com/jobs/view/123 --title "Backend Intern" --company Acme
  jobhunt tracker add https://internshala.com/internship/detail/xyz -t "Go Intern" -c Acme -l Bengaluru -s applied --note "applied via site"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.ParseStatus(addStatus)
			if err != nil {
				return err
			}
			in.URL = args[0]
			j, err := app.ManualJob(in)
			if err != nil {
				return err
			}
			a, err := app.Open()
			if err != nil {
				return err
			}
			defer a.Close()
			if err := a.Store.TrackManual(cmd.Context(), &j, st, strings.TrimSpace(addNote)); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s — %s (%s) → %s\n", j.Title, j.Company, j.Key, st)
			return nil
		},
	}
	add.Flags().StringVarP(&in.Title, "title", "t", "", "job title, as the posting shows it (required)")
	add.Flags().StringVarP(&in.Company, "company", "c", "", "company name (required)")
	add.Flags().StringVarP(&in.Location, "location", "l", "", `where, e.g. "Bengaluru" or "India"`)
	add.Flags().StringVarP(&addStatus, "status", "s", "saved", "saved, applied, oa, interview, offer or rejected")
	add.Flags().StringVar(&addNote, "note", "", "note to attach")
	_ = add.MarkFlagRequired("title")
	_ = add.MarkFlagRequired("company")

	cmd.AddCommand(listCmd, set, add, show, rm)
	return cmd
}

func newHideCmds() []*cobra.Command {
	mk := func(use, short string, do func(a *app.App, cmd *cobra.Command, key string) error) *cobra.Command {
		return &cobra.Command{
			Use: use + " <key>", Short: short, Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return withKey(cmd, args[0], func(a *app.App, key string) error {
					if err := do(a, cmd, key); err != nil {
						return err
					}
					fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", use, describe(cmd, a, key))
					return nil
				})
			},
		}
	}
	return []*cobra.Command{
		mk("hide", "Hide a job from future results", func(a *app.App, cmd *cobra.Command, k string) error { return a.Store.Hide(cmd.Context(), k) }),
		mk("unhide", "Show a hidden job again", func(a *app.App, cmd *cobra.Command, k string) error { return a.Store.Unhide(cmd.Context(), k) }),
	}
}

func withKey(cmd *cobra.Command, prefix string, fn func(a *app.App, key string) error) error {
	a, err := app.Open()
	if err != nil {
		return err
	}
	defer a.Close()
	key, err := a.Store.ResolveKey(cmd.Context(), prefix)
	if err != nil {
		return err
	}
	return fn(a, key)
}

func describe(cmd *cobra.Command, a *app.App, key string) string {
	if j, _ := a.Store.Job(cmd.Context(), key); j != nil {
		return fmt.Sprintf("%s — %s (%s)", j.Title, j.Company, key)
	}
	return key
}
