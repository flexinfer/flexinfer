package secrets

import (
	"errors"
	"runtime"
	"testing"
)

// mockExecutor is a test mock for CommandExecutor.
type mockExecutor struct {
	lookPathResult map[string]error // command -> error (nil = found)
	runResults     []mockRunResult  // Sequential results for Run calls
	runIndex       int              // Current index in runResults
	runCalls       [][]string       // Record of all Run calls (command + args)
}

type mockRunResult struct {
	stdout []byte
	stderr []byte
	err    error
}

func newMockExecutor() *mockExecutor {
	return &mockExecutor{
		lookPathResult: make(map[string]error),
		runResults:     []mockRunResult{},
		runCalls:       [][]string{},
	}
}

func (m *mockExecutor) LookPath(file string) (string, error) {
	if err, ok := m.lookPathResult[file]; ok {
		if err != nil {
			return "", err
		}
		return "/usr/bin/" + file, nil
	}
	return "/usr/bin/" + file, nil
}

func (m *mockExecutor) Run(name string, args ...string) (stdout, stderr []byte, err error) {
	m.runCalls = append(m.runCalls, append([]string{name}, args...))
	if m.runIndex >= len(m.runResults) {
		return nil, nil, nil
	}
	result := m.runResults[m.runIndex]
	m.runIndex++
	return result.stdout, result.stderr, result.err
}

func (m *mockExecutor) addRunResult(stdout, stderr []byte, err error) {
	m.runResults = append(m.runResults, mockRunResult{stdout, stderr, err})
}

func TestKeychainBackend_NewKeychainBackend_NotDarwin(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("This test only runs on non-macOS systems")
	}

	_, err := NewKeychainBackend()
	if err == nil {
		t.Error("NewKeychainBackend() should return error on non-macOS")
	}
	if err != nil && err.Error() != "keychain backend only available on macOS" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestKeychainBackend_NewKeychainBackend_SecurityNotFound(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("This test only runs on macOS")
	}

	mock := newMockExecutor()
	mock.lookPathResult["security"] = errors.New("not found")

	_, err := NewKeychainBackendWithExecutor(mock)
	if err == nil {
		t.Error("NewKeychainBackendWithExecutor() should return error when security not found")
	}
}

func TestKeychainBackend_NewKeychainBackend_Success(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("This test only runs on macOS")
	}

	mock := newMockExecutor()
	backend, err := NewKeychainBackendWithExecutor(mock)
	if err != nil {
		t.Fatalf("NewKeychainBackendWithExecutor() error = %v", err)
	}
	if backend == nil {
		t.Error("NewKeychainBackendWithExecutor() returned nil backend")
	}
	if backend.Name() != "keychain" {
		t.Errorf("Name() = %v, want keychain", backend.Name())
	}
	if backend.ReadOnly() {
		t.Error("ReadOnly() = true, want false")
	}
}

func TestKeychainBackend_Get_Success(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("This test only runs on macOS")
	}

	mock := newMockExecutor()
	mock.addRunResult([]byte("secret-value\n"), nil, nil)

	backend, _ := NewKeychainBackendWithExecutor(mock)
	value, err := backend.Get("test-key")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if value != "secret-value" {
		t.Errorf("Get() = %v, want secret-value", value)
	}

	// Verify correct command was called
	if len(mock.runCalls) != 1 {
		t.Fatalf("expected 1 Run call, got %d", len(mock.runCalls))
	}
	call := mock.runCalls[0]
	if call[0] != "security" || call[1] != "find-generic-password" {
		t.Errorf("unexpected command: %v", call)
	}
}

func TestKeychainBackend_Get_NotFound(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("This test only runs on macOS")
	}

	mock := newMockExecutor()
	mock.addRunResult(nil, []byte("could not be found"), errors.New("exit 44"))

	backend, _ := NewKeychainBackendWithExecutor(mock)
	value, err := backend.Get("missing-key")
	if err != nil {
		t.Fatalf("Get() error = %v (should be nil for not found)", err)
	}
	if value != "" {
		t.Errorf("Get() = %v, want empty string", value)
	}
}

func TestKeychainBackend_Get_SecKeychainError(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("This test only runs on macOS")
	}

	mock := newMockExecutor()
	mock.addRunResult(nil, []byte("SecKeychainSearchCopyNext: The specified item could not be found"), errors.New("exit 44"))

	backend, _ := NewKeychainBackendWithExecutor(mock)
	value, err := backend.Get("missing-key")
	if err != nil {
		t.Fatalf("Get() error = %v (should be nil for not found)", err)
	}
	if value != "" {
		t.Errorf("Get() = %v, want empty string", value)
	}
}

func TestKeychainBackend_Get_Error(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("This test only runs on macOS")
	}

	mock := newMockExecutor()
	mock.addRunResult(nil, []byte("some other error"), errors.New("exit 1"))

	backend, _ := NewKeychainBackendWithExecutor(mock)
	_, err := backend.Get("test-key")
	if err == nil {
		t.Error("Get() should return error for unexpected failures")
	}
}

func TestKeychainBackend_Set_Success(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("This test only runs on macOS")
	}

	mock := newMockExecutor()
	// First call is delete (might fail, that's ok)
	mock.addRunResult(nil, []byte("could not be found"), errors.New("not found"))
	// Second call is add
	mock.addRunResult(nil, nil, nil)

	backend, _ := NewKeychainBackendWithExecutor(mock)
	err := backend.Set("test-key", "test-value")
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	// Verify add command was called with correct args
	if len(mock.runCalls) != 2 {
		t.Fatalf("expected 2 Run calls, got %d", len(mock.runCalls))
	}
	addCall := mock.runCalls[1]
	if addCall[0] != "security" || addCall[1] != "add-generic-password" {
		t.Errorf("unexpected add command: %v", addCall)
	}
}

func TestKeychainBackend_Set_Error(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("This test only runs on macOS")
	}

	mock := newMockExecutor()
	// Delete might fail (ok)
	mock.addRunResult(nil, nil, nil)
	// Add fails
	mock.addRunResult(nil, []byte("keychain locked"), errors.New("exit 1"))

	backend, _ := NewKeychainBackendWithExecutor(mock)
	err := backend.Set("test-key", "test-value")
	if err == nil {
		t.Error("Set() should return error on failure")
	}
}

func TestKeychainBackend_Delete_Success(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("This test only runs on macOS")
	}

	mock := newMockExecutor()
	mock.addRunResult(nil, nil, nil)

	backend, _ := NewKeychainBackendWithExecutor(mock)
	err := backend.Delete("test-key")
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
}

func TestKeychainBackend_Delete_NotFound(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("This test only runs on macOS")
	}

	mock := newMockExecutor()
	mock.addRunResult(nil, []byte("could not be found"), errors.New("exit 44"))

	backend, _ := NewKeychainBackendWithExecutor(mock)
	err := backend.Delete("missing-key")
	if err != nil {
		t.Fatalf("Delete() error = %v (should be nil for not found)", err)
	}
}

func TestKeychainBackend_Delete_Error(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("This test only runs on macOS")
	}

	mock := newMockExecutor()
	mock.addRunResult(nil, []byte("keychain locked"), errors.New("exit 1"))

	backend, _ := NewKeychainBackendWithExecutor(mock)
	err := backend.Delete("test-key")
	if err == nil {
		t.Error("Delete() should return error on failure")
	}
}

func TestKeychainBackend_List_Success(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("This test only runs on macOS")
	}

	// Sample keychain dump output
	dumpOutput := `keychain: "/Users/test/Library/Keychains/login.keychain-db"
    "svce"<blob>="loom"
    "acct"<blob>="github_token"
keychain: "/Users/test/Library/Keychains/login.keychain-db"
    "svce"<blob>="loom"
    "acct"<blob>="gitlab_token"
keychain: "/Users/test/Library/Keychains/login.keychain-db"
    "svce"<blob>="other-service"
    "acct"<blob>="should-not-appear"
`

	mock := newMockExecutor()
	mock.addRunResult([]byte(dumpOutput), nil, nil)

	backend, _ := NewKeychainBackendWithExecutor(mock)
	keys, err := backend.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("List() returned %d keys, want 2", len(keys))
	}

	keySet := make(map[string]bool)
	for _, k := range keys {
		keySet[k] = true
	}
	if !keySet["github_token"] {
		t.Error("List() should include github_token")
	}
	if !keySet["gitlab_token"] {
		t.Error("List() should include gitlab_token")
	}
}

func TestKeychainBackend_List_Empty(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("This test only runs on macOS")
	}

	mock := newMockExecutor()
	mock.addRunResult([]byte(""), nil, nil)

	backend, _ := NewKeychainBackendWithExecutor(mock)
	keys, err := backend.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("List() returned %d keys, want 0", len(keys))
	}
}

func TestKeychainBackend_List_Error(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("This test only runs on macOS")
	}

	mock := newMockExecutor()
	mock.addRunResult(nil, nil, errors.New("dump failed"))

	backend, _ := NewKeychainBackendWithExecutor(mock)
	_, err := backend.List()
	if err == nil {
		t.Error("List() should return error on failure")
	}
}

func TestKeychainBackend_Interface(t *testing.T) {
	// Verify KeychainBackend implements Backend interface
	var _ Backend = (*KeychainBackend)(nil)
}
