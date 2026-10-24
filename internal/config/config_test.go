package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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
