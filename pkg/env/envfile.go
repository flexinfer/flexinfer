package env

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

// LoadFile reads a KEY=value env file and sets each variable in the process
// environment. Lines starting with '#' (after trimming whitespace) and blank
// lines are skipped. Values may be optionally wrapped in single or double
// quotes, which are stripped. Existing environment variables are NOT
// overridden. If the file does not exist, nil is returned.
func LoadFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("open env file %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := parseEnvLine(line)
		if !ok {
			continue // skip malformed lines silently
		}

		// Do not override existing env vars.
		if os.Getenv(key) != "" {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("setenv %s (line %d): %w", key, lineNum, err)
		}
	}
	return scanner.Err()
}

// parseEnvLine splits "KEY=value" and strips optional quotes from the value.
func parseEnvLine(line string) (key, value string, ok bool) {
	idx := strings.IndexByte(line, '=')
	if idx < 1 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:idx])
	value = strings.TrimSpace(line[idx+1:])
	value = stripQuotes(value)
	return key, value, true
}

// stripQuotes removes matching leading/trailing single or double quotes.
func stripQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') ||
			(s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
