package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/hive/squads"
	"github.com/crb2nu/loom/pkg/hive/store"
)

const fixtureSquadHUD = `apiVersion: hive.loom.dev/v1
kind: Squad
metadata:
  name: hud-frontend
spec:
  paths:
    - "internal/hud/frontend/**"
  tests:
    - pnpm-typecheck
  gates:
    required: [pr_self_review, scope]
    advisory: [coverage]
  ensemble:
    editor:
      backend: spawn
      driver: claude-opus
      max_cost_usd: 4.0
  budget_share: 0.30
  recursion_enabled: false
`

const fixtureSquadGitops = `apiVersion: hive.loom.dev/v1
kind: Squad
metadata:
  name: gitops
spec:
  paths:
    - "platform/gitops/**"
  tests:
    - kustomize-build
  gates:
    required: [diff_size, scope]
  budget_share: 0.20
`

// newOperatorWithSquads wires the standard test operator and additionally
// boots a squads.Loader against a temp manifest dir seeded with the given
// YAML files. Returns the operator + a cleanup. The squads.Loader runs
// with SkipWatch=true so the test isn't dependent on fsnotify timing.
func newOperatorWithSquads(t *testing.T, manifests map[string]string) (*operator, func()) {
	t.Helper()
	op, base := newTestOperator(t)

	dir := t.TempDir()
	for name, body := range manifests {
		path := filepath.Join(dir, name+".yaml")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write manifest %s: %v", name, err)
		}
	}
	loader, err := squads.NewLoader(context.Background(), dir, op.store,
		squads.LoaderOptions{SkipWatch: true})
	if err != nil {
		base()
		t.Fatalf("squads loader: %v", err)
	}
	op.withSquadsLoader(loader)

	cleanup := func() {
		_ = loader.Close()
		base()
	}
	return op, cleanup
}

func TestHandleSquadsList_EmptyReturns200WithEmptyArray(t *testing.T) {
	// No squads loader at all — operator must still return a non-500
	// empty list so the HUD can render an empty state.
	op, cleanup := newTestOperator(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/hive/squads", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	body := strings.TrimSpace(rec.Body.String())
	// Tolerate either "[]" or "null" — handler may emit either for an empty list.
	if body != "[]" && body != "null" {
		t.Errorf("empty body: got %q want []|null", body)
	}
}

func TestHandleSquadsList_ReturnsLoadedSquads(t *testing.T) {
	op, cleanup := newOperatorWithSquads(t, map[string]string{
		"hud-frontend": fixtureSquadHUD,
		"gitops":       fixtureSquadGitops,
	})
	defer cleanup()

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/hive/squads", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"hud-frontend"`, `"gitops"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q: %s", want, body)
		}
	}
}

func TestHandleSquadGet_Returns404ForUnknown(t *testing.T) {
	op, cleanup := newOperatorWithSquads(t, map[string]string{
		"hud-frontend": fixtureSquadHUD,
	})
	defer cleanup()

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/hive/squads/does-not-exist", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown squad, got %d", rec.Code)
	}
}

func TestHandleSquadGet_ReturnsDetail(t *testing.T) {
	op, cleanup := newOperatorWithSquads(t, map[string]string{
		"hud-frontend": fixtureSquadHUD,
	})
	defer cleanup()

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/hive/squads/hud-frontend", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"hud-frontend"`) {
		t.Errorf("body missing squad name: %s", body)
	}
}

func TestHandleSquadMemory_HonorsLimitCap(t *testing.T) {
	op, cleanup := newOperatorWithSquads(t, map[string]string{
		"hud-frontend": fixtureSquadHUD,
	})
	defer cleanup()
	ctx := context.Background()

	// Seed 5 memory rows.
	for i := 0; i < 5; i++ {
		err := op.store.Squads.PutMemory(ctx, &store.SquadMemory{
			SquadName:  "hud-frontend",
			Kind:       store.SquadMemoryConvention,
			Title:      "convention-" + intStr(i),
			Body:       "rule body",
			Importance: 0.5 + 0.05*float64(i),
		})
		if err != nil {
			t.Fatalf("seed memory %d: %v", i, err)
		}
	}

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/hive/squads/hud-frontend/memory?limit=3", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	// Body should be a JSON array of 3 rows. Decode minimally to count.
	var rows []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		// Tolerate {data: [...]} or any envelope by counting Title occurrences.
		if got := strings.Count(rec.Body.String(), `"convention-`); got != 3 {
			t.Errorf("limit=3 should return 3 rows; counted %d titles in %s",
				got, rec.Body.String())
		}
		return
	}
	if len(rows) != 3 {
		t.Errorf("limit=3 should return 3 rows; got %d", len(rows))
	}
}

func TestHandleSquadMemory_KindFilter(t *testing.T) {
	op, cleanup := newOperatorWithSquads(t, map[string]string{
		"hud-frontend": fixtureSquadHUD,
	})
	defer cleanup()
	ctx := context.Background()

	mustPutMemory := func(kind store.SquadMemoryKind, title string) {
		t.Helper()
		err := op.store.Squads.PutMemory(ctx, &store.SquadMemory{
			SquadName:  "hud-frontend",
			Kind:       kind,
			Title:      title,
			Body:       "x",
			Importance: 0.6,
		})
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	mustPutMemory(store.SquadMemoryConvention, "convention-A")
	mustPutMemory(store.SquadMemoryTechDebt, "debt-B")
	mustPutMemory(store.SquadMemoryFollowup, "followup-C")

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/hive/squads/hud-frontend/memory?kind=tech_debt", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "debt-B") {
		t.Errorf("expected debt-B in filtered output: %s", body)
	}
	if strings.Contains(body, "convention-A") || strings.Contains(body, "followup-C") {
		t.Errorf("kind filter leaked other rows: %s", body)
	}
}

func TestHandleSquadRouteTest_RequiresAdminToken(t *testing.T) {
	setAdminToken("secret-abc")
	defer setAdminToken("")

	op, cleanup := newOperatorWithSquads(t, map[string]string{
		"hud-frontend": fixtureSquadHUD,
	})
	defer cleanup()

	body := bytes.NewBufferString(`{"backlog_id":"HIVE-X-1"}`)
	req := httptest.NewRequest(http.MethodPost,
		"/api/hive/squads/hud-frontend/route-test", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("missing token should be 401, got %d", rec.Code)
	}
}

func TestHandleSquadRouteTest_HappyPath(t *testing.T) {
	setAdminToken("secret-abc")
	defer setAdminToken("")

	op, cleanup := newOperatorWithSquads(t, map[string]string{
		"hud-frontend": fixtureSquadHUD,
	})
	defer cleanup()
	ctx := context.Background()

	// Seed a backlog item that hits the hud-frontend squad's paths.
	council := "COUNCIL-RT"
	if err := op.store.Council.Put(ctx, &store.CouncilRun{
		ID: council, Trigger: store.CouncilTriggerCron,
		StartedAt: time.Now().UTC(), Outcome: store.CouncilOutcomeSuccess,
	}); err != nil {
		t.Fatalf("seed council: %v", err)
	}
	if err := op.store.Backlog.Put(ctx, &store.BacklogItem{
		ID: "HIVE-RT-001", Title: "route-test", State: store.BacklogQueued,
		Priority: store.P2, CreatedBy: "test", CouncilRunID: &council,
		Slices: []store.Slice{{
			Name:  "main",
			Files: []string{"internal/hud/frontend/src/lib/components/SpawnPanel.svelte"},
		}},
	}); err != nil {
		t.Fatalf("seed backlog: %v", err)
	}

	body := bytes.NewBufferString(`{"backlog_id":"HIVE-RT-001"}`)
	req := httptest.NewRequest(http.MethodPost,
		"/api/hive/squads/hud-frontend/route-test", body)
	req.Header.Set("Authorization", "Bearer secret-abc")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	got := rec.Body.String()
	if !strings.Contains(got, `"hud-frontend"`) {
		t.Errorf("decision should mention hud-frontend: %s", got)
	}
	// Confidence + sample size + a candidates / decision shape should all
	// appear in the body. We don't pin the JSON envelope here (the
	// handler may use either a flat or nested shape) but verify the key
	// router-output keys round-trip.
	for _, want := range []string{`confidence`, `path_class`} {
		if !strings.Contains(got, want) {
			t.Errorf("body missing %q: %s", want, got)
		}
	}
}

func TestHandleSquadRouteTest_404ForUnknownBacklog(t *testing.T) {
	setAdminToken("secret-abc")
	defer setAdminToken("")

	op, cleanup := newOperatorWithSquads(t, map[string]string{
		"hud-frontend": fixtureSquadHUD,
	})
	defer cleanup()

	body := bytes.NewBufferString(`{"backlog_id":"DOES-NOT-EXIST"}`)
	req := httptest.NewRequest(http.MethodPost,
		"/api/hive/squads/hud-frontend/route-test", body)
	req.Header.Set("Authorization", "Bearer secret-abc")
	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown backlog id should be 404, got %d", rec.Code)
	}
}

// intStr is a small Itoa helper so the test file's import set stays
// minimal (the operator's existing helpers don't import strconv either).
func intStr(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
