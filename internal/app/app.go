// Package app wires config, store, sources, and the search engine together
// for the CLI and TUI.
package app

import (
	"strconv"
	"time"

	"golang.org/x/time/rate"

	"github.com/itxkarthik/jobhunt/internal/classify"
	"github.com/itxkarthik/jobhunt/internal/config"
	"github.com/itxkarthik/jobhunt/internal/search"
	"github.com/itxkarthik/jobhunt/internal/sources"
	"github.com/itxkarthik/jobhunt/internal/sources/adzuna"
	"github.com/itxkarthik/jobhunt/internal/sources/remotive"
	"github.com/itxkarthik/jobhunt/internal/store"
)

type App struct {
	Config     *config.Config
	Store      *store.Store
	Classifier *classify.Classifier
	Sources    []sources.Source
}

// Open loads config, opens the DB, and builds the enabled sources.
func Open() (*App, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	cl, err := classify.Load(cfg.KeywordsFile)
	if err != nil {
		st.Close()
		return nil, err
	}
	return &App{Config: cfg, Store: st, Classifier: cl, Sources: BuildSources(cfg)}, nil
}

func (a *App) Close() error { return a.Store.Close() }

// BuildSources constructs every enabled source with its own HTTP client and
// rate limiter. Disabled-by-default sources (paid APIs) need enabled: true.
func BuildSources(cfg *config.Config) []sources.Source {
	client := func() *sources.Client { return sources.NewClient(cfg.UserAgent, rate.Limit(1)) }
	var out []sources.Source
	add := func(name string, defEnabled bool, mk func(sc config.SourceConfig, c *sources.Client) sources.Source) {
		sc := cfg.Source(name)
		if !sc.IsEnabled(defEnabled) {
			return
		}
		c := client()
		src := mk(sc, c)
		if rl, ok := src.(sources.RateLimited); ok {
			c.Limiter = rate.NewLimiter(rl.Rate(), 1)
		}
		out = append(out, src)
	}
	// Adzuna is off by default: new keys only get 25 trial calls, then £8/month.
	add("adzuna", false, func(sc config.SourceConfig, c *sources.Client) sources.Source {
		return adzuna.New(sc.AppID, sc.AppKey, sc.MaxPages, c)
	})
	add("remotive", true, func(_ config.SourceConfig, c *sources.Client) sources.Source {
		return remotive.New(c)
	})
	return out
}

// Engine returns a search engine over the app's sources.
func (a *App) Engine() *search.Engine {
	return &search.Engine{
		Sources:     a.Sources,
		Store:       a.Store,
		Classifier:  a.Classifier,
		CacheTTL:    a.Config.CacheTTL,
		Timeout:     a.Config.Timeout,
		Concurrency: 8,
	}
}

// Since formats a duration in compact form: 3h, 2d, 5w.
func Since(t time.Time, now time.Time) string {
	if t.IsZero() {
		return "?"
	}
	d := now.Sub(t)
	switch {
	case d < time.Hour:
		return "<1h"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h"
	case d < 14*24*time.Hour:
		return strconv.Itoa(int(d.Hours()/24)) + "d"
	default:
		return strconv.Itoa(int(d.Hours()/24/7)) + "w"
	}
}
