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

// TestLoopbackOriginsAllowedOnAnyPort covers the case that otherwise looks like
// a dead backend: Vite falls back to 5174+ when its default port is taken, and
// the browser's origin then matches nothing in FRONTEND_URL.
func TestLoopbackOriginsAllowedOnAnyPort(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("FRONTEND_URL", "https://app.example.com")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.AllowLoopbackOrigins {
		t.Fatal("loopback origins should be allowed by default")
	}

	allowed := []string{
		"http://localhost:5173",
		"http://localhost:5174", // Vite's fallback port
		"http://localhost:4173", // vite preview
		"http://127.0.0.1:5173",
		"http://[::1]:5173",
		"http://localhost",
	}
	for _, origin := range allowed {
		if !cfg.OriginAllowed(origin) {
			t.Errorf("OriginAllowed(%q) = false, want true", origin)
		}
	}

	// Anything that is not this machine still has to be listed explicitly.
	denied := []string{
		"https://evil.example.com",
		"http://localhost.evil.com:5173",
		"http://notlocalhost:5173",
		"https://localhost:5173", // https loopback is not a local dev server
		"",
	}
	for _, origin := range denied {
		if cfg.OriginAllowed(origin) {
			t.Errorf("OriginAllowed(%q) = true, want false", origin)
		}
	}
}

func TestLoopbackOriginsCanBeDisabled(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("FRONTEND_URL", "https://app.example.com")
	t.Setenv("ALLOW_LOOPBACK_ORIGINS", "false")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.OriginAllowed("http://localhost:5174") {
		t.Error("loopback origin allowed despite ALLOW_LOOPBACK_ORIGINS=false")
	}
	if !cfg.OriginAllowed("https://app.example.com") {
		t.Error("the configured origin should still be allowed")
	}
}
