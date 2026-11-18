package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestLoadFileAndEnv(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("cache_ttl: 2h\nsources:\n  adzuna:\n    app_id: fromfile\n  remotive:\n    enabled: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JOBHUNT_CONFIG", p)
	t.Setenv("XDG_DATA_HOME", dir)
	t.Setenv("JOBHUNT_ADZUNA_APP_KEY", "fromenv")
	t.Setenv("JOBHUNT_EDGAR_USER_AGENT", "Test Runner test@example.com")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CacheTTL != 2*time.Hour {
		t.Errorf("cache_ttl = %v", cfg.CacheTTL)
	}
	if a := cfg.Source("adzuna"); a.AppID != "fromfile" || a.AppKey != "fromenv" || !a.IsEnabled(true) {
		t.Errorf("adzuna = %+v", a)
	}
	if cfg.Importers.EDGAR.UserAgent != "Test Runner test@example.com" {
		t.Errorf("edgar user agent = %q", cfg.Importers.EDGAR.UserAgent)
	}
	if cfg.Source("remotive").IsEnabled(true) {
		t.Error("remotive should be disabled")
	}
	if cfg.DBPath != filepath.Join(dir, "jobhunt", "jobhunt.db") {
		t.Errorf("db path = %s", cfg.DBPath)
	}
	if len(cfg.Defaults.Countries) != 1 || cfg.Defaults.Countries[0] != "IN" {
		t.Errorf("default countries = %v", cfg.Defaults.Countries)
	}
}

func TestMissingFileUsesDefaults(t *testing.T) {
	t.Setenv("JOBHUNT_CONFIG", filepath.Join(t.TempDir(), "nope.yaml"))
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CacheTTL != time.Hour || cfg.UserAgent != DefaultUserAgent {
		t.Errorf("defaults not applied: %+v", cfg)
	}
}

func TestFeedConfig(t *testing.T) {
	tests := []struct {
		name                string
		file                string
		envURL, envSecret   string
		wantURL, wantSecret string
	}{
		{"from file", "feed:\n  webhook_url: https://a.example/h\n  webhook_secret: file-secret-0123456\n", "", "",
			"https://a.example/h", "file-secret-0123456"},
		{"env overrides file", "feed:\n  webhook_url: https://a.example/h\n  webhook_secret: file-secret-0123456\n",
			"https://b.example/h", "env-secret-0123456789", "https://b.example/h", "env-secret-0123456789"},
		{"env only", "", "https://b.example/h", "env-secret-0123456789", "https://b.example/h", "env-secret-0123456789"},
		{"none", "", "", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "config.yaml")
			if err := os.WriteFile(p, []byte(tt.file), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("JOBHUNT_CONFIG", p)
			t.Setenv("XDG_DATA_HOME", dir)
			t.Setenv("JOBHUNT_WEBHOOK_URL", tt.envURL)
			t.Setenv("JOBHUNT_WEBHOOK_SECRET", tt.envSecret)
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Feed.WebhookURL != tt.wantURL || cfg.Feed.WebhookSecret != tt.wantSecret {
				t.Errorf("feed = url %q, secret set=%v; want %q, secret set=%v",
					cfg.Feed.WebhookURL, cfg.Feed.WebhookSecret != "", tt.wantURL, tt.wantSecret != "")
			}
		})
	}
}

func TestExampleConfigParses(t *testing.T) {
	var cfg Config
	if err := yaml.Unmarshal(Example, &cfg); err != nil {
		t.Fatalf("config.example.yaml: %v", err)
	}
}

func TestServeTokenFromEnv(t *testing.T) {
	t.Setenv("JOBHUNT_CONFIG", filepath.Join(t.TempDir(), "nope.yaml"))
	t.Setenv("JOBHUNT_SERVE_TOKEN", "env-token-0123456789")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Serve.Token != "env-token-0123456789" {
		t.Error("JOBHUNT_SERVE_TOKEN not applied")
	}
}
