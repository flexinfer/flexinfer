package secrets

import (
	"os"
	"testing"
)

// mockBackend is a simple in-memory backend for testing
type mockBackend struct {
	name     string
	secrets  map[string]string
	readOnly bool
}

func newMockBackend(name string, readOnly bool) *mockBackend {
	return &mockBackend{
		name:     name,
		secrets:  make(map[string]string),
		readOnly: readOnly,
	}
}

func (m *mockBackend) Get(key string) (string, error) {
	return m.secrets[key], nil
}

func (m *mockBackend) Set(key, value string) error {
	if m.readOnly {
		return ErrReadOnly
	}
	m.secrets[key] = value
	return nil
}

func (m *mockBackend) Delete(key string) error {
	if m.readOnly {
		return ErrReadOnly
	}
	delete(m.secrets, key)
	return nil
}

func (m *mockBackend) List() ([]string, error) {
	keys := make([]string, 0, len(m.secrets))
	for k := range m.secrets {
		keys = append(keys, k)
	}
	return keys, nil
}

func (m *mockBackend) Name() string {
	return m.name
}

func (m *mockBackend) ReadOnly() bool {
	return m.readOnly
}

func TestManager_Get_PriorityOrder(t *testing.T) {
	primary := newMockBackend("primary", false)
	primary.secrets["key"] = "primary-value"

	secondary := newMockBackend("secondary", false)
	secondary.secrets["key"] = "secondary-value"

	mgr := NewManager(primary, secondary)

	value, source, err := mgr.Get("key")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if value != "primary-value" {
		t.Errorf("Get() value = %v, want primary-value", value)
	}
	if source != "primary" {
		t.Errorf("Get() source = %v, want primary", source)
	}
}

func TestManager_Get_Fallback(t *testing.T) {
	primary := newMockBackend("primary", false)
	// primary has no value for "key"

	secondary := newMockBackend("secondary", false)
	secondary.secrets["key"] = "secondary-value"

	mgr := NewManager(primary, secondary)

	value, source, err := mgr.Get("key")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if value != "secondary-value" {
		t.Errorf("Get() value = %v, want secondary-value", value)
	}
	if source != "secondary" {
		t.Errorf("Get() source = %v, want secondary", source)
	}
}

func TestManager_Get_NotFound(t *testing.T) {
	primary := newMockBackend("primary", false)
	secondary := newMockBackend("secondary", false)

	mgr := NewManager(primary, secondary)

	_, _, err := mgr.Get("nonexistent")
	if err != ErrNotFound {
		t.Errorf("Get() error = %v, want ErrNotFound", err)
	}
}

func TestManager_GetValue(t *testing.T) {
	primary := newMockBackend("primary", false)
	primary.secrets["key"] = "value"

	mgr := NewManager(primary)

	value := mgr.GetValue("key")
	if value != "value" {
		t.Errorf("GetValue() = %v, want value", value)
	}

	// Non-existent key should return empty string
	value = mgr.GetValue("nonexistent")
	if value != "" {
		t.Errorf("GetValue() = %v, want empty string", value)
	}
}

func TestManager_Set_WritesToPrimary(t *testing.T) {
	primary := newMockBackend("primary", false)
	secondary := newMockBackend("secondary", false)

	mgr := NewManager(primary, secondary)

	if err := mgr.Set("key", "new-value"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	if primary.secrets["key"] != "new-value" {
		t.Error("Set() should write to primary backend")
	}
	if _, ok := secondary.secrets["key"]; ok {
		t.Error("Set() should not write to secondary backend")
	}
}

func TestManager_Set_UsesFirstWritable(t *testing.T) {
	// Read-only backend first
	readOnly := newMockBackend("readonly", true)

	// Writable backend second
	writable := newMockBackend("writable", false)

	mgr := NewManager(readOnly, writable)

	if err := mgr.Set("key", "value"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	if writable.secrets["key"] != "value" {
		t.Error("Set() should write to first writable backend")
	}
}

func TestManager_Set_NoWritableBackend(t *testing.T) {
	readOnly1 := newMockBackend("readonly1", true)
	readOnly2 := newMockBackend("readonly2", true)

	mgr := NewManager(readOnly1, readOnly2)

	err := mgr.Set("key", "value")
	if err == nil {
		t.Error("Set() should return error when no writable backend")
	}
}

func TestManager_Delete(t *testing.T) {
	primary := newMockBackend("primary", false)
	primary.secrets["key"] = "value"

	mgr := NewManager(primary)

	if err := mgr.Delete("key"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	if _, ok := primary.secrets["key"]; ok {
		t.Error("Delete() should remove key from primary backend")
	}
}

func TestManager_Delete_NoWritableBackend(t *testing.T) {
	readOnly := newMockBackend("readonly", true)

	mgr := NewManager(readOnly)

	err := mgr.Delete("key")
	if err == nil {
		t.Error("Delete() should return error when no writable backend")
	}
}

func TestManager_List(t *testing.T) {
	primary := newMockBackend("primary", false)
	primary.secrets["key1"] = "value1"
	primary.secrets["key2"] = "value2"

	secondary := newMockBackend("secondary", false)
	secondary.secrets["key2"] = "value2-dup" // duplicate
	secondary.secrets["key3"] = "value3"

	mgr := NewManager(primary, secondary)

	keys, err := mgr.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	// Should have 3 unique keys
	if len(keys) != 3 {
		t.Errorf("List() returned %d keys, want 3", len(keys))
	}

	// Check all keys present
	keySet := make(map[string]bool)
	for _, k := range keys {
		keySet[k] = true
	}
	for _, expected := range []string{"key1", "key2", "key3"} {
		if !keySet[expected] {
			t.Errorf("List() missing key %s", expected)
		}
	}
}

func TestManager_Backends(t *testing.T) {
	primary := newMockBackend("primary", false)
	secondary := newMockBackend("secondary", false)

	mgr := NewManager(primary, secondary)

	backends := mgr.Backends()
	if len(backends) != 2 {
		t.Errorf("Backends() returned %d backends, want 2", len(backends))
	}
}

func TestManager_PrimaryBackend(t *testing.T) {
	readOnly := newMockBackend("readonly", true)
	writable := newMockBackend("writable", false)

	mgr := NewManager(readOnly, writable)

	primary := mgr.PrimaryBackend()
	if primary == nil {
		t.Fatal("PrimaryBackend() returned nil")
	}
	if primary.Name() != "writable" {
		t.Errorf("PrimaryBackend() = %s, want writable", primary.Name())
	}
}

func TestEnvBackend_Get(t *testing.T) {
	backend := NewEnvBackend()

	// Set a test env var
	os.Setenv("TEST_SECRET_KEY", "test-value")
	defer os.Unsetenv("TEST_SECRET_KEY")

	value, err := backend.Get("TEST_SECRET_KEY")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if value != "test-value" {
		t.Errorf("Get() = %v, want test-value", value)
	}
}

func TestEnvBackend_GetNonexistent(t *testing.T) {
	backend := NewEnvBackend()

	value, err := backend.Get("NONEXISTENT_VAR_12345")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if value != "" {
		t.Errorf("Get() = %v, want empty string", value)
	}
}

func TestEnvBackend_Set(t *testing.T) {
	backend := NewEnvBackend()

	err := backend.Set("key", "value")
	if err != ErrReadOnly {
		t.Errorf("Set() error = %v, want ErrReadOnly", err)
	}
}

func TestEnvBackend_Delete(t *testing.T) {
	backend := NewEnvBackend()

	err := backend.Delete("key")
	if err != ErrReadOnly {
		t.Errorf("Delete() error = %v, want ErrReadOnly", err)
	}
}

func TestEnvBackend_Name(t *testing.T) {
	backend := NewEnvBackend()
	if backend.Name() != "env" {
		t.Errorf("Name() = %v, want env", backend.Name())
	}
}

func TestEnvBackend_ReadOnly(t *testing.T) {
	backend := NewEnvBackend()
	if !backend.ReadOnly() {
		t.Error("ReadOnly() = false, want true")
	}
}

func TestEnvBackend_List(t *testing.T) {
	backend := NewEnvBackend()

	// Set some test env vars
	os.Setenv("TEST_API_KEY", "key1")
	os.Setenv("TEST_SECRET", "secret1")
	os.Setenv("TEST_NORMAL_VAR", "value") // shouldn't match (no secret-related suffix)
	defer func() {
		os.Unsetenv("TEST_API_KEY")
		os.Unsetenv("TEST_SECRET")
		os.Unsetenv("TEST_NORMAL_VAR")
	}()

	keys, err := backend.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	// Check that our test keys are included
	keySet := make(map[string]bool)
	for _, k := range keys {
		keySet[k] = true
	}

	if !keySet["TEST_API_KEY"] {
		t.Error("List() should include TEST_API_KEY")
	}
	if !keySet["TEST_SECRET"] {
		t.Error("List() should include TEST_SECRET")
	}
	if keySet["TEST_NORMAL_VAR"] {
		t.Error("List() should not include TEST_NORMAL_VAR")
	}
}

func TestErrors(t *testing.T) {
	if ErrNotFound == nil {
		t.Error("ErrNotFound should not be nil")
	}
	if ErrReadOnly == nil {
		t.Error("ErrReadOnly should not be nil")
	}
}
