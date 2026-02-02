package secrets

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// OnePasswordBackend retrieves secrets from 1Password CLI (op).
// Requires 1Password CLI to be installed and authenticated.
type OnePasswordBackend struct {
	vault    string // Optional vault name
	mu       sync.RWMutex
	cache    map[string]string // Simple cache to avoid repeated CLI calls
	executor CommandExecutor   // Command executor (for testing)
}

// NewOnePasswordBackend creates a new 1Password backend.
// vault can be empty to use the default vault.
func NewOnePasswordBackend(vault string) (*OnePasswordBackend, error) {
	return NewOnePasswordBackendWithExecutor(vault, defaultExecutor)
}

// NewOnePasswordBackendWithExecutor creates a new 1Password backend with a custom executor.
// This is useful for testing.
func NewOnePasswordBackendWithExecutor(vault string, exec CommandExecutor) (*OnePasswordBackend, error) {
	// Check if op CLI is available
	if _, err := exec.LookPath("op"); err != nil {
		return nil, fmt.Errorf("1Password CLI (op) not found: %w", err)
	}

	// Verify we're signed in by running a simple command
	_, stderr, err := exec.Run("op", "whoami", "--format=json")
	if err != nil {
		return nil, fmt.Errorf("1Password CLI not authenticated: %s", string(stderr))
	}

	return &OnePasswordBackend{
		vault:    vault,
		cache:    make(map[string]string),
		executor: exec,
	}, nil
}

// opItem represents a 1Password item from JSON output.
type opItem struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Vault struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"vault"`
	Fields []struct {
		ID    string `json:"id"`
		Label string `json:"label"`
		Value string `json:"value"`
	} `json:"fields"`
}

// Get retrieves a secret from 1Password.
// The key format is: "item/field" or just "item" (defaults to "password" field).
func (b *OnePasswordBackend) Get(key string) (string, error) {
	b.mu.RLock()
	if val, ok := b.cache[key]; ok {
		b.mu.RUnlock()
		return val, nil
	}
	b.mu.RUnlock()

	// Parse key into item and field
	item, field := parseKey(key)

	// Build op command
	args := []string{"item", "get", item, "--format=json"}
	if b.vault != "" {
		args = append(args, "--vault", b.vault)
	}

	stdout, stderr, err := b.executor.Run("op", args...)
	if err != nil {
		// Item not found is not an error, just return empty
		if strings.Contains(string(stderr), "not found") {
			return "", nil
		}
		return "", fmt.Errorf("op get: %s", string(stderr))
	}

	var opItem opItem
	if err := json.Unmarshal(stdout, &opItem); err != nil {
		return "", fmt.Errorf("parse 1Password response: %w", err)
	}

	// Find the field
	for _, f := range opItem.Fields {
		if f.Label == field || f.ID == field {
			b.mu.Lock()
			b.cache[key] = f.Value
			b.mu.Unlock()
			return f.Value, nil
		}
	}

	return "", nil
}

// Set stores a secret in 1Password.
// Creates a new "API Credential" item or updates existing.
func (b *OnePasswordBackend) Set(key, value string) error {
	item, field := parseKey(key)

	// Try to get existing item first
	args := []string{"item", "get", item, "--format=json"}
	if b.vault != "" {
		args = append(args, "--vault", b.vault)
	}

	_, _, err := b.executor.Run("op", args...)
	if err != nil {
		// Item doesn't exist, create it
		createArgs := []string{
			"item", "create",
			"--category", "API Credential",
			"--title", item,
			fmt.Sprintf("%s=%s", field, value),
		}
		if b.vault != "" {
			createArgs = append(createArgs, "--vault", b.vault)
		}

		_, stderr, err := b.executor.Run("op", createArgs...)
		if err != nil {
			return fmt.Errorf("op create: %s", string(stderr))
		}
	} else {
		// Item exists, update field
		editArgs := []string{
			"item", "edit", item,
			fmt.Sprintf("%s=%s", field, value),
		}
		if b.vault != "" {
			editArgs = append(editArgs, "--vault", b.vault)
		}

		_, stderr, err := b.executor.Run("op", editArgs...)
		if err != nil {
			return fmt.Errorf("op edit: %s", string(stderr))
		}
	}

	// Update cache
	b.mu.Lock()
	b.cache[key] = value
	b.mu.Unlock()

	return nil
}

// Delete removes a secret from 1Password.
// Note: This deletes the entire item, not just a field.
func (b *OnePasswordBackend) Delete(key string) error {
	item, _ := parseKey(key)

	args := []string{"item", "delete", item}
	if b.vault != "" {
		args = append(args, "--vault", b.vault)
	}

	_, stderr, err := b.executor.Run("op", args...)
	if err != nil {
		// Already deleted is not an error
		if strings.Contains(string(stderr), "not found") {
			return nil
		}
		return fmt.Errorf("op delete: %s", string(stderr))
	}

	// Clear from cache
	b.mu.Lock()
	delete(b.cache, key)
	b.mu.Unlock()

	return nil
}

// List returns all secret keys from 1Password.
// Returns item titles, not individual fields.
func (b *OnePasswordBackend) List() ([]string, error) {
	args := []string{"item", "list", "--format=json"}
	if b.vault != "" {
		args = append(args, "--vault", b.vault)
	}

	stdout, stderr, err := b.executor.Run("op", args...)
	if err != nil {
		return nil, fmt.Errorf("op list: %s", string(stderr))
	}

	var items []struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(stdout, &items); err != nil {
		return nil, fmt.Errorf("parse 1Password list: %w", err)
	}

	keys := make([]string, 0, len(items))
	for _, item := range items {
		keys = append(keys, item.Title)
	}

	return keys, nil
}

// Name returns the backend name.
func (b *OnePasswordBackend) Name() string {
	return "1password"
}

// ReadOnly returns false since 1Password supports writes.
func (b *OnePasswordBackend) ReadOnly() bool {
	return false
}

// parseKey splits a key into item and field.
// Format: "item/field" or just "item" (defaults to "credential" field).
func parseKey(key string) (item, field string) {
	parts := strings.SplitN(key, "/", 2)
	item = parts[0]
	if len(parts) > 1 {
		field = parts[1]
	} else {
		field = "credential"
	}
	return
}
