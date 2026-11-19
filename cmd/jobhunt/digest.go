package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/itxkarthik/jobhunt/internal/app"
	"github.com/itxkarthik/jobhunt/internal/company"
	"github.com/itxkarthik/jobhunt/internal/feed"
	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/notify"
	"github.com/itxkarthik/jobhunt/internal/search"
	"github.com/itxkarthik/jobhunt/internal/store"
)

func newDigestCmd() *cobra.Command {
	var (
		ff          filterFlags
		so          searchOpts
		cursor      string
		format      string
		toNotify    bool
		toWebhook   bool
		noCompanies bool
		dryRun      bool
		minScore    float64
		maxJobs     int
		maxCos      int
	)
	cmd := &cobra.Command{
		Use:   "digest",
		Short: "What's new since the last digest: matching jobs and companies",
		Long: `Prints the jobs matching your profile and the companies (from the
companies list, filtered by the profile's companies section) that are new
or changed since the last digest. A named cursor records what each digest
contained, so nothing repeats. When nothing is new it prints and sends
nothing, so it suits a timer (docs/examples/jobhunt-digest.timer).

The first digest shows the best of what matches now and records the rest,
so later ones stay short.

Output goes to stdout as Markdown (or JSON), and optionally to a desktop
notification and the signed webhook from config (JOBHUNT_WEBHOOK_URL and
JOBHUNT_WEBHOOK_SECRET), which Telegram, Slack and Discord bridges can
read. Companies with a published contact get an outreach draft; jobhunt
never sends email or applies anywhere.`,
		Example: `  jobhunt digest
  jobhunt digest --notify --webhook
  jobhunt digest --format json --cursor phone
  jobhunt digest --dry-run`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if format != "md" && format != "json" {
				return fmt.Errorf("--format must be md or json, not %q", format)
			}
			if err := store.ValidCursorName(cursor); err != nil {
				return err
			}
			a, err := app.Open()
			if err != nil {
				return err
			}
			defer a.Close()
			p := a.Config.Profile
			if !cmd.Flags().Changed("profile") {
				ff.profile = !p.IsZero() // the digest uses the profile when there is one
			}
			f, err := ff.resolve(cmd, a)
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("min-score") {
				minScore = p.MinScore
			}
			log := cmd.ErrOrStderr()
			dg := &digester{
				search:  func(ctx context.Context) (*search.Result, error) { return so.run(ctx, a, f) },
				cursors: a.Store,
				cursor:  cursor,
				format:  format,
				out:     cmd.OutOrStdout(),
				dryRun:  dryRun,
				opts:    app.DigestOptions{MinScore: minScore, MaxJobs: maxJobs, MaxCompanies: maxCos, Stack: p.Stack},
				newID:   rand.Text,
				now:     time.Now,
			}
			if !noCompanies {
				in := company.FilterInput{}
				if ff.profile {
					in = app.ProfileCompanyInput(p, in)
				}
				dg.companies = func(ctx context.Context) ([]model.Company, error) {
					vs, err := app.Companies(ctx, a.Store, app.CompanyQuery{FilterInput: in})
					cs := make([]model.Company, 0, len(vs))
					for _, v := range vs {
						cs = append(cs, v.Company)
					}
					return cs, err
				}
			}
			if toNotify {
				dg.notify = notify.Send
			}
			if toWebhook {
				if dg.webhook, err = webhookFor("", a, log); err != nil {
					return err
				}
				if dg.webhook == nil {
					return errors.New("--webhook needs feed.webhook_url in config.yaml or JOBHUNT_WEBHOOK_URL")
				}
			}
			return dg.once(cmd.Context())
		},
	}
	ff.register(cmd)
	so.register(cmd)
	fs := cmd.Flags()
	fs.StringVar(&cursor, "cursor", "digest", "named cursor that remembers what earlier digests contained")
	fs.StringVar(&format, "format", "md", "stdout format: md or json")
	fs.BoolVar(&toNotify, "notify", false, "also show a desktop notification")
	fs.BoolVar(&toWebhook, "webhook", false, "also POST the digest as JSON to the webhook from config (signed with its secret)")
	fs.BoolVar(&noCompanies, "no-companies", false, "jobs only")
	fs.BoolVar(&dryRun, "dry-run", false, "show the digest without recording it, so the next one repeats it")
	fs.Float64Var(&minScore, "min-score", 0, "leave out jobs scoring below this (default: profile min_score)")
	fs.IntVar(&maxJobs, "max-jobs", 20, "show at most this many jobs; the rest are counted")
	fs.IntVar(&maxCos, "max-companies", 10, "show at most this many companies; the rest are counted")
	return cmd
}

// digester builds and delivers one digest.
type digester struct {
	search    func(context.Context) (*search.Result, error)
	companies func(context.Context) ([]model.Company, error) // nil: jobs only
	cursors   cursorStore
	cursor    string
	format    string
	out       io.Writer
	notify    func(title, body string) error // nil: off
	webhook   *feed.Webhook                  // nil: off
	dryRun    bool
	opts      app.DigestOptions
	newID     func() string
	now       func() time.Time
}

// webhookDigest is the webhook payload: the digest plus its Markdown, so
// a chat bridge can post it as is.
type webhookDigest struct {
	Type string `json:"type"` // always "digest"
	app.Digest
	Markdown string `json:"markdown"`
}

func (dg *digester) once(ctx context.Context) error {
	delivered, pending, err := dg.cursors.FeedState(ctx, dg.cursor)
	if err != nil {
		return err
	}
	first := len(delivered) == 0 && len(pending) == 0
	res, err := dg.search(ctx)
	if err != nil {
		return err
	}
	var cs []model.Company
	if dg.companies != nil {
		if cs, err = dg.companies(ctx); err != nil {
			return err
		}
	}
	o := dg.opts
	o.Now = dg.now()
	d, keys := app.BuildDigest(res.Jobs, cs, delivered, first, o)
	if d.Empty() {
		return nil // nothing new: say nothing
	}
	md := d.Markdown()
	if dg.format == "json" {
		if err := writeJSON(dg.out, d); err != nil {
			return err
		}
	} else if _, err := io.WriteString(dg.out, md); err != nil {
		return err
	}
	if dg.webhook != nil {
		body, err := json.Marshal(webhookDigest{Type: "digest", Digest: d, Markdown: md})
		if err != nil {
			return err
		}
		if err := dg.webhook.SendAs(ctx, dg.newID(), "application/json", body); err != nil {
			return fmt.Errorf("%w (not recorded; the next digest includes these again)", err)
		}
	}
	if dg.notify != nil {
		title, body := d.Notification()
		if err := dg.notify(title, body); err != nil {
			return fmt.Errorf("notification: %w", err)
		}
	}
	if dg.dryRun {
		return nil
	}
	return dg.cursors.MarkDelivered(ctx, dg.cursor, keys, dg.now())
}
