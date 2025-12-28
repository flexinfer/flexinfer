package secrets

import (
	"os"
	"strings"
)

// EnvBackend reads secrets from environment variables.
// This is read-only and has highest priority to allow runtime overrides.
type EnvBackend struct{}

// NewEnvBackend creates a new environment variable backend.
func NewEnvBackend() *EnvBackend {
	return &EnvBackend{}
}

// Get retrieves a secret from environment variables.
func (b *EnvBackend) Get(key string) (string, error) {
	return os.Getenv(key), nil
}

// Set is not supported for environment variables.
func (b *EnvBackend) Set(key, value string) error {
	return ErrReadOnly
}

// Delete is not supported for environment variables.
func (b *EnvBackend) Delete(key string) error {
	return ErrReadOnly
}

// List returns environment variable names that look like secrets.
// This is a best-effort list based on common naming patterns.
func (b *EnvBackend) List() ([]string, error) {
	var keys []string

	// Common secret-related suffixes
	secretSuffixes := []string{
		"_TOKEN", "_KEY", "_SECRET", "_PASSWORD", "_PAT",
		"_API_KEY", "_API_TOKEN", "_ACCESS_TOKEN",
	}

	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			continue
		}
		name := parts[0]

		// Check if this looks like a secret
		for _, suffix := range secretSuffixes {
			if strings.HasSuffix(name, suffix) {
				keys = append(keys, name)
				break
			}
		}
	}

	return keys, nil
}

// Name returns the backend name.
func (b *EnvBackend) Name() string {
	return "env"
}

// ReadOnly returns true since we can't modify environment variables.
func (b *EnvBackend) ReadOnly() bool {
	return true
}
