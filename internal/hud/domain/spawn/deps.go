// deps.go defines the Deps interface and supporting types for spawn domain handlers.
// These interfaces decouple the spawn domain from the hud.App implementation,
// preventing import cycles and enabling testability.
package spawn

import (
	"context"
	"net/http"

	pkgspawn "github.com/crb2nu/loom/internal/spawn"
)

// Deps exposes the subset of App capabilities that spawn handlers need.
// The hud.App satisfies this interface via accessor methods in domain_adapters.go.
type Deps interface {
	WriteJSON(w http.ResponseWriter, status int, v any)
	WriteError(w http.ResponseWriter, status int, msg string, err error)
	RequireAdminToken(w http.ResponseWriter, r *http.Request) bool
	Spawner() SpawnerOps
}

// SpawnerOps is the subset of SpawnOrchestrator methods used by spawn handlers.
type SpawnerOps interface {
	Spawn(ctx context.Context, req pkgspawn.Request) (string, error)
	GetSpawn(spawnID string) (*pkgspawn.State, bool)
	ListSpawns() []*pkgspawn.State
	StopSpawn(ctx context.Context, spawnID string) error
	Projects() []string
}
