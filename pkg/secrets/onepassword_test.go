package secrets

import (
	"errors"
	"testing"
)

func TestOnePasswordBackend_NewOnePasswordBackend_OpNotFound(t *testing.T) {
	mock := newMockExecutor()
	mock.lookPathResult["op"] = errors.New("not found")

	_, err := NewOnePasswordBackendWithExecutor("", mock)
	if err == nil {
		t.Error("NewOnePasswordBackendWithExecutor() should return error when op not found")
	}
}

func TestOnePasswordBackend_NewOnePasswordBackend_NotAuthenticated(t *testing.T) {
	mock := newMockExecutor()
	mock.addRunResult(nil, []byte("not signed in"), errors.New("exit 1"))

	_, err := NewOnePasswordBackendWithExecutor("", mock)
	if err == nil {
		t.Error("NewOnePasswordBackendWithExecutor() should return error when not authenticated")
	}
}

func TestOnePasswordBackend_NewOnePasswordBackend_Success(t *testing.T) {
	mock := newMockExecutor()
	mock.addRunResult([]byte(`{"email":"test@example.com"}`), nil, nil)

	backend, err := NewOnePasswordBackendWithExecutor("", mock)
	if err != nil {
		t.Fatalf("NewOnePasswordBackendWithExecutor() error = %v", err)
	}
	if backend == nil {
		t.Error("NewOnePasswordBackendWithExecutor() returned nil backend")
	}
	if backend.Name() != "1password" {
		t.Errorf("Name() = %v, want 1password", backend.Name())
	}
	if backend.ReadOnly() {
		t.Error("ReadOnly() = true, want false")
	}
}

func TestOnePasswordBackend_NewOnePasswordBackend_WithVault(t *testing.T) {
	mock := newMockExecutor()
	mock.addRunResult([]byte(`{"email":"test@example.com"}`), nil, nil)

	backend, err := NewOnePasswordBackendWithExecutor("my-vault", mock)
	if err != nil {
		t.Fatalf("NewOnePasswordBackendWithExecutor() error = %v", err)
	}
	if backend.vault != "my-vault" {
		t.Errorf("vault = %v, want my-vault", backend.vault)
	}
}

func TestOnePasswordBackend_Get_Success(t *testing.T) {
	mock := newMockExecutor()
	// whoami
	mock.addRunResult([]byte(`{"email":"test@example.com"}`), nil, nil)

	backend, _ := NewOnePasswordBackendWithExecutor("", mock)

	// item get
	itemJSON := `{
		"id": "abc123",
		"title": "my-secret",
		"fields": [
			{"id": "credential", "label": "credential", "value": "secret-value"},
			{"id": "username", "label": "username", "value": "user@example.com"}
		]
	}`
	mock.addRunResult([]byte(itemJSON), nil, nil)

	value, err := backend.Get("my-secret")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if value != "secret-value" {
		t.Errorf("Get() = %v, want secret-value", value)
	}

	// Verify command called
	getCall := mock.runCalls[1]
	if getCall[0] != "op" || getCall[1] != "item" || getCall[2] != "get" {
		t.Errorf("unexpected command: %v", getCall)
	}
}

func TestOnePasswordBackend_Get_WithField(t *testing.T) {
	mock := newMockExecutor()
	mock.addRunResult([]byte(`{"email":"test@example.com"}`), nil, nil)

	backend, _ := NewOnePasswordBackendWithExecutor("", mock)

	itemJSON := `{
		"id": "abc123",
		"title": "my-secret",
		"fields": [
			{"id": "credential", "label": "credential", "value": "cred-value"},
			{"id": "username", "label": "username", "value": "user-value"}
		]
	}`
	mock.addRunResult([]byte(itemJSON), nil, nil)

	value, err := backend.Get("my-secret/username")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if value != "user-value" {
		t.Errorf("Get() = %v, want user-value", value)
	}
}

func TestOnePasswordBackend_Get_NotFound(t *testing.T) {
	mock := newMockExecutor()
	mock.addRunResult([]byte(`{"email":"test@example.com"}`), nil, nil)

	backend, _ := NewOnePasswordBackendWithExecutor("", mock)

	mock.addRunResult(nil, []byte("item not found"), errors.New("exit 1"))

	value, err := backend.Get("missing-item")
	if err != nil {
		t.Fatalf("Get() error = %v (should be nil for not found)", err)
	}
	if value != "" {
		t.Errorf("Get() = %v, want empty string", value)
	}
}

func TestOnePasswordBackend_Get_FieldNotFound(t *testing.T) {
	mock := newMockExecutor()
	mock.addRunResult([]byte(`{"email":"test@example.com"}`), nil, nil)

	backend, _ := NewOnePasswordBackendWithExecutor("", mock)

	itemJSON := `{
		"id": "abc123",
		"title": "my-secret",
		"fields": [
			{"id": "other", "label": "other", "value": "other-value"}
		]
	}`
	mock.addRunResult([]byte(itemJSON), nil, nil)

	value, err := backend.Get("my-secret/missing-field")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if value != "" {
		t.Errorf("Get() = %v, want empty string", value)
	}
}

func TestOnePasswordBackend_Get_Cached(t *testing.T) {
	mock := newMockExecutor()
	mock.addRunResult([]byte(`{"email":"test@example.com"}`), nil, nil)

	backend, _ := NewOnePasswordBackendWithExecutor("", mock)

	// First get - fetches from op
	itemJSON := `{
		"id": "abc123",
		"title": "cached-item",
		"fields": [
			{"id": "credential", "label": "credential", "value": "cached-value"}
		]
	}`
	mock.addRunResult([]byte(itemJSON), nil, nil)

	value1, _ := backend.Get("cached-item")

	// Second get - should use cache (no new run call)
	callCountBefore := len(mock.runCalls)
	value2, _ := backend.Get("cached-item")

	if value1 != value2 {
		t.Errorf("cached value mismatch: %v != %v", value1, value2)
	}
	if len(mock.runCalls) != callCountBefore {
		t.Error("Get() should use cache for repeated calls")
	}
}

func TestOnePasswordBackend_Get_WithVault(t *testing.T) {
	mock := newMockExecutor()
	mock.addRunResult([]byte(`{"email":"test@example.com"}`), nil, nil)

	backend, _ := NewOnePasswordBackendWithExecutor("my-vault", mock)

	itemJSON := `{"id": "abc", "title": "test", "fields": [{"label": "credential", "value": "v"}]}`
	mock.addRunResult([]byte(itemJSON), nil, nil)

	backend.Get("test-item")

	// Verify --vault flag was passed
	getCall := mock.runCalls[1]
	hasVault := false
	for i, arg := range getCall {
		if arg == "--vault" && i+1 < len(getCall) && getCall[i+1] == "my-vault" {
			hasVault = true
			break
		}
	}
	if !hasVault {
		t.Errorf("Get() should include --vault flag: %v", getCall)
	}
}

func TestOnePasswordBackend_Get_InvalidJSON(t *testing.T) {
	mock := newMockExecutor()
	mock.addRunResult([]byte(`{"email":"test@example.com"}`), nil, nil)

	backend, _ := NewOnePasswordBackendWithExecutor("", mock)

	mock.addRunResult([]byte("not valid json"), nil, nil)

	_, err := backend.Get("test-item")
	if err == nil {
		t.Error("Get() should return error for invalid JSON")
	}
}

func TestOnePasswordBackend_Get_Error(t *testing.T) {
	mock := newMockExecutor()
	mock.addRunResult([]byte(`{"email":"test@example.com"}`), nil, nil)

	backend, _ := NewOnePasswordBackendWithExecutor("", mock)

	mock.addRunResult(nil, []byte("some error"), errors.New("exit 1"))

	_, err := backend.Get("test-item")
	if err == nil {
		t.Error("Get() should return error on failure")
	}
}

func TestOnePasswordBackend_Set_CreateNew(t *testing.T) {
	mock := newMockExecutor()
	mock.addRunResult([]byte(`{"email":"test@example.com"}`), nil, nil)

	backend, _ := NewOnePasswordBackendWithExecutor("", mock)

	// item get fails (item doesn't exist)
	mock.addRunResult(nil, []byte("not found"), errors.New("exit 1"))
	// item create succeeds
	mock.addRunResult(nil, nil, nil)

	err := backend.Set("new-item", "new-value")
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	// Verify create command
	createCall := mock.runCalls[2]
	if createCall[0] != "op" || createCall[1] != "item" || createCall[2] != "create" {
		t.Errorf("unexpected create command: %v", createCall)
	}
}

func TestOnePasswordBackend_Set_UpdateExisting(t *testing.T) {
	mock := newMockExecutor()
	mock.addRunResult([]byte(`{"email":"test@example.com"}`), nil, nil)

	backend, _ := NewOnePasswordBackendWithExecutor("", mock)

	// item get succeeds (item exists)
	mock.addRunResult([]byte(`{}`), nil, nil)
	// item edit succeeds
	mock.addRunResult(nil, nil, nil)

	err := backend.Set("existing-item", "updated-value")
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	// Verify edit command
	editCall := mock.runCalls[2]
	if editCall[0] != "op" || editCall[1] != "item" || editCall[2] != "edit" {
		t.Errorf("unexpected edit command: %v", editCall)
	}
}

func TestOnePasswordBackend_Set_CreateError(t *testing.T) {
	mock := newMockExecutor()
	mock.addRunResult([]byte(`{"email":"test@example.com"}`), nil, nil)

	backend, _ := NewOnePasswordBackendWithExecutor("", mock)

	// item get fails
	mock.addRunResult(nil, []byte("not found"), errors.New("exit 1"))
	// item create fails
	mock.addRunResult(nil, []byte("create failed"), errors.New("exit 1"))

	err := backend.Set("new-item", "new-value")
	if err == nil {
		t.Error("Set() should return error on create failure")
	}
}

func TestOnePasswordBackend_Set_EditError(t *testing.T) {
	mock := newMockExecutor()
	mock.addRunResult([]byte(`{"email":"test@example.com"}`), nil, nil)

	backend, _ := NewOnePasswordBackendWithExecutor("", mock)

	// item get succeeds
	mock.addRunResult([]byte(`{}`), nil, nil)
	// item edit fails
	mock.addRunResult(nil, []byte("edit failed"), errors.New("exit 1"))

	err := backend.Set("existing-item", "value")
	if err == nil {
		t.Error("Set() should return error on edit failure")
	}
}

func TestOnePasswordBackend_Set_UpdatesCache(t *testing.T) {
	mock := newMockExecutor()
	mock.addRunResult([]byte(`{"email":"test@example.com"}`), nil, nil)

	backend, _ := NewOnePasswordBackendWithExecutor("", mock)

	// item get fails, create succeeds
	mock.addRunResult(nil, []byte("not found"), errors.New("exit 1"))
	mock.addRunResult(nil, nil, nil)

	backend.Set("cache-test", "cached-val")

	// Subsequent get should use cache
	callCountBefore := len(mock.runCalls)
	value, _ := backend.Get("cache-test")

	if value != "cached-val" {
		t.Errorf("Get() = %v, want cached-val", value)
	}
	if len(mock.runCalls) != callCountBefore {
		t.Error("Get() should use cache after Set()")
	}
}

func TestOnePasswordBackend_Delete_Success(t *testing.T) {
	mock := newMockExecutor()
	mock.addRunResult([]byte(`{"email":"test@example.com"}`), nil, nil)

	backend, _ := NewOnePasswordBackendWithExecutor("", mock)

	mock.addRunResult(nil, nil, nil)

	err := backend.Delete("item-to-delete")
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	// Verify delete command
	deleteCall := mock.runCalls[1]
	if deleteCall[0] != "op" || deleteCall[1] != "item" || deleteCall[2] != "delete" {
		t.Errorf("unexpected delete command: %v", deleteCall)
	}
}

func TestOnePasswordBackend_Delete_NotFound(t *testing.T) {
	mock := newMockExecutor()
	mock.addRunResult([]byte(`{"email":"test@example.com"}`), nil, nil)

	backend, _ := NewOnePasswordBackendWithExecutor("", mock)

	mock.addRunResult(nil, []byte("not found"), errors.New("exit 1"))

	err := backend.Delete("missing-item")
	if err != nil {
		t.Fatalf("Delete() error = %v (should be nil for not found)", err)
	}
}

func TestOnePasswordBackend_Delete_Error(t *testing.T) {
	mock := newMockExecutor()
	mock.addRunResult([]byte(`{"email":"test@example.com"}`), nil, nil)

	backend, _ := NewOnePasswordBackendWithExecutor("", mock)

	mock.addRunResult(nil, []byte("delete failed"), errors.New("exit 1"))

	err := backend.Delete("item")
	if err == nil {
		t.Error("Delete() should return error on failure")
	}
}

func TestOnePasswordBackend_Delete_ClearsCache(t *testing.T) {
	mock := newMockExecutor()
	mock.addRunResult([]byte(`{"email":"test@example.com"}`), nil, nil)

	backend, _ := NewOnePasswordBackendWithExecutor("", mock)

	// Set up cache
	backend.cache["test-item"] = "cached-value"

	// Delete
	mock.addRunResult(nil, nil, nil)
	backend.Delete("test-item")

	// Check cache is cleared
	if _, ok := backend.cache["test-item"]; ok {
		t.Error("Delete() should clear cache entry")
	}
}

func TestOnePasswordBackend_List_Success(t *testing.T) {
	mock := newMockExecutor()
	mock.addRunResult([]byte(`{"email":"test@example.com"}`), nil, nil)

	backend, _ := NewOnePasswordBackendWithExecutor("", mock)

	listJSON := `[
		{"title": "item1"},
		{"title": "item2"},
		{"title": "item3"}
	]`
	mock.addRunResult([]byte(listJSON), nil, nil)

	keys, err := backend.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(keys) != 3 {
		t.Errorf("List() returned %d keys, want 3", len(keys))
	}

	keySet := make(map[string]bool)
	for _, k := range keys {
		keySet[k] = true
	}
	for _, expected := range []string{"item1", "item2", "item3"} {
		if !keySet[expected] {
			t.Errorf("List() missing %s", expected)
		}
	}
}

func TestOnePasswordBackend_List_Empty(t *testing.T) {
	mock := newMockExecutor()
	mock.addRunResult([]byte(`{"email":"test@example.com"}`), nil, nil)

	backend, _ := NewOnePasswordBackendWithExecutor("", mock)

	mock.addRunResult([]byte(`[]`), nil, nil)

	keys, err := backend.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("List() returned %d keys, want 0", len(keys))
	}
}

func TestOnePasswordBackend_List_Error(t *testing.T) {
	mock := newMockExecutor()
	mock.addRunResult([]byte(`{"email":"test@example.com"}`), nil, nil)

	backend, _ := NewOnePasswordBackendWithExecutor("", mock)

	mock.addRunResult(nil, []byte("list failed"), errors.New("exit 1"))

	_, err := backend.List()
	if err == nil {
		t.Error("List() should return error on failure")
	}
}

func TestOnePasswordBackend_List_InvalidJSON(t *testing.T) {
	mock := newMockExecutor()
	mock.addRunResult([]byte(`{"email":"test@example.com"}`), nil, nil)

	backend, _ := NewOnePasswordBackendWithExecutor("", mock)

	mock.addRunResult([]byte("not json"), nil, nil)

	_, err := backend.List()
	if err == nil {
		t.Error("List() should return error for invalid JSON")
	}
}

func TestOnePasswordBackend_List_WithVault(t *testing.T) {
	mock := newMockExecutor()
	mock.addRunResult([]byte(`{"email":"test@example.com"}`), nil, nil)

	backend, _ := NewOnePasswordBackendWithExecutor("my-vault", mock)

	mock.addRunResult([]byte(`[]`), nil, nil)

	backend.List()

	// Verify --vault flag
	listCall := mock.runCalls[1]
	hasVault := false
	for i, arg := range listCall {
		if arg == "--vault" && i+1 < len(listCall) && listCall[i+1] == "my-vault" {
			hasVault = true
			break
		}
	}
	if !hasVault {
		t.Errorf("List() should include --vault flag: %v", listCall)
	}
}

func TestParseKey(t *testing.T) {
	tests := []struct {
		input     string
		wantItem  string
		wantField string
	}{
		{"simple-item", "simple-item", "credential"},
		{"item/field", "item", "field"},
		{"item/field/subfield", "item", "field/subfield"},
		{"", "", "credential"},
	}

	for _, tt := range tests {
		item, field := parseKey(tt.input)
		if item != tt.wantItem {
			t.Errorf("parseKey(%q) item = %v, want %v", tt.input, item, tt.wantItem)
		}
		if field != tt.wantField {
			t.Errorf("parseKey(%q) field = %v, want %v", tt.input, field, tt.wantField)
		}
	}
}

func TestOnePasswordBackend_Interface(t *testing.T) {
	// Verify OnePasswordBackend implements Backend interface
	var _ Backend = (*OnePasswordBackend)(nil)
}
