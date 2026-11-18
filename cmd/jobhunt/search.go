package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/itxkarthik/jobhunt/internal/app"
	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/search"
	"github.com/itxkarthik/jobhunt/internal/sources/links"
)

// filterFlags holds raw flag values before they're parsed into model.Filters.
type filterFlags struct {
	in     app.FilterInput
	preset string
}

func (ff *filterFlags) register(cmd *cobra.Command) {
	fs := cmd.Flags()
	fs.StringSliceVarP(&ff.in.Countries, "country", "c", nil, "countries: IN,US,JP,UK,DE,CA,SG,NL,AU,AE,remote-global, or any (default from config: IN)")
	fs.StringSliceVarP(&ff.in.Modes, "mode", "m", nil, "work modes: remote,onsite,hybrid (default all)")
	fs.StringSliceVarP(&ff.in.Types, "type", "t", nil, "job types: internship,full-time,part-time,contract,new-grad, or any (default from config)")
	fs.StringSliceVarP(&ff.in.Areas, "area", "a", nil, "CS areas: backend,frontend,full-stack,mobile,ml-ai,data-engineering,data-science,devops,systems,hpc-gpu,security,embedded,qa,research,blockchain,gamedev")
	fs.StringSliceVarP(&ff.in.Keywords, "keywords", "k", nil, "keywords; a job must mention at least one (e.g. Go,Kubernetes)")
	fs.StringSliceVarP(&ff.in.Exclude, "exclude", "x", nil, "drop jobs mentioning any of these (e.g. senior,\"10+ years\")")
	fs.StringSliceVarP(&ff.in.Experience, "experience", "e", nil, "experience: intern,entry,mid,senior")
	fs.StringVar(&ff.in.PostedWithin, "posted-within", "", "only jobs posted within 24h, 3d, 7d, 30d")
	fs.BoolVar(&ff.in.Visa, "visa", false, "only jobs that mention visa sponsorship")
	fs.Float64Var(&ff.in.MinSalary, "min-salary", 0, "minimum advertised salary (annual, in --currency)")
	fs.StringVar(&ff.in.Currency, "currency", "", "currency for --min-salary, e.g. INR, USD")
	fs.BoolVar(&ff.in.StrictCountry, "strict-country", false, "exclude remote jobs that are open worldwide/regionally rather than to the country itself")
	fs.BoolVar(&ff.in.IncludeUnknown, "include-unknown", false, "keep jobs whose country/type/area couldn't be determined")
	fs.StringSliceVar(&ff.in.CompanyStages, "company-stage", nil, "only jobs at listed companies (jobhunt companies) at these funding stages, e.g. seed,series-a")
	fs.StringSliceVar(&ff.in.CompanyHeadcounts, "company-headcount", nil, "only jobs at listed companies of these sizes, e.g. 1-10,11-50")
	fs.StringVarP(&ff.preset, "preset", "p", "", "start from a saved preset; flags you pass override it")
}

// resolve builds filters from flags, starting from --preset if given. Only
// flags set explicitly on the command line override the preset.
func (ff *filterFlags) resolve(cmd *cobra.Command, a *app.App) (model.Filters, error) {
	f, err := app.ParseFilters(a.Config.Defaults, ff.in)
	if err != nil || ff.preset == "" {
		return f, err
	}
	p, err := a.Store.Preset(cmd.Context(), ff.preset)
	if err != nil {
		return f, err
	}
	base := p.Filters
	set := cmd.Flags().Changed
	overrides := []struct {
		flag  string
		apply func()
	}{
		{"country", func() { base.Countries = f.Countries }},
		{"mode", func() { base.Modes = f.Modes }},
		{"type", func() { base.Types = f.Types }},
		{"area", func() { base.Areas = f.Areas }},
		{"keywords", func() { base.Keywords = f.Keywords }},
		{"exclude", func() { base.Exclude = f.Exclude }},
		{"experience", func() { base.Experience = f.Experience }},
		{"posted-within", func() { base.PostedWithin = f.PostedWithin }},
		{"visa", func() { base.VisaOnly = f.VisaOnly }},
		{"min-salary", func() { base.MinSalary = f.MinSalary }},
		{"currency", func() { base.Currency = f.Currency }},
		{"strict-country", func() { base.StrictCountry = f.StrictCountry }},
		{"include-unknown", func() { base.IncludeUnknown = f.IncludeUnknown }},
		{"company-stage", func() { base.CompanyStages = f.CompanyStages }},
		{"company-headcount", func() { base.CompanyHeadcounts = f.CompanyHeadcounts }},
	}
	for _, o := range overrides {
		if set(o.flag) {
			o.apply()
		}
	}
	return base, nil
}

func newSearchCmd() *cobra.Command {
	var (
		ff      filterFlags
		limit   int
		noLinks bool
		so      searchOpts
	)
	cmd := &cobra.Command{
		Use:   "search",
		Short: "Search all sources and print matching jobs",
		Example: `  jobhunt search --country IN --type internship --area backend
  jobhunt search -c JP,remote-global -t new-grad -k Go,Kubernetes --visa
  jobhunt search -c US -a hpc-gpu -k CUDA -x senior,staff --posted-within 7d
  jobhunt search --preset india-backend --new-only`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := app.Open()
			if err != nil {
				return err
			}
			defer a.Close()
			f, err := ff.resolve(cmd, a)
			if err != nil {
				return err
			}
			if limit == 0 {
				limit = a.Config.Defaults.Limit
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Searching %d sources…\n", len(a.Sources))
			res, err := so.run(cmd.Context(), a, f)
			if err != nil {
				return err
			}
			printResults(cmd.OutOrStdout(), res, limit, time.Now())
			if !noLinks {
				printLinks(cmd.OutOrStdout(), links.For(f))
			}
			return nil
		},
	}
	ff.register(cmd)
	cmd.Flags().IntVarP(&limit, "limit", "n", 0, "max rows to print (default from config: 50; -1 for all)")
	so.register(cmd)
	cmd.Flags().BoolVar(&noLinks, "no-links", false, "don't print search links for sites that can't be queried")
	return cmd
}

// searchOpts are engine flags shared by search, export, and watch.
type searchOpts struct {
	refresh, newOnly, showHidden bool
	sortBy                       string
}

func (o *searchOpts) register(cmd *cobra.Command) {
	fs := cmd.Flags()
	fs.BoolVar(&o.refresh, "refresh", false, "refetch sources, except ones whose terms impose a minimum interval")
	fs.StringVar(&o.sortBy, "sort", "score", "sort by score or date")
	fs.BoolVar(&o.newOnly, "new-only", false, "only jobs not seen in any earlier search")
	fs.BoolVar(&o.showHidden, "show-hidden", false, "include jobs you hid")
}

func (o *searchOpts) run(ctx context.Context, a *app.App, f model.Filters) (*search.Result, error) {
	eng := a.Engine()
	eng.Refresh, eng.SortBy, eng.NewOnly, eng.ShowHidden = o.refresh, o.sortBy, o.newOnly, o.showHidden
	return eng.Run(ctx, f)
}

func printResults(w io.Writer, res *search.Result, limit int, now time.Time) {
	jobs := res.Jobs
	shown := len(jobs)
	if limit > 0 && shown > limit {
		shown = limit
	}
	if shown == 0 {
		fmt.Fprintln(w, "No matching jobs.")
	} else {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "#\tSCORE\tKEY\tAGE\tTITLE\tCOMPANY\tLOCATION\tMODE\tTYPE\tAREAS\tSOURCE\tURL")
		for i, j := range jobs[:shown] {
			loc := location(&j)
			mark := ""
			if j.New {
				mark = "★"
			}
			fmt.Fprintf(tw, "%d%s\t%.0f\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				i+1, mark, j.Score, j.Key, app.Since(j.PostedAt, now), trunc(j.Title, 48), trunc(j.Company, 22), trunc(loc, 22),
				j.WorkMode, j.JobType, trunc(joinAreas(j.Areas), 20), strings.Join(j.Sources, ","), j.URL)
		}
		tw.Flush()
	}
	fmt.Fprintf(w, "\n%d matching of %d fetched", len(jobs), res.Total)
	if shown < len(jobs) {
		fmt.Fprintf(w, " (showing %d; use --limit -1 for all)", shown)
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, "\nSources:")
	for _, r := range res.Reports {
		line := fmt.Sprintf("  %-10s %-8s %4d", r.Source, r.Status, r.Count)
		if r.Status == search.StatusOK {
			line += fmt.Sprintf("  (%s)", r.Duration.Round(time.Millisecond))
		}
		if r.Err != "" {
			line += "  " + r.Err
		}
		fmt.Fprintln(w, line)
		if (r.Status == search.StatusSkipped || r.Status == search.StatusError) && r.Link != "" {
			fmt.Fprintf(w, "  %-10s open:    %s\n", "", r.Link)
		}
	}
}

func printLinks(w io.Writer, ls []links.Link) {
	if len(ls) == 0 {
		return
	}
	fmt.Fprintln(w, "\nAlso search (open in browser; these sites don't allow automated access):")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, l := range ls {
		fmt.Fprintf(tw, "  %s\t%s\n", l.Site, l.URL)
	}
	tw.Flush()
}

func joinAreas(as []model.Area) string {
	if len(as) == 0 {
		return "-"
	}
	s := make([]string, len(as))
	for i, a := range as {
		s[i] = string(a)
	}
	return strings.Join(s, ",")
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func location(j *model.Job) string {
	switch {
	case j.City != "":
		return j.City
	case j.Location != "":
		return j.Location
	}
	return strings.Join(j.Countries, ",")
}
