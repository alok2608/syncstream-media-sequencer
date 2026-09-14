package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeEnvFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}
	return path
}

func TestLoadEnvFileParsesTheUsualShapes(t *testing.T) {
	path := writeEnvFile(t, `
# A comment line
DATABASE_URL=postgres://localhost:5432/sequencer?sslmode=disable

  PORT = 9000
export AUTO_SEED=true
QUOTED="a value with spaces"
SINGLE='single quoted'
EMPTY=
TRAILING=value # trailing comment
NOT_AN_ASSIGNMENT
`)

	for _, key := range []string{"DATABASE_URL", "PORT", "AUTO_SEED", "QUOTED", "SINGLE", "EMPTY", "TRAILING"} {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}

	if err := LoadEnvFile(path); err != nil {
		t.Fatalf("load: %v", err)
	}

	cases := map[string]string{
		"DATABASE_URL": "postgres://localhost:5432/sequencer?sslmode=disable",
		"PORT":         "9000",
		"AUTO_SEED":    "true",
		"QUOTED":       "a value with spaces",
		"SINGLE":       "single quoted",
		"EMPTY":        "",
		"TRAILING":     "value",
	}
	for key, want := range cases {
		if got := os.Getenv(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if _, set := os.LookupEnv("NOT_AN_ASSIGNMENT"); set {
		t.Error("a line without '=' should be ignored")
	}
}

// TestRealEnvironmentWins is the important one: a stale local .env must never
// override what a deployment platform injects.
func TestRealEnvironmentWins(t *testing.T) {
	path := writeEnvFile(t, "DATABASE_URL=postgres://from-the-file/db\n")
	t.Setenv("DATABASE_URL", "postgres://from-the-real-environment/db")

	if err := LoadEnvFile(path); err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := os.Getenv("DATABASE_URL"); got != "postgres://from-the-real-environment/db" {
		t.Errorf("the .env file overrode the real environment: %q", got)
	}
}

func TestMissingEnvFileIsNotAnError(t *testing.T) {
	// Production has no .env at all; that must be perfectly normal.
	if err := LoadEnvFile(filepath.Join(t.TempDir(), "definitely-absent")); err != nil {
		t.Errorf("a missing .env should be ignored, got %v", err)
	}
}

func TestLoadUsesTheEnvFileWhenNothingIsExported(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"),
		[]byte("DATABASE_URL=postgres://localhost/from-dotenv\nPORT=9999\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Load() reads ./.env, so run from that directory.
	t.Chdir(dir)
	t.Setenv("DATABASE_URL", "")
	os.Unsetenv("DATABASE_URL")
	t.Setenv("PORT", "")
	os.Unsetenv("PORT")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.DatabaseURL != "postgres://localhost/from-dotenv" {
		t.Errorf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if cfg.Port != "9999" {
		t.Errorf("Port = %q", cfg.Port)
	}
}
