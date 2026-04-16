package hud

import (
	"net/http"
	"strings"

	"github.com/crb2nu/loom/pkg/registry"
)

type catalogAPIEntry struct {
	registry.CatalogEntry
}

func (a *App) handleCatalogList(w http.ResponseWriter, r *http.Request) {
	reg, regPath := a.loadRegistry()
	if reg == nil {
		a.writeJSON(w, http.StatusOK, map[string]any{
			"servers":       []catalogAPIEntry{},
			"count":         0,
			"registry_path": regPath,
		})
		return
	}

	cs, _ := registry.LoadCatalogState()
	categoryFilter := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("category")))
	query := strings.TrimSpace(r.URL.Query().Get("query"))

	// Build the running set from health monitor if available.
	runningSet := make(map[string]bool)
	if a.healthMonitor != nil {
		for _, srv := range a.healthMonitor.Servers() {
			runningSet[srv.Name] = srv.Running
		}
	}

	entries := registry.BuildCatalogEntries(reg, cs, "", categoryFilter, query, runningSet)
	apiEntries := make([]catalogAPIEntry, len(entries))
	for i, entry := range entries {
		apiEntries[i] = catalogAPIEntry{CatalogEntry: entry}
	}

	a.writeJSON(w, http.StatusOK, map[string]any{
		"servers":       apiEntries,
		"count":         len(apiEntries),
		"registry_path": regPath,
	})
}

func (a *App) handleCatalogEnable(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		a.writeError(w, http.StatusBadRequest, "server name required", nil)
		return
	}

	cs, err := registry.LoadCatalogState()
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "load catalog state", err)
		return
	}

	changed := cs.Enable(name)
	if changed {
		if err := cs.Save(); err != nil {
			a.writeError(w, http.StatusInternalServerError, "save catalog state", err)
			return
		}
	}

	a.writeJSON(w, http.StatusOK, map[string]any{
		"server":  name,
		"enabled": true,
		"changed": changed,
	})
}

func (a *App) handleCatalogDisable(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		a.writeError(w, http.StatusBadRequest, "server name required", nil)
		return
	}

	cs, err := registry.LoadCatalogState()
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "load catalog state", err)
		return
	}

	changed := cs.Disable(name)
	if changed {
		if err := cs.Save(); err != nil {
			a.writeError(w, http.StatusInternalServerError, "save catalog state", err)
			return
		}
	}

	a.writeJSON(w, http.StatusOK, map[string]any{
		"server":  name,
		"enabled": false,
		"changed": changed,
	})
}

// loadRegistry resolves and loads the MCP server registry.
func (a *App) loadRegistry() (*registry.Registry, string) {
	if path := strings.TrimSpace(a.config.RegistryPath); path != "" {
		reg, err := registry.LoadWithDefaults(path)
		if err != nil {
			a.logger.Warn("failed to load configured HUD registry", "path", path, "error", err)
			return nil, path
		}
		return reg, path
	}

	path, found := registry.FindRegistry()
	if !found {
		return nil, ""
	}
	reg, err := registry.LoadWithDefaults(path)
	if err != nil {
		return nil, path
	}
	return reg, path
}
