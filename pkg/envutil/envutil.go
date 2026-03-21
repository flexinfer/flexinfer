package envutil

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// StringOrDefault returns the value of the named environment variable,
// or fallback if the variable is empty or unset.
func StringOrDefault(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

// IntOrDefault returns the integer value of the named environment variable,
// or fallback if the variable is empty, unset, or not a positive integer.
func IntOrDefault(name string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

// BoolOrDefault returns the boolean value of the named environment variable,
// or fallback if the variable is empty, unset, or not a recognized boolean.
func BoolOrDefault(name string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

// DurationOrDefault returns the duration value of the named environment variable,
// or fallback if the variable is empty, unset, or not a valid duration.
func DurationOrDefault(name string, fallback time.Duration) time.Duration {
	if v := os.Getenv(name); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

// Float64OrDefault returns the float64 value of the named environment variable,
// or fallback if the variable is empty, unset, or not a valid float.
func Float64OrDefault(name string, fallback float64) float64 {
	if v := os.Getenv(name); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}
