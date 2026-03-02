package agentcontext

import (
	"context"
	"fmt"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

type TemplateSvc struct{ *Service }

func (s *TemplateSvc) HandleTemplateCreate(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := v.Required("name")
	description := v.String("description", "")
	namespace := v.String("namespace", "")
	fromSessionID := v.String("from_session_id", "")
	createdBy := v.String("created_by", s.cfg.DefaultAgentID)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	now := time.Now()
	template := SessionTemplate{
		ID:          GenerateID(createdBy, name, namespace, now),
		Name:        name,
		Description: description,
		Namespace:   namespace,
		CreatedBy:   createdBy,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	// Parse entry types to include
	if types := toStringSlice(args["entry_types_to_include"]); len(types) > 0 {
		for _, t := range types {
			template.EntryTypesToInclude = append(template.EntryTypesToInclude, EntryType(t))
		}
	}

	// Copy from existing session if specified
	if fromSessionID != "" {
		entries, _ := s.qdrant.Get(CollContext).Scroll(ctx, FilterMust(Match("session_id", fromSessionID)), 100)
		for _, e := range entries {
			// Filter by entry types if specified
			if len(template.EntryTypesToInclude) > 0 {
				found := false
				for _, t := range template.EntryTypesToInclude {
					if e.EntryType == t {
						found = true
						break
					}
				}
				if !found {
					continue
				}
			}
			template.InitialEntries = append(template.InitialEntries, e)
		}
	}

	// Store template (use dummy vector since not searching by content)
	dummyVector := make([]float64, sessionsVectorSize)
	if err := s.qdrant.Get(CollTemplates).EnsureCollection(ctx, sessionsVectorSize); err != nil {
		return mcp.ErrorResult(fmt.Errorf("ensure collection: %w", err)), nil
	}

	point := Point{
		ID:      template.ID,
		Vector:  dummyVector,
		Payload: templateToPayload(template),
	}

	if err := s.qdrant.Get(CollTemplates).Upsert(ctx, []Point{point}, true); err != nil {
		return mcp.ErrorResult(fmt.Errorf("create template: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":          true,
		"template_id": template.ID,
		"name":        template.Name,
		"entry_count": len(template.InitialEntries),
	})
}

func (s *TemplateSvc) HandleTemplateList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	namespace := v.String("namespace", "")
	limit := v.Int("limit", 50)

	var filter map[string]any
	if namespace != "" {
		filter = FilterMust(Match("namespace", namespace))
	}

	points, err := s.qdrant.Get(CollTemplates).ScrollPoints(ctx, filter, limit, false)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("list templates: %w", err)), nil
	}

	templates := make([]map[string]any, 0, len(points))
	for _, p := range points {
		templates = append(templates, map[string]any{
			"id":          p.Payload["id"],
			"name":        p.Payload["name"],
			"description": p.Payload["description"],
			"namespace":   p.Payload["namespace"],
			"created_by":  p.Payload["created_by"],
			"created_at":  p.Payload["created_at"],
		})
	}

	return mcp.JSONResult(map[string]any{
		"ok":        true,
		"templates": templates,
		"count":     len(templates),
	})
}

func templateToPayload(t SessionTemplate) map[string]any {
	entryTypes := make([]string, len(t.EntryTypesToInclude))
	for i, et := range t.EntryTypesToInclude {
		entryTypes[i] = string(et)
	}

	return map[string]any{
		"id":                     t.ID,
		"name":                   t.Name,
		"description":            t.Description,
		"namespace":              t.Namespace,
		"created_by":             t.CreatedBy,
		"entry_types_to_include": entryTypes,
		"initial_entries_count":  len(t.InitialEntries),
		"created_at":             t.CreatedAt.Format(time.RFC3339Nano),
		"updated_at":             t.UpdatedAt.Format(time.RFC3339Nano),
	}
}
