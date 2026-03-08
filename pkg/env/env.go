// Package env provides standardized environment variable helpers for MCP servers.
// These helpers eliminate duplicate getEnv* functions scattered across servers.
package env

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// String returns the value of the environment variable or the fallback if not set or empty.
func String(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Int returns the integer value of the environment variable or the fallback.
// Only positive integers are accepted; zero or negative values return the fallback.
func Int(key string, fallback int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

// IntWithZero returns the integer value of the environment variable or the fallback.
// Unlike Int, this accepts zero and negative values as valid.
func IntWithZero(key string, fallback int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil {
			return n
		}
	}
	return fallback
}

// Float returns the float64 value of the environment variable or the fallback.
// Only positive values are accepted; zero or negative values return the fallback.
func Float(key string, fallback float64) float64 {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err == nil && f > 0 {
			return f
		}
	}
	return fallback
}

// Bool returns the boolean value of the environment variable or the fallback.
// Accepts "1", "true", "t", "yes", "y", "on" (case-insensitive) as true.
// Accepts "0", "false", "f", "no", "n", "off" (case-insensitive) as false.
// Unrecognized non-empty values return the fallback.
func Bool(key string, fallback bool) bool {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "t", "yes", "y", "on":
			return true
		case "0", "false", "f", "no", "n", "off":
			return false
		default:
			return fallback
		}
	}
	return fallback
}

// Duration returns the duration value of the environment variable or the fallback.
// The value should be a valid Go duration string (e.g., "10s", "5m", "1h").
func Duration(key string, fallback time.Duration) time.Duration {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		d, err := time.ParseDuration(v)
		if err == nil {
			return d
		}
	}
	return fallback
}

// StringChain returns the first non-empty environment variable value from keys,
// falling back to the default if all are empty.
func StringChain(keys []string, fallback string) string {
	for _, key := range keys {
		if v := os.Getenv(key); v != "" {
			return v
		}
	}
	return fallback
}

// Int64 returns the int64 value of the environment variable or the fallback.
// Only positive values are accepted; zero or negative values return the fallback.
func Int64(key string, fallback int64) int64 {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

// MustString returns the value of the environment variable or an error if not set.
// Use this for required configuration values.
func MustString(key string) (string, error) {
	v := os.Getenv(key)
	if v == "" {
		return "", &MissingEnvError{Key: key}
	}
	return v, nil
}

// StringWithFallbacks returns the value of the first non-empty environment variable.
// If all are empty, returns empty string.
// This is useful for token fallback chains (e.g., GITHUB_PERSONAL_ACCESS_TOKEN, GITHUB_TOKEN).
func StringWithFallbacks(keys ...string) string {
	for _, key := range keys {
		if v := os.Getenv(key); v != "" {
			return v
		}
	}
	return ""
}

// MissingEnvError is returned when a required environment variable is not set.
type MissingEnvError struct {
	Key string
}

func (e *MissingEnvError) Error() string {
	return e.Key + " environment variable is required"
}
