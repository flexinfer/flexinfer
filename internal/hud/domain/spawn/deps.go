// deps.go defines the Deps interface and supporting types for spawn domain handlers.
// These interfaces decouple the spawn domain from the hud.App implementation,
// preventing import cycles and enabling testability.
package spawn

import (
	"context"
	"net/http"

	"github.com/crb2nu/loom/internal/hud/bridge"
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
	DeleteSpawn(ctx context.Context, spawnID string) error
	Projects() []string
	GetSpawnTelemetry(spawnID string) (*bridge.SpawnTelemetry, bool)
	// SendControlMessage appends a control command to a running multi-turn
	// spawn's control file. Errors are wrapped spawn.ErrSpawn* sentinels so
	// handlers can map them to precise HTTP statuses.
	SendControlMessage(ctx context.Context, spawnID string, cmd pkgspawn.ControlCommand) error
}
