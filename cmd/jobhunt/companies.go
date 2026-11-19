package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/itxkarthik/jobhunt/internal/app"
	"github.com/itxkarthik/jobhunt/internal/company"
	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources"
)

func newCompaniesCmd() *cobra.Command {
	var (
		q        app.CompanyQuery
		asJSON   bool
		showJobs bool
	)
	list := func(cmd *cobra.Command, _ []string) error {
		a, err := app.Open()
		if err != nil {
			return err
		}
		defer a.Close()
		q.Jobs = asJSON && showJobs
		cs, err := app.Companies(cmd.Context(), a.Store, q)
		if err != nil {
			return err
		}
		w := cmd.OutOrStdout()
		if asJSON {
			return writeJSON(w, cs)
		}
		if len(cs) == 0 {
			fmt.Fprintln(w, "No companies match. Add one: jobhunt companies set \"Acme\" --stage seed --headcount 12 --board greenhouse:acme")
			return nil
		}
		now := time.Now()
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "COMPANY\tSTAGE\tLAST ROUND\tPEOPLE\tWHERE\tOPEN JOBS\tCONTACTS")
		for _, c := range cs {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%d\n", trunc(c.Name, 28), c.Stage, roundSummary(c.LatestRound(), now),
				c.Headcount, place(c.Company), c.OpenJobs, len(c.Contacts))
		}
		return tw.Flush()
	}
	cmd := &cobra.Command{
		Use:   "companies",
		Short: "Track companies to watch: funding, size, job boards and contacts",
		Long: `Companies you watch, usually startups: their funding rounds, headcount,
job boards and contacts the person published themselves. Every round and
contact is stored with the URL it came from. Open jobs are the ones seen in
searches in the last 14 days.`,
		Example: `  jobhunt companies --stage seed,series-a --funded-within 270d --funded-before 90d --headcount 1-10,11-50
  jobhunt companies --hiring mid,senior --country IN
  jobhunt companies set "Acme AI" --stage seed --headcount 12 --board ashby:acme --website https://acme.ai
  jobhunt companies set "Acme AI" --round seed,2500000,USD,2026-09-02,https://acme.ai/blog/seed
  jobhunt companies set "Acme AI" --contact-name "Jo Lee" --contact-role CEO --contact-email jo@acme.ai \
      --contact-source https://news.ycombinator.com/item?id=123
  jobhunt companies show acme --json`,
		Args: cobra.NoArgs,
		RunE: list,
	}
	fs := cmd.Flags()
	fs.StringSliceVar(&q.Stages, "stage", nil, "funding stages: "+joinStr(strs(model.AllStages)))
	fs.StringSliceVar(&q.Headcounts, "headcount", nil, "headcount buckets: "+joinStr(strs(model.AllHeadcounts)))
	fs.StringSliceVarP(&q.Countries, "country", "c", nil, "where the company is based, e.g. IN,US")
	fs.StringSliceVar(&q.Tags, "tag", nil, "only companies with all of these tags")
	fs.StringVar(&q.FundedWithin, "funded-within", "", "latest round raised at most this long ago, e.g. 270d")
	fs.StringVar(&q.FundedBefore, "funded-before", "", "latest round raised at least this long ago, e.g. 90d")
	fs.BoolVar(&q.HasContact, "has-contact", false, "only companies with a published contact")
	fs.StringVarP(&q.Query, "query", "q", "", "name contains this")
	fs.StringSliceVar(&q.Hiring, "hiring", nil, "only companies with open jobs at these levels: intern,entry,mid,senior, or any")
	fs.IntVar(&q.Limit, "limit", 0, "show at most this many (0 = all)")
	fs.BoolVar(&asJSON, "json", false, "print JSON")
	fs.BoolVar(&showJobs, "jobs", false, "with --json, include each company's open jobs")

	cmd.AddCommand(newCompanyShowCmd(), newCompanySetCmd(), newCompanyRmCmd(), newCompanyImportCmd(), newCompanyLinksCmd(), newCompanySyncCmd())
	return cmd
}

func newCompanyShowCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use: "show <name>", Short: "Show a company and its open jobs", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := app.Open()
			if err != nil {
				return err
			}
			defer a.Close()
			c, err := app.FindCompany(cmd.Context(), a.Store, args[0])
			if err != nil {
				return err
			}
			jobs, err := a.Store.CompanyJobs(cmd.Context(), []string{c.Key}, time.Now().Add(-app.OpenJobWindow))
			if err != nil {
				return err
			}
			v := app.CompanyView{Company: *c, OpenJobs: len(jobs[c.Key]), Jobs: jobs[c.Key]}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), v)
			}
			printCompany(cmd.OutOrStdout(), v, time.Now())
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func newCompanySetCmd() *cobra.Command {
	var (
		c       model.Company
		stage   string
		people  string
		boards  []string
		rounds  []string
		ct      model.Contact
		rm      company.Removal
		asJSON  bool
		checked bool
		noCheck bool
	)
	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Add a company or update it; only the fields you pass change",
		Long: `Add a company or update it. Only the fields you pass change; lists
(rounds, boards, contacts, tags) are merged.

A round is "stage,amount,currency,date,source-url"; leave parts empty with
commas, e.g. "seed,,,2026-09-02,https://…". A contact needs --contact-source:
the page where the person published it.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c.Name, c.Stage, c.Headcount = args[0], model.Stage(stage), model.Headcount(people)
			c.Sources = []string{"manual"}
			for _, b := range boards {
				bd, err := company.ParseBoard(b)
				if err != nil {
					return err
				}
				c.Boards = append(c.Boards, bd)
			}
			for _, r := range rounds {
				rd, err := parseRound(r)
				if err != nil {
					return err
				}
				c.Rounds = append(c.Rounds, rd)
			}
			if ct != (model.Contact{}) {
				c.Contacts = []model.Contact{ct}
			}
			if checked {
				c.CheckedAt = time.Now()
			}
			a, err := app.Open()
			if err != nil {
				return err
			}
			defer a.Close()
			if !noCheck {
				if err := app.AddBoards(cmd.Context(), a.Store, c, a.CheckBoard); err != nil {
					return fmt.Errorf("%w (use --no-check to save it anyway)", err)
				}
			}
			got, created, err := app.UpsertCompany(cmd.Context(), a.Store, c, rm)
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), got)
			}
			verb := "updated"
			if created {
				verb = "added"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s (%s, %s people)\n", verb, got.Name, got.Stage, got.Headcount)
			return nil
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&c.Website, "website", "", "company website")
	fs.StringVarP(&c.Country, "country", "c", "", "where it's based, e.g. IN")
	fs.StringVar(&c.City, "city", "", "city, e.g. Bengaluru")
	fs.StringVar(&stage, "stage", "", "funding stage: "+joinStr(strs(model.AllStages)))
	fs.StringVar(&people, "headcount", "", "people, as a number or bucket ("+joinStr(strs(model.AllHeadcounts))+")")
	fs.StringVar(&c.HeadcountSource, "headcount-source", "", `where the headcount came from: a URL, or "estimate"`)
	fs.StringSliceVar(&boards, "board", nil, "job board as ats:slug, e.g. greenhouse:acme; fetched once to check it, then searched like companies.yaml boards")
	fs.BoolVar(&noCheck, "no-check", false, "save new boards without fetching them first")
	fs.BoolVar(&c.Remote, "remote", false, "the company hires remotely beyond its country, so its board is searched for remote roles too")
	fs.StringArrayVar(&rounds, "round", nil, `funding round "stage,amount,currency,date,source-url" (repeatable)`)
	fs.StringVar(&ct.Name, "contact-name", "", "contact's name")
	fs.StringVar(&ct.Role, "contact-role", "", "contact's role, e.g. CEO")
	fs.StringVar(&ct.Email, "contact-email", "", "contact's email, as they published it")
	fs.StringVar(&ct.URL, "contact-url", "", "contact's profile, e.g. their X or personal page")
	fs.StringVar(&ct.SourceURL, "contact-source", "", "page where the person published this contact (required with a contact)")
	fs.StringSliceVar(&c.Tags, "tag", nil, "tags to add")
	fs.StringVar(&c.Notes, "note", "", "replace the notes")
	fs.BoolVar(&checked, "checked", false, "mark the record as checked now")
	fs.StringSliceVar(&rm.Contacts, "rm-contact", nil, "remove contacts by email, URL or name")
	fs.StringSliceVar(&rm.Boards, "rm-board", nil, "remove boards (ats:slug)")
	fs.StringSliceVar(&rm.Tags, "rm-tag", nil, "remove tags")
	fs.BoolVar(&asJSON, "json", false, "print the stored record as JSON")
	return cmd
}

func newCompanyRmCmd() *cobra.Command {
	return &cobra.Command{
		Use: "rm <name>", Aliases: []string{"delete"}, Short: "Remove a company", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := app.Open()
			if err != nil {
				return err
			}
			defer a.Close()
			c, err := app.FindCompany(cmd.Context(), a.Store, args[0])
			if err != nil {
				return err
			}
			if _, err := a.Store.DeleteCompany(cmd.Context(), c.Key); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "removed", c.Name)
			return nil
		},
	}
}

// parseRound reads "stage,amount,currency,date,source-url".
func parseRound(s string) (model.Round, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 5 {
		return model.Round{}, fmt.Errorf("round %q: want stage,amount,currency,date,source-url", s)
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	r := model.Round{Stage: model.Stage(parts[0]), Currency: parts[2], SourceURL: parts[4]}
	if parts[1] != "" {
		amt, err := strconv.ParseFloat(strings.ReplaceAll(parts[1], "_", ""), 64)
		if err != nil {
			return r, fmt.Errorf("round amount %q: %w", parts[1], err)
		}
		r.Amount = amt
	}
	if parts[3] != "" {
		d, err := time.Parse(time.DateOnly, parts[3])
		if err != nil {
			return r, fmt.Errorf("round date %q: want YYYY-MM-DD", parts[3])
		}
		r.Date = d
	}
	return r, nil
}

func printCompany(w io.Writer, v app.CompanyView, now time.Time) {
	c := v.Company
	fmt.Fprintf(w, "%s  (%s)\n", c.Name, c.Key)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	row := func(k, val string) {
		if val != "" {
			fmt.Fprintf(tw, "  %s\t%s\n", k, val)
		}
	}
	row("website", c.Website)
	row("based in", place(c))
	row("stage", string(c.Stage))
	hc := string(c.Headcount)
	if c.HeadcountSource != "" {
		hc += " (" + c.HeadcountSource + ")"
	}
	row("people", hc)
	var bs []string
	for _, b := range c.Boards {
		bs = append(bs, b.String())
	}
	row("boards", strings.Join(bs, ", "))
	row("tags", strings.Join(c.Tags, ", "))
	row("sources", strings.Join(c.Sources, ", "))
	if !c.CheckedAt.IsZero() {
		row("checked", app.Since(c.CheckedAt, now)+" ago")
	}
	tw.Flush()
	if len(c.Rounds) > 0 {
		fmt.Fprintln(w, "\nRounds")
		for i := range c.Rounds {
			fmt.Fprintf(w, "  %s  %s\n", roundSummary(&c.Rounds[i], now), c.Rounds[i].SourceURL)
		}
	}
	if len(c.Contacts) > 0 {
		fmt.Fprintln(w, "\nContacts (as published; jobhunt never sends email)")
		for _, ct := range c.Contacts {
			who := strings.Join(sources.NonEmpty(ct.Name, ct.Role), ", ")
			fmt.Fprintf(w, "  %s  %s\n    source: %s\n", who, strings.Join(sources.NonEmpty(ct.Email, ct.URL), "  "), ct.SourceURL)
		}
	}
	if c.Notes != "" {
		fmt.Fprintf(w, "\nNotes\n  %s\n", strings.ReplaceAll(c.Notes, "\n", "\n  "))
	}
	fmt.Fprintf(w, "\nOpen jobs (seen in the last %d days): %d\n", int(app.OpenJobWindow.Hours()/24), v.OpenJobs)
	for _, j := range v.Jobs {
		fmt.Fprintf(w, "  %-8s  %-10s  %s\n    %s\n", j.Key[:min(8, len(j.Key))], j.Experience, trunc(j.Title, 60), j.URL)
	}
}

// roundSummary renders "seed 2.5M USD · 2026-09-02 (78d ago)".
func roundSummary(r *model.Round, now time.Time) string {
	if r == nil {
		return "-"
	}
	var parts []string
	if r.Stage != "" && r.Stage != model.StageUnknown {
		parts = append(parts, string(r.Stage))
	}
	if r.Amount > 0 {
		parts = append(parts, strings.TrimSpace(money(r.Amount)+" "+r.Currency))
	}
	s := strings.Join(parts, " ")
	if !r.Date.IsZero() {
		if s != "" {
			s += " · "
		}
		s += r.Date.Format(time.DateOnly) + " (" + app.Since(r.Date, now) + " ago)"
	}
	if s == "" {
		return "-"
	}
	return s
}

func money(v float64) string {
	// One decimal at most: 2.5M, 375M, 1.2B.
	short := func(x float64) string { return strconv.FormatFloat(math.Round(x*10)/10, 'f', -1, 64) }
	switch {
	case v >= 1e9:
		return short(v/1e9) + "B"
	case v >= 1e6:
		return short(v/1e6) + "M"
	case v >= 1e3:
		return short(v/1e3) + "K"
	}
	return short(v)
}

func place(c model.Company) string {
	return strings.Join(sources.NonEmpty(c.City, c.Country), ", ")
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
