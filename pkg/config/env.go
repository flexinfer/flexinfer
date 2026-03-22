// Package config provides unified environment variable parsing helpers
// used across flexinfer components (proxy, runtime, controller, etc.).
package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// GetEnv returns the value of an environment variable, or the default if unset/empty.
func GetEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// GetEnvInt returns an int environment variable, or the default if unset/invalid.
func GetEnvInt(key string, defaultVal int) int {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return i
}

// GetEnvInt64 returns an int64 environment variable, or the default if unset/invalid.
func GetEnvInt64(key string, defaultVal int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	i, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return defaultVal
	}
	return i
}

// GetEnvFloat64 returns a float64 environment variable, or the default if unset/invalid.
func GetEnvFloat64(key string, defaultVal float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return defaultVal
	}
	return f
}

// GetEnvBool returns a boolean environment variable, or the default if unset/invalid.
// Truthy: "true", "1", "yes", "on" (case-insensitive)
// Falsy: "false", "0", "no", "off" (case-insensitive)
func GetEnvBool(key string, defaultVal bool) bool {
	v := strings.ToLower(os.Getenv(key))
	switch v {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	default:
		return defaultVal
	}
}

// GetEnvDuration returns a time.Duration environment variable, or the default if unset/invalid.
func GetEnvDuration(key string, defaultVal time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return defaultVal
	}
	return d
}

// GetEnvUint64 returns a uint64 environment variable, or the default if unset/invalid.
func GetEnvUint64(key string, defaultVal uint64) uint64 {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	u, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return defaultVal
	}
	return u
}
