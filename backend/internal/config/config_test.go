package config

import (
	"testing"
	"time"
)

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected an error when DATABASE_URL is unset")
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Port != "8080" {
		t.Errorf("port = %q, want 8080", cfg.Port)
	}
	if cfg.SyncLeadTime != time.Second {
		t.Errorf("sync lead = %s, want 1s", cfg.SyncLeadTime)
	}
	if len(cfg.AllowedOrigins) != 2 {
		t.Errorf("expected 2 default origins, got %v", cfg.AllowedOrigins)
	}
}

func TestOriginAllowed(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("FRONTEND_URL", "https://app.example.com/, http://localhost:5173")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	cases := []struct {
		origin string
		want   bool
	}{
		{"https://app.example.com", true},
		{"https://app.example.com/", true},
		{"HTTPS://APP.EXAMPLE.COM", true},
		{"http://localhost:5173", true},
		{"https://evil.example.com", false},
		{"", false},
	}
	for _, c := range cases {
		if got := cfg.OriginAllowed(c.origin); got != c.want {
			t.Errorf("OriginAllowed(%q) = %v, want %v", c.origin, got, c.want)
		}
	}
}

func TestWildcardOrigin(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("FRONTEND_URL", "*")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.AllowsAnyOrigin() || !cfg.OriginAllowed("https://anything.test") {
		t.Fatal("wildcard origin should allow everything")
	}
}

func TestInvalidSyncLeadTimeRejected(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("SYNC_LEAD_TIME_MS", "60000")
	if _, err := Load(); err == nil {
		t.Fatal("expected an error for an out-of-range sync lead time")
	}
}
