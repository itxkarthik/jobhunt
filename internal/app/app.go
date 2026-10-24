// Package app wires config, store, sources, and the search engine together
// for the CLI and TUI.
package app

import (
	"strconv"
	"time"

	"github.com/itxkarthik/jobhunt/internal/classify"
	"github.com/itxkarthik/jobhunt/internal/config"
	"github.com/itxkarthik/jobhunt/internal/search"
	"github.com/itxkarthik/jobhunt/internal/sources"
	"github.com/itxkarthik/jobhunt/internal/sources/ats"
	"github.com/itxkarthik/jobhunt/internal/store"
)

type App struct {
	Config     *config.Config
	Store      *store.Store
	Classifier *classify.Classifier
	Companies  []ats.Company
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
	companies, err := ats.LoadCompanies(cfg.CompaniesFile)
	if err != nil {
		st.Close()
		return nil, err
	}
	return &App{Config: cfg, Store: st, Classifier: cl, Companies: companies, Sources: BuildSources(cfg, companies)}, nil
}

func (a *App) Close() error { return a.Store.Close() }

// Engine returns a search engine over the app's sources.
func (a *App) Engine() *search.Engine {
	return &search.Engine{
		Sources:     a.Sources,
		Store:       a.Store,
		Classifier:  a.Classifier,
		CacheTTL:    a.Config.CacheTTL,
		SourceTTL:   a.sourceTTLs(),
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

func (a *App) sourceTTLs() map[string]time.Duration {
	m := map[string]time.Duration{}
	for name, sc := range a.Config.Sources {
		if sc.TTL > 0 {
			m[name] = sc.TTL
		}
	}
	return m
}
