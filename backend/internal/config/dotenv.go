package config

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"strings"
)

// DefaultEnvFile is the conventional location of the local env file, relative
// to the working directory the server is started from.
const DefaultEnvFile = ".env"

// LoadEnvFile reads KEY=VALUE pairs from path into the process environment.
//
// Go has no built-in .env support: os.Getenv reads the process environment, so
// a .env file sitting on disk means nothing unless something loads it. Doing it
// here means `go run ./cmd/server` works on its own, instead of needing the
// file to be sourced into the shell first.
//
// A variable that is already set in the real environment always wins, so a
// leftover local .env can never override what a platform like Render injects.
// A missing file is not an error - in production there is no .env at all.
func LoadEnvFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := parseEnvLine(scanner.Text())
		if !ok {
			continue
		}
		// Already exported? The real environment is authoritative.
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// parseEnvLine parses one line, skipping blanks and comments. It accepts an
// optional `export ` prefix and strips one layer of matching quotes.
func parseEnvLine(line string) (key, value string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	line = strings.TrimPrefix(line, "export ")

	key, value, found := strings.Cut(line, "=")
	if !found {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", "", false
	}

	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		first, last := value[0], value[len(value)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return key, value[1 : len(value)-1], true
		}
	}
	// An unquoted value may carry a trailing comment.
	if idx := strings.Index(value, " #"); idx >= 0 {
		value = strings.TrimSpace(value[:idx])
	}
	return key, value, true
}
