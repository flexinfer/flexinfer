package aimodels

import (
	"net/http"
	"sort"

	"github.com/crb2nu/loom/pkg/aimodels"
)

// roleEntry is the per-role payload returned by /api/aimodels/roles.
type roleEntry struct {
	Role      string   `json:"role"`
	Primary   string   `json:"primary"`
	Fallbacks []string `json:"fallbacks"`
}

// handleRoles returns every role the resolver knows about, sorted by
// role name for stable rendering. Empty resolver yields an empty list
// rather than 500 — operators on a fresh daemon should see the page
// load rather than an error toast.
func (d *AIModelsDomain) handleRoles(w http.ResponseWriter, _ *http.Request) {
	r := d.deps.AIModelsResolver()
	if r == nil {
		d.deps.WriteJSON(w, http.StatusOK, map[string]any{
			"roles": []roleEntry{},
		})
		return
	}

	specs := r.Roles()
	out := make([]roleEntry, 0, len(specs))
	for role, spec := range specs {
		out = append(out, roleEntry{
			Role:      string(role),
			Primary:   spec.Primary,
			Fallbacks: spec.Fallbacks,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Role < out[j].Role })

	d.deps.WriteJSON(w, http.StatusOK, map[string]any{
		"roles":             out,
		"override_path":     aimodels.DefaultPath(),
		"all_known_roles":   aimodels.AllRoles(),
		"baked_in_defaults": true,
	})
}
