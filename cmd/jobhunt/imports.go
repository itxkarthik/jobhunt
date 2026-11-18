package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/spf13/cobra"

	"github.com/itxkarthik/jobhunt/internal/app"
	"github.com/itxkarthik/jobhunt/internal/sources/edgar"
)

func newCompanyImportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Fill the companies list from official sources",
		Long: `Importers add companies from sources jobhunt may fetch automatically.
Each run only fetches what's new since the last one; --dry-run shows what
would be added without saving anything.`,
	}
	cmd.AddCommand(newImportEDGARCmd())
	return cmd
}

func newImportEDGARCmd() *cobra.Command {
	var (
		since          string
		o              edgar.Options
		dryRun, asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "edgar",
		Short: "US funding rounds from SEC Form D filings",
		Long: `Reads EDGAR's daily index of Form D filings (private rounds a US company
must report within 15 days of the first sale) and adds the likely startups:
corporations in tech industries, with the amount raised, the date, the city
and the executive officers named in the filing, each linked to the filing.

EDGAR asks every client to identify itself, so set
importers.edgar.user_agent ("Your Name you@example.com") in config.yaml or
JOBHUNT_EDGAR_USER_AGENT first. Requests stay at 5 a second.`,
		Example: `  jobhunt companies import edgar --since 14d
  jobhunt companies import edgar --dry-run --industry any
  jobhunt companies import edgar --since 30d --redo   # read a month again
  jobhunt companies --stage unknown --funded-within 30d --country US   # what came in`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, err := app.ParseAge(since)
			if err != nil {
				return err
			}
			o.Since = d
			a, err := app.Open()
			if err != nil {
				return err
			}
			defer a.Close()
			im, err := a.EDGARImporter()
			if err != nil {
				return err
			}
			if len(o.Industries) == 0 {
				o.Industries = a.Config.Importers.EDGAR.Industries
			}
			o.AllEntities = o.AllEntities || a.Config.Importers.EDGAR.AllEntities
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			sum, err := app.ImportEDGAR(ctx, a.Store, im, o, dryRun)
			if sum != nil {
				if asJSON {
					if jerr := writeJSON(cmd.OutOrStdout(), sum); jerr != nil {
						return jerr
					}
				} else {
					printImport(cmd.OutOrStdout(), sum, "daily indexes", "likely startups")
				}
			}
			return err
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&since, "since", "7d", "look back this far, e.g. 14d (each weekday is one index request)")
	fs.IntVar(&o.MaxFilings, "max-filings", 200, "fetch at most this many filings per run; the rest wait for the next run")
	fs.StringSliceVar(&o.Industries, "industry", nil, `Form D industry groups to keep, or "any" (default from config: Computers, Telecommunications, Other Technology, Other)`)
	fs.BoolVar(&o.AllEntities, "all-entities", false, "keep LLCs and LPs too, not just corporations (mostly funds)")
	fs.BoolVar(&dryRun, "dry-run", false, "show what would be added; save nothing")
	fs.BoolVar(&o.Redo, "redo", false, "read the whole --since window again: forget what earlier runs finished there (a run cut short by --max-filings continues on the next run)")
	fs.BoolVar(&asJSON, "json", false, "print the summary as JSON")
	return cmd
}

func printImport(w io.Writer, s *app.ImportSummary, readWhat, foundWhat string) {
	fmt.Fprintf(w, "%s: read %d %s, %d %s, fetched %d\n", s.Source, s.Read, readWhat, s.Found, foundWhat, s.Fetched)
	verb := "added"
	if s.DryRun {
		verb = "would add or update"
	}
	list := func(label string, names []string) {
		if len(names) == 0 {
			return
		}
		fmt.Fprintf(w, "%s %d: %s\n", label, len(names), strings.Join(names, ", "))
	}
	list(verb, s.Added)
	list("updated", s.Updated)
	list("skipped", s.Skipped)
	for _, e := range s.Errors {
		fmt.Fprintln(w, "error:", e)
	}
	if !s.Complete {
		fmt.Fprintln(w, "stopped early; run again to continue")
	}
	if len(s.Added)+len(s.Updated) == 0 && len(s.Errors) == 0 {
		fmt.Fprintln(w, "nothing new")
	}
}
