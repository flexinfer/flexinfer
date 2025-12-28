package secrets

import (
	"bytes"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// KeychainBackend uses macOS Keychain for secret storage.
// Uses the 'security' command-line tool.
type KeychainBackend struct {
	service string // Keychain service name (e.g., "loom")
}

// NewKeychainBackend creates a new macOS Keychain backend.
// Returns an error if not running on macOS or security command is not available.
func NewKeychainBackend() (*KeychainBackend, error) {
	if runtime.GOOS != "darwin" {
		return nil, fmt.Errorf("keychain backend only available on macOS")
	}

	// Check if security command is available
	if _, err := exec.LookPath("security"); err != nil {
		return nil, fmt.Errorf("security command not found: %w", err)
	}

	return &KeychainBackend{
		service: "loom",
	}, nil
}

// Get retrieves a secret from macOS Keychain.
func (b *KeychainBackend) Get(key string) (string, error) {
	cmd := exec.Command("security", "find-generic-password",
		"-s", b.service,
		"-a", key,
		"-w", // Output password only
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Not found is not an error, just return empty
		if strings.Contains(stderr.String(), "could not be found") ||
			strings.Contains(stderr.String(), "SecKeychainSearchCopyNext") {
			return "", nil
		}
		return "", fmt.Errorf("keychain get failed: %s", stderr.String())
	}

	return strings.TrimSpace(stdout.String()), nil
}

// Set stores a secret in macOS Keychain.
func (b *KeychainBackend) Set(key, value string) error {
	// First try to delete any existing entry (ignore errors)
	_ = b.Delete(key)

	cmd := exec.Command("security", "add-generic-password",
		"-s", b.service,
		"-a", key,
		"-w", value,
		"-U", // Update if exists
	)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("keychain set failed: %s", stderr.String())
	}

	return nil
}

// Delete removes a secret from macOS Keychain.
func (b *KeychainBackend) Delete(key string) error {
	cmd := exec.Command("security", "delete-generic-password",
		"-s", b.service,
		"-a", key,
	)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Not found is not an error
		if strings.Contains(stderr.String(), "could not be found") {
			return nil
		}
		return fmt.Errorf("keychain delete failed: %s", stderr.String())
	}

	return nil
}

// List returns all secret keys stored in Keychain under the loom service.
func (b *KeychainBackend) List() ([]string, error) {
	cmd := exec.Command("security", "dump-keychain")

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("keychain list failed: %w", err)
	}

	// Parse output to find loom entries
	// Format: "svce"<blob>="loom" ... "acct"<blob>="KEY_NAME"
	var keys []string
	lines := strings.Split(stdout.String(), "\n")

	inLoomEntry := false
	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Check if this is a loom service entry
		if strings.Contains(line, `"svce"`) && strings.Contains(line, `"loom"`) {
			inLoomEntry = true
			continue
		}

		// Extract account name from loom entries
		if inLoomEntry && strings.Contains(line, `"acct"`) {
			// Parse: "acct"<blob>="KEY_NAME"
			if start := strings.Index(line, `="`); start != -1 {
				if end := strings.LastIndex(line, `"`); end > start+2 {
					key := line[start+2 : end]
					keys = append(keys, key)
				}
			}
			inLoomEntry = false
		}

		// Reset on new keychain entry
		if strings.HasPrefix(line, "keychain:") {
			inLoomEntry = false
		}
	}

	return keys, nil
}

// Name returns the backend name.
func (b *KeychainBackend) Name() string {
	return "keychain"
}

// ReadOnly returns false since Keychain supports writes.
func (b *KeychainBackend) ReadOnly() bool {
	return false
}
