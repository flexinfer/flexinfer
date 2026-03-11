package hud

import (
	"net/http"
	"sort"
	"strings"

	"github.com/crb2nu/loom/pkg/registry"
)

type catalogAPIEntry struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Categories  []string `json:"categories,omitempty"`
	Enabled     bool     `json:"enabled"`
	Running     bool     `json:"running"`
}

func (a *App) handleCatalogList(w http.ResponseWriter, r *http.Request) {
	reg, regPath := a.loadRegistry()
	if reg == nil {
		a.writeJSON(w, http.StatusOK, map[string]any{
			"servers":       []catalogAPIEntry{},
			"count":         0,
			"registry_path": "",
		})
		return
	}

	cs, _ := registry.LoadCatalogState()
	categoryFilter := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("category")))

	// Build the running set from health monitor if available.
	runningSet := make(map[string]bool)
	if a.healthMonitor != nil {
		for _, srv := range a.healthMonitor.Servers() {
			runningSet[srv.Name] = srv.Running
		}
	}

	entries := make([]catalogAPIEntry, 0, len(reg.Servers))
	for _, srv := range reg.Servers {
		if srv == nil {
			continue
		}
		if categoryFilter != "" && !serverHasCategoryCI(srv, categoryFilter) {
			continue
		}

		desc := ""
		if srv.Common != nil {
			desc = strings.TrimSpace(srv.Common.Description)
		}

		entries = append(entries, catalogAPIEntry{
			Name:        srv.Name,
			Description: desc,
			Categories:  srv.Categories,
			Enabled:     cs == nil || !cs.IsDisabled(srv.Name),
			Running:     runningSet[srv.Name],
		})
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	a.writeJSON(w, http.StatusOK, map[string]any{
		"servers":       entries,
		"count":         len(entries),
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

func serverHasCategoryCI(srv *registry.Server, needle string) bool {
	for _, c := range srv.Categories {
		if strings.EqualFold(strings.TrimSpace(c), needle) {
			return true
		}
	}
	return false
}
