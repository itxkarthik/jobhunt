package app

import (
	"golang.org/x/time/rate"

	"github.com/itxkarthik/jobhunt/internal/config"
	"github.com/itxkarthik/jobhunt/internal/sources"
	"github.com/itxkarthik/jobhunt/internal/sources/adzuna"
	"github.com/itxkarthik/jobhunt/internal/sources/arbeitnow"
	"github.com/itxkarthik/jobhunt/internal/sources/ats"
	"github.com/itxkarthik/jobhunt/internal/sources/file"
	"github.com/itxkarthik/jobhunt/internal/sources/himalayas"
	"github.com/itxkarthik/jobhunt/internal/sources/hn"
	"github.com/itxkarthik/jobhunt/internal/sources/jsearch"
	"github.com/itxkarthik/jobhunt/internal/sources/remoteok"
	"github.com/itxkarthik/jobhunt/internal/sources/remotive"
	"github.com/itxkarthik/jobhunt/internal/sources/wwr"
	"github.com/itxkarthik/jobhunt/internal/sources/xposts"
)

// SourceInfo describes a source for `jobhunt sources`, whether or not it's enabled.
type SourceInfo struct {
	Name           string
	Description    string
	DefaultEnabled bool
	Key            string // credential needed, "" if none
	build          func(cfg *config.Config, sc config.SourceConfig, c *sources.Client, boards *ats.Boards) sources.Source
}

// Catalog lists every source in display order. Adding a source means
// writing its package and adding one entry here.
var Catalog = []SourceInfo{
	{"greenhouse", "Company boards on Greenhouse (companies.yaml)", true, "", func(_ *config.Config, _ config.SourceConfig, c *sources.Client, b *ats.Boards) sources.Source {
		return ats.NewShared("greenhouse", b, c, "")
	}},
	{"lever", "Company boards on Lever (companies.yaml)", true, "", func(_ *config.Config, _ config.SourceConfig, c *sources.Client, b *ats.Boards) sources.Source {
		return ats.NewShared("lever", b, c, "")
	}},
	{"ashby", "Company boards on Ashby (companies.yaml)", true, "", func(_ *config.Config, _ config.SourceConfig, c *sources.Client, b *ats.Boards) sources.Source {
		return ats.NewShared("ashby", b, c, "")
	}},
	{"workable", "Company boards on Workable (companies.yaml)", true, "", func(_ *config.Config, _ config.SourceConfig, c *sources.Client, b *ats.Boards) sources.Source {
		return ats.NewShared("workable", b, c, "")
	}},
	{"recruitee", "Company boards on Recruitee (companies.yaml)", true, "", func(_ *config.Config, _ config.SourceConfig, c *sources.Client, b *ats.Boards) sources.Source {
		return ats.NewShared("recruitee", b, c, "")
	}},
	{"hn", "Hacker News \"Who is hiring?\" (latest thread)", true, "", func(_ *config.Config, _ config.SourceConfig, c *sources.Client, _ *ats.Boards) sources.Source {
		return hn.New(c)
	}},
	{"himalayas", "Himalayas remote jobs API", true, "", func(_ *config.Config, sc config.SourceConfig, c *sources.Client, _ *ats.Boards) sources.Source {
		return himalayas.New(sc.MaxPages, c)
	}},
	{"remoteok", "Remote OK API", true, "", func(_ *config.Config, _ config.SourceConfig, c *sources.Client, _ *ats.Boards) sources.Source {
		return remoteok.New(c)
	}},
	{"wwr", "We Work Remotely RSS", true, "", func(_ *config.Config, _ config.SourceConfig, c *sources.Client, _ *ats.Boards) sources.Source {
		return wwr.New(c)
	}},
	{"remotive", "Remotive API (24h delayed, ~10 jobs)", true, "", func(_ *config.Config, _ config.SourceConfig, c *sources.Client, _ *ats.Boards) sources.Source {
		return remotive.New(c)
	}},
	{"arbeitnow", "Arbeitnow API (mostly Germany/EU)", true, "", func(_ *config.Config, sc config.SourceConfig, c *sources.Client, _ *ats.Boards) sources.Source {
		return arbeitnow.New(sc.MaxPages, c)
	}},
	{"adzuna", "Adzuna API (paid: 25 trial calls, then £8/mo)", false, "app_id + app_key", func(_ *config.Config, sc config.SourceConfig, c *sources.Client, _ *ats.Boards) sources.Source {
		return adzuna.New(sc.AppID, sc.AppKey, sc.MaxPages, c)
	}},
	{"jsearch", "JSearch / Google Jobs incl. LinkedIn & Indeed (OpenWeb Ninja or RapidAPI key)", false, "api_key", func(_ *config.Config, sc config.SourceConfig, c *sources.Client, _ *ats.Boards) sources.Source {
		return jsearch.New(sc.APIKey, sc.MaxPages, c)
	}},
	{"x", "X recent-search API (pay-per-use)", false, "bearer_token", func(_ *config.Config, sc config.SourceConfig, c *sources.Client, _ *ats.Boards) sources.Source {
		return xposts.New(sc.BearerToken, sc.MaxResults, c)
	}},
	{"file", "Jobs from local NDJSON files, one per line (sources.file.paths)", false, "", func(_ *config.Config, sc config.SourceConfig, _ *sources.Client, _ *ats.Boards) sources.Source {
		return file.New(sc.Paths)
	}},
}

// BuildSources constructs every enabled source with its own HTTP client and
// rate limiter.
func BuildSources(cfg *config.Config, boards *ats.Boards) []sources.Source {
	var out []sources.Source
	for _, info := range Catalog {
		sc := cfg.Source(info.Name)
		if !sc.IsEnabled(info.DefaultEnabled) {
			continue
		}
		c := sources.NewClient(cfg.UserAgent, rate.Limit(1))
		src := info.build(cfg, sc, c, boards)
		if rl, ok := src.(sources.RateLimited); ok {
			c.Limiter = rate.NewLimiter(rl.Rate(), 1)
		}
		out = append(out, src)
	}
	for _, src := range out {
		if fs, ok := src.(*file.Source); ok {
			for _, info := range Catalog {
				if info.Name != fs.Name() {
					fs.Reserved = append(fs.Reserved, info.Name)
				}
			}
		}
	}
	return out
}
