package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/itxkarthik/jobhunt/internal/app"
	"github.com/itxkarthik/jobhunt/internal/config"
	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/search"
	"github.com/itxkarthik/jobhunt/internal/sources/links"
)

// filterFlags holds raw flag values before they're parsed into model.Filters.
type filterFlags struct {
	countries, modes, types, areas, experience []string
	keywords, exclude                          []string
	postedWithin                               string
	visa                                       bool
	minSalary                                  float64
	currency                                   string
	strictCountry, includeUnknown              bool
}

func (ff *filterFlags) register(cmd *cobra.Command) {
	fs := cmd.Flags()
	fs.StringSliceVarP(&ff.countries, "country", "c", nil, "countries: IN,US,JP,UK,DE,CA,SG,NL,AU,AE,remote-global (default from config: IN)")
	fs.StringSliceVarP(&ff.modes, "mode", "m", nil, "work modes: remote,onsite,hybrid (default all)")
	fs.StringSliceVarP(&ff.types, "type", "t", nil, "job types: internship,full-time,part-time,contract,new-grad")
	fs.StringSliceVarP(&ff.areas, "area", "a", nil, "CS areas: backend,frontend,full-stack,mobile,ml-ai,data-engineering,data-science,devops,systems,hpc-gpu,security,embedded,qa,research,blockchain,gamedev")
	fs.StringSliceVarP(&ff.keywords, "keywords", "k", nil, "keywords; a job must mention at least one (e.g. Go,Kubernetes)")
	fs.StringSliceVarP(&ff.exclude, "exclude", "x", nil, "drop jobs mentioning any of these (e.g. senior,\"10+ years\")")
	fs.StringSliceVarP(&ff.experience, "experience", "e", nil, "experience: intern,entry,mid,senior")
	fs.StringVar(&ff.postedWithin, "posted-within", "", "only jobs posted within 24h, 3d, 7d, 30d")
	fs.BoolVar(&ff.visa, "visa", false, "only jobs that mention visa sponsorship")
	fs.Float64Var(&ff.minSalary, "min-salary", 0, "minimum advertised salary (annual, in --currency)")
	fs.StringVar(&ff.currency, "currency", "", "currency for --min-salary, e.g. INR, USD")
	fs.BoolVar(&ff.strictCountry, "strict-country", false, "exclude remote jobs that are open worldwide/regionally rather than to the country itself")
	fs.BoolVar(&ff.includeUnknown, "include-unknown", false, "keep jobs whose country/type/area couldn't be determined")
}

func (ff *filterFlags) toFilters(cfg *config.Config) (model.Filters, error) {
	var f model.Filters
	countries := ff.countries
	if len(countries) == 0 {
		countries = cfg.Defaults.Countries
	}
	for _, c := range countries {
		code, ok := model.NormalizeCountry(c)
		if !ok {
			return f, fmt.Errorf("unknown country %q", c)
		}
		f.Countries = append(f.Countries, code)
	}
	modes := orDefault(ff.modes, cfg.Defaults.Modes)
	for _, s := range modes {
		m, err := model.ParseWorkMode(s)
		if err != nil {
			return f, err
		}
		f.Modes = append(f.Modes, m)
	}
	for _, s := range orDefault(ff.types, cfg.Defaults.Types) {
		t, err := model.ParseJobType(s)
		if err != nil {
			return f, err
		}
		f.Types = append(f.Types, t)
	}
	for _, s := range orDefault(ff.areas, cfg.Defaults.Areas) {
		a, err := model.ParseArea(s)
		if err != nil {
			return f, err
		}
		f.Areas = append(f.Areas, a)
	}
	for _, s := range ff.experience {
		e, err := model.ParseExperience(s)
		if err != nil {
			return f, err
		}
		f.Experience = append(f.Experience, e)
	}
	f.Keywords, f.Exclude = trimAll(ff.keywords), trimAll(ff.exclude)
	if ff.postedWithin != "" {
		d, err := parseAge(ff.postedWithin)
		if err != nil {
			return f, err
		}
		f.PostedWithin = d
	}
	f.VisaOnly, f.MinSalary = ff.visa, ff.minSalary
	f.Currency = strings.ToUpper(ff.currency)
	f.StrictCountry, f.IncludeUnknown = ff.strictCountry, ff.includeUnknown
	return f, nil
}

// parseAge accepts Go durations plus a "d" (days) suffix: 24h, 3d, 7d, 30d.
func parseAge(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("bad --posted-within %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("bad --posted-within %q (use 24h, 3d, 7d, 30d)", s)
	}
	return d, nil
}

func orDefault(v, def []string) []string {
	if len(v) > 0 {
		return v
	}
	return def
}

func trimAll(xs []string) []string {
	var out []string
	for _, x := range xs {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

func newSearchCmd() *cobra.Command {
	var (
		ff      filterFlags
		limit   int
		refresh bool
		noLinks bool
		sortBy  string
	)
	cmd := &cobra.Command{
		Use:   "search",
		Short: "Search all sources and print matching jobs",
		Example: `  jobhunt search --country IN --type internship --area backend
  jobhunt search -c JP,remote-global -t new-grad -k Go,Kubernetes --visa
  jobhunt search -c US -a hpc-gpu -k CUDA -x senior,staff --posted-within 7d`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := app.Open()
			if err != nil {
				return err
			}
			defer a.Close()
			f, err := ff.toFilters(a.Config)
			if err != nil {
				return err
			}
			if limit == 0 {
				limit = a.Config.Defaults.Limit
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			eng := a.Engine()
			eng.Refresh = refresh
			eng.SortBy = sortBy
			fmt.Fprintf(cmd.ErrOrStderr(), "Searching %d sources…\n", len(a.Sources))
			res, err := eng.Run(ctx, f)
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
	cmd.Flags().BoolVar(&refresh, "refresh", false, "refetch sources, except ones whose terms impose a minimum interval")
	cmd.Flags().StringVar(&sortBy, "sort", "score", "sort by score or date")
	cmd.Flags().BoolVar(&noLinks, "no-links", false, "don't print search links for sites that can't be queried")
	return cmd
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
			loc := j.City
			if loc == "" {
				loc = j.Location
			}
			if loc == "" {
				loc = strings.Join(j.Countries, ",")
			}
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
