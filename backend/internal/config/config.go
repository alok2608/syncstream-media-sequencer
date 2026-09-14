// Package config loads runtime configuration from the environment. Nothing in
// the application logic hardcodes a host, port or deployment domain.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the fully resolved server configuration.
type Config struct {
	Port        string
	DatabaseURL string
	// AllowedOrigins is the CORS/WebSocket origin allow-list. "*" allows any
	// origin, which is convenient for local development and demos.
	AllowedOrigins []string
	// SyncLeadTime is how far in the future a sync is scheduled so that every
	// connected client can receive, preload and align before playback starts.
	SyncLeadTime time.Duration
	// AllowLoopbackOrigins accepts any http://localhost / 127.0.0.1 / [::1]
	// origin regardless of port. Vite silently falls back to 5174, 5175 and so
	// on when its default port is busy, which otherwise looks exactly like a
	// broken backend: CORS blocks the reads and the WebSocket upgrade is
	// refused. Set ALLOW_LOOPBACK_ORIGINS=false to require an exact match.
	AllowLoopbackOrigins bool

	// AutoSeed seeds demo data on boot when the database has no windows yet.
	AutoSeed bool
	// LogRequests toggles HTTP access logging.
	LogRequests bool
}

// Load reads configuration from the environment and validates it.
//
// A local .env file, if present, is loaded first as a developer convenience;
// real environment variables always take precedence over it.
func Load() (Config, error) {
	if err := LoadEnvFile(DefaultEnvFile); err != nil {
		return Config{}, fmt.Errorf("read %s: %w", DefaultEnvFile, err)
	}

	cfg := Config{
		Port:                 env("PORT", "8080"),
		DatabaseURL:          strings.TrimSpace(os.Getenv("DATABASE_URL")),
		AllowedOrigins:       parseOrigins(env("FRONTEND_URL", "http://localhost:5173")),
		SyncLeadTime:         time.Duration(envInt("SYNC_LEAD_TIME_MS", 1000)) * time.Millisecond,
		AllowLoopbackOrigins: envBool("ALLOW_LOOPBACK_ORIGINS", true),
		AutoSeed:             envBool("AUTO_SEED", true),
		LogRequests:          envBool("LOG_REQUESTS", true),
	}

	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required (example: postgres://user:pass@host:5432/dbname?sslmode=disable)")
	}
	if cfg.SyncLeadTime < 0 || cfg.SyncLeadTime > 30*time.Second {
		return Config{}, fmt.Errorf("SYNC_LEAD_TIME_MS must be between 0 and 30000, got %s", cfg.SyncLeadTime)
	}
	if len(cfg.AllowedOrigins) == 0 {
		return Config{}, errors.New("FRONTEND_URL must contain at least one origin, or \"*\"")
	}
	return cfg, nil
}

// AllowsAnyOrigin reports whether the allow-list is the wildcard.
func (c Config) AllowsAnyOrigin() bool {
	for _, o := range c.AllowedOrigins {
		if o == "*" {
			return true
		}
	}
	return false
}

// OriginAllowed reports whether a browser Origin header may talk to this API.
func (c Config) OriginAllowed(origin string) bool {
	if c.AllowsAnyOrigin() {
		return true
	}
	origin = strings.TrimRight(strings.TrimSpace(origin), "/")
	if origin == "" {
		return false
	}
	for _, allowed := range c.AllowedOrigins {
		if strings.EqualFold(strings.TrimRight(allowed, "/"), origin) {
			return true
		}
	}
	return c.AllowLoopbackOrigins && isLoopbackOrigin(origin)
}

// isLoopbackOrigin reports whether an origin points at this machine, on any
// port. Only http is accepted: an https loopback origin is not something a
// local dev server produces.
func isLoopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// parseOrigins splits a comma separated origin list and drops empty entries.
func parseOrigins(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v, err := strconv.Atoi(env(key, "")); err == nil {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if v, err := strconv.ParseBool(env(key, "")); err == nil {
		return v
	}
	return fallback
}
