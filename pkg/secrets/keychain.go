package secrets

import (
	"fmt"
	"runtime"
	"strings"
)

// KeychainBackend uses macOS Keychain for secret storage.
// Uses the 'security' command-line tool.
type KeychainBackend struct {
	service  string          // Keychain service name (e.g., "loom")
	executor CommandExecutor // Command executor (for testing)
}

// NewKeychainBackend creates a new macOS Keychain backend.
// Returns an error if not running on macOS or security command is not available.
func NewKeychainBackend() (*KeychainBackend, error) {
	return NewKeychainBackendWithExecutor(defaultExecutor)
}

// NewKeychainBackendWithExecutor creates a new Keychain backend with a custom executor.
// This is useful for testing.
func NewKeychainBackendWithExecutor(exec CommandExecutor) (*KeychainBackend, error) {
	if runtime.GOOS != "darwin" {
		return nil, fmt.Errorf("keychain backend only available on macOS")
	}

	// Check if security command is available
	if _, err := exec.LookPath("security"); err != nil {
		return nil, fmt.Errorf("security command not found: %w", err)
	}

	return &KeychainBackend{
		service:  "loom",
		executor: exec,
	}, nil
}

// Get retrieves a secret from macOS Keychain.
func (b *KeychainBackend) Get(key string) (string, error) {
	stdout, stderr, err := b.executor.Run("security", "find-generic-password",
		"-s", b.service,
		"-a", key,
		"-w", // Output password only
	)

	if err != nil {
		stderrStr := string(stderr)
		// Not found is not an error, just return empty
		if strings.Contains(stderrStr, "could not be found") ||
			strings.Contains(stderrStr, "SecKeychainSearchCopyNext") {
			return "", nil
		}
		return "", fmt.Errorf("keychain get failed: %s", stderrStr)
	}

	return strings.TrimSpace(string(stdout)), nil
}

// Set stores a secret in macOS Keychain.
func (b *KeychainBackend) Set(key, value string) error {
	// First try to delete any existing entry (ignore errors)
	_ = b.Delete(key)

	// Use -A to allow access from any application without prompting
	// This is necessary for loomd (daemon) to access secrets stored by CLI
	_, stderr, err := b.executor.Run("security", "add-generic-password",
		"-s", b.service,
		"-a", key,
		"-w", value,
		"-A", // Allow access from any application
		"-U", // Update if exists
	)

	if err != nil {
		return fmt.Errorf("keychain set failed: %s", string(stderr))
	}

	return nil
}

// Delete removes a secret from macOS Keychain.
func (b *KeychainBackend) Delete(key string) error {
	_, stderr, err := b.executor.Run("security", "delete-generic-password",
		"-s", b.service,
		"-a", key,
	)

	if err != nil {
		stderrStr := string(stderr)
		// Not found is not an error
		if strings.Contains(stderrStr, "could not be found") {
			return nil
		}
		return fmt.Errorf("keychain delete failed: %s", stderrStr)
	}

	return nil
}

// List returns all secret keys stored in Keychain under the loom service.
func (b *KeychainBackend) List() ([]string, error) {
	stdout, _, err := b.executor.Run("security", "dump-keychain")
	if err != nil {
		return nil, fmt.Errorf("keychain list failed: %w", err)
	}

	// Parse output to find loom entries
	// Format: "svce"<blob>="loom" ... "acct"<blob>="KEY_NAME"
	var keys []string
	lines := strings.Split(string(stdout), "\n")

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
