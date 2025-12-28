package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/crypto/pbkdf2"
)

// FileBackend stores secrets in an encrypted file.
// Uses AES-256-GCM for encryption.
type FileBackend struct {
	path   string
	key    []byte // Derived encryption key
	mu     sync.RWMutex
	cache  map[string]string // In-memory cache of decrypted secrets
	loaded bool
}

// defaultFilePath returns the default secrets file path.
func defaultFilePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "loom", "secrets.enc")
}

// NewFileBackend creates a new encrypted file backend.
// If path is empty, uses ~/.config/loom/secrets.enc
// The encryption key is derived from LOOM_MASTER_KEY env var, machine ID, or prompts user.
func NewFileBackend(path string) (*FileBackend, error) {
	if path == "" {
		path = defaultFilePath()
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create secrets dir: %w", err)
	}

	// Get or derive master key
	key, err := getMasterKey()
	if err != nil {
		return nil, fmt.Errorf("get master key: %w", err)
	}

	return &FileBackend{
		path:  path,
		key:   key,
		cache: make(map[string]string),
	}, nil
}

// getMasterKey retrieves or derives the master encryption key.
// Priority:
// 1. LOOM_MASTER_KEY environment variable (for CI/CD)
// 2. Key stored in macOS Keychain
// 3. Derive from machine ID (fallback)
func getMasterKey() ([]byte, error) {
	// Check environment variable first
	if envKey := os.Getenv("LOOM_MASTER_KEY"); envKey != "" {
		return deriveKey(envKey), nil
	}

	// Try to get from keychain (macOS)
	if kb, err := NewKeychainBackend(); err == nil {
		if key, err := kb.Get("_loom_master_key"); err == nil && key != "" {
			return deriveKey(key), nil
		}
	}

	// Generate and store a new key
	key, err := generateMasterKey()
	if err != nil {
		return nil, err
	}

	// Try to store in keychain for future use
	if kb, err := NewKeychainBackend(); err == nil {
		_ = kb.Set("_loom_master_key", key)
	}

	return deriveKey(key), nil
}

// generateMasterKey generates a random master key.
func generateMasterKey() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", bytes), nil
}

// deriveKey derives a 32-byte AES key from a passphrase.
func deriveKey(passphrase string) []byte {
	salt := []byte("loom-secrets-v1")
	return pbkdf2.Key([]byte(passphrase), salt, 100000, 32, sha256.New)
}

// load reads and decrypts the secrets file.
func (b *FileBackend) load() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.loaded {
		return nil
	}

	data, err := os.ReadFile(b.path)
	if os.IsNotExist(err) {
		b.cache = make(map[string]string)
		b.loaded = true
		return nil
	}
	if err != nil {
		return fmt.Errorf("read secrets file: %w", err)
	}

	// Decrypt
	plaintext, err := b.decrypt(data)
	if err != nil {
		return fmt.Errorf("decrypt secrets: %w", err)
	}

	// Parse JSON
	if err := json.Unmarshal(plaintext, &b.cache); err != nil {
		return fmt.Errorf("parse secrets: %w", err)
	}

	b.loaded = true
	return nil
}

// save encrypts and writes the secrets file.
func (b *FileBackend) save() error {
	// Marshal to JSON
	plaintext, err := json.MarshalIndent(b.cache, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal secrets: %w", err)
	}

	// Encrypt
	ciphertext, err := b.encrypt(plaintext)
	if err != nil {
		return fmt.Errorf("encrypt secrets: %w", err)
	}

	// Write atomically
	tmpPath := b.path + ".tmp"
	if err := os.WriteFile(tmpPath, ciphertext, 0600); err != nil {
		return fmt.Errorf("write secrets: %w", err)
	}

	if err := os.Rename(tmpPath, b.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename secrets: %w", err)
	}

	return nil
}

// encrypt encrypts data using AES-256-GCM.
func (b *FileBackend) encrypt(plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(b.key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// decrypt decrypts data using AES-256-GCM.
func (b *FileBackend) decrypt(ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(b.key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	if len(ciphertext) < gcm.NonceSize() {
		return nil, fmt.Errorf("ciphertext too short")
	}

	nonce := ciphertext[:gcm.NonceSize()]
	ciphertext = ciphertext[gcm.NonceSize():]

	return gcm.Open(nil, nonce, ciphertext, nil)
}

// Get retrieves a secret from the encrypted file.
func (b *FileBackend) Get(key string) (string, error) {
	if err := b.load(); err != nil {
		return "", err
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.cache[key], nil
}

// Set stores a secret in the encrypted file.
func (b *FileBackend) Set(key, value string) error {
	if err := b.load(); err != nil {
		return err
	}

	b.mu.Lock()
	b.cache[key] = value
	b.mu.Unlock()

	return b.save()
}

// Delete removes a secret from the encrypted file.
func (b *FileBackend) Delete(key string) error {
	if err := b.load(); err != nil {
		return err
	}

	b.mu.Lock()
	delete(b.cache, key)
	b.mu.Unlock()

	return b.save()
}

// List returns all secret keys in the encrypted file.
func (b *FileBackend) List() ([]string, error) {
	if err := b.load(); err != nil {
		return nil, err
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	keys := make([]string, 0, len(b.cache))
	for k := range b.cache {
		keys = append(keys, k)
	}
	return keys, nil
}

// Name returns the backend name.
func (b *FileBackend) Name() string {
	return "file"
}

// ReadOnly returns false since file backend supports writes.
func (b *FileBackend) ReadOnly() bool {
	return false
}
