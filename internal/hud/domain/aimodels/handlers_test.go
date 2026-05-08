package aimodels

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/crb2nu/loom/pkg/aimodels"
)

type fakeDeps struct {
	resolver *aimodels.Resolver
}

func (f *fakeDeps) WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeDeps) WriteError(w http.ResponseWriter, status int, msg string, _ error) {
	http.Error(w, msg, status)
}

func (f *fakeDeps) AIModelsResolver() *aimodels.Resolver { return f.resolver }

func TestHandleRoles_DefaultResolver(t *testing.T) {
	t.Parallel()
	deps := &fakeDeps{resolver: aimodels.DefaultResolver()}
	d := New(deps)

	req := httptest.NewRequest(http.MethodGet, "/api/aimodels/roles", nil)
	rec := httptest.NewRecorder()
	d.handleRoles(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Roles []roleEntry `json:"roles"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if len(body.Roles) != len(aimodels.AllRoles()) {
		t.Errorf("roles count = %d, want %d", len(body.Roles), len(aimodels.AllRoles()))
	}
	// Roles must be sorted alphabetically by role name.
	for i := 1; i < len(body.Roles); i++ {
		if body.Roles[i-1].Role > body.Roles[i].Role {
			t.Errorf("roles not sorted: %v at index %d > %v at index %d",
				body.Roles[i-1].Role, i-1, body.Roles[i].Role, i)
		}
	}
	// Spot-check a known role primary.
	for _, r := range body.Roles {
		if r.Role == string(aimodels.RoleWeaverRouter) && r.Primary != "qwen3-1p7b-tools-radeonvii" {
			t.Errorf("weaver-router primary = %q, want qwen3-1p7b-tools-radeonvii", r.Primary)
		}
	}
}

func TestHandleRoles_NilResolverGivesEmptyList(t *testing.T) {
	t.Parallel()
	deps := &fakeDeps{resolver: nil}
	d := New(deps)

	req := httptest.NewRequest(http.MethodGet, "/api/aimodels/roles", nil)
	rec := httptest.NewRecorder()
	d.handleRoles(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even on nil resolver; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Roles []roleEntry `json:"roles"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Roles) != 0 {
		t.Errorf("nil resolver should return empty roles, got %d", len(body.Roles))
	}
}
