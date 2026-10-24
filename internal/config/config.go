// Package config loads ~/.config/jobhunt/config.yaml and applies
// environment-variable overrides for API keys.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

const DefaultUserAgent = "jobhunt/0.1 (+https://github.com/itxkarthik/jobhunt; personal job search)"

type Config struct {
	UserAgent string        `yaml:"user_agent"`
	CacheTTL  time.Duration `yaml:"cache_ttl"`
	Timeout   time.Duration `yaml:"timeout"`
	DBPath    string        `yaml:"db_path"`

	// KeywordsFile overrides the embedded classifier keyword lists.
	KeywordsFile string `yaml:"keywords_file"`

	Defaults Defaults                `yaml:"defaults"`
	Sources  map[string]SourceConfig `yaml:"sources"`
}

// Defaults are applied when the matching CLI flag is not given.
type Defaults struct {
	Countries []string `yaml:"countries"`
	Modes     []string `yaml:"modes"`
	Types     []string `yaml:"types"`
	Areas     []string `yaml:"areas"`
	Limit     int      `yaml:"limit"`
}

type SourceConfig struct {
	Enabled     *bool         `yaml:"enabled,omitempty"`
	AppID       string        `yaml:"app_id,omitempty"`
	AppKey      string        `yaml:"app_key,omitempty"`
	APIKey      string        `yaml:"api_key,omitempty"`
	BearerToken string        `yaml:"bearer_token,omitempty"`
	MaxPages    int           `yaml:"max_pages,omitempty"`
	MaxResults  int           `yaml:"max_results,omitempty"`
	TTL         time.Duration `yaml:"ttl,omitempty"`
}

// IsEnabled returns the configured value or def when unset.
func (s SourceConfig) IsEnabled(def bool) bool {
	if s.Enabled == nil {
		return def
	}
	return *s.Enabled
}

func Default() *Config {
	return &Config{
		UserAgent: DefaultUserAgent,
		CacheTTL:  time.Hour,
		Timeout:   20 * time.Second,
		Defaults: Defaults{
			Countries: []string{"IN"},
			Limit:     50,
		},
		Sources: map[string]SourceConfig{},
	}
}

// Dir returns the config directory (~/.config/jobhunt or $XDG_CONFIG_HOME/jobhunt).
func Dir() (string, error) {
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "jobhunt"), nil
}

// Path returns the config file path, honoring $JOBHUNT_CONFIG.
func Path() (string, error) {
	if p := os.Getenv("JOBHUNT_CONFIG"); p != "" {
		return p, nil
	}
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "config.yaml"), nil
}

// DataDir is where the SQLite DB lives (~/.local/share/jobhunt).
func DataDir() (string, error) {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "jobhunt"), nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".local", "share", "jobhunt"), nil
}

// Load reads the config file if present (a missing file is not an error),
// then applies env overrides.
func Load() (*Config, error) {
	cfg := Default()
	p, err := Path()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if err := yaml.Unmarshal(b, cfg); err != nil {
			return nil, fmt.Errorf("parse %s: %w", p, err)
		}
	}
	if cfg.Sources == nil {
		cfg.Sources = map[string]SourceConfig{}
	}
	applyEnv(cfg)
	if cfg.DBPath == "" {
		d, err := DataDir()
		if err != nil {
			return nil, err
		}
		cfg.DBPath = filepath.Join(d, "jobhunt.db")
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = DefaultUserAgent
	}
	return cfg, nil
}

// applyEnv lets keys come from the environment so they never need to be
// written to disk, e.g. JOBHUNT_ADZUNA_APP_ID.
func applyEnv(cfg *Config) {
	set := func(src string, fn func(*SourceConfig, string), env string) {
		if v := os.Getenv(env); v != "" {
			sc := cfg.Sources[src]
			fn(&sc, v)
			cfg.Sources[src] = sc
		}
	}
	set("adzuna", func(s *SourceConfig, v string) { s.AppID = v }, "JOBHUNT_ADZUNA_APP_ID")
	set("adzuna", func(s *SourceConfig, v string) { s.AppKey = v }, "JOBHUNT_ADZUNA_APP_KEY")
	set("jsearch", func(s *SourceConfig, v string) { s.APIKey = v }, "JOBHUNT_JSEARCH_API_KEY")
	set("x", func(s *SourceConfig, v string) { s.BearerToken = v }, "JOBHUNT_X_BEARER_TOKEN")
}

// Source returns the config block for name (zero value if absent).
func (c *Config) Source(name string) SourceConfig {
	return c.Sources[name]
}
