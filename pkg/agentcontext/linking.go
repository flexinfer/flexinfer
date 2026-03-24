package agentcontext

import (
	"strings"

	"github.com/crb2nu/loom/pkg/projectmeta"
)

func pipelineRefFromValue(v any) *PipelineRef {
	m := toMapStringAny(v)
	if m == nil {
		return nil
	}
	ref := &PipelineRef{
		ID:      toInt(m["id"]),
		Project: strings.TrimSpace(toString(m["project"])),
		Ref:     strings.TrimSpace(toString(m["ref"])),
		WebURL:  strings.TrimSpace(toString(m["web_url"])),
	}
	if ref.ID <= 0 && ref.Project == "" && ref.Ref == "" && ref.WebURL == "" {
		return nil
	}
	return ref
}

func pipelineRefFromLegacyArgs(args map[string]any) *PipelineRef {
	if ref := pipelineRefFromValue(args["pipeline_ref"]); ref != nil {
		return ref
	}
	project := strings.TrimSpace(toString(args["pipeline_project"]))
	id := toInt(args["pipeline_id"])
	if id <= 0 && project == "" {
		return nil
	}
	return &PipelineRef{
		ID:      id,
		Project: project,
		Ref:     strings.TrimSpace(toString(args["pipeline_ref_name"])),
		WebURL:  strings.TrimSpace(toString(args["pipeline_web_url"])),
	}
}

func pipelineRefToPayload(ref *PipelineRef) map[string]any {
	if ref == nil {
		return nil
	}
	project := strings.TrimSpace(ref.Project)
	refName := strings.TrimSpace(ref.Ref)
	webURL := strings.TrimSpace(ref.WebURL)
	if ref.ID <= 0 && project == "" && refName == "" && webURL == "" {
		return nil
	}
	return map[string]any{
		"id":      ref.ID,
		"project": project,
		"ref":     refName,
		"web_url": webURL,
	}
}

func canonicalProject(explicitProject, namespace string, pipelineRef *PipelineRef) string {
	if pipelineRef != nil && strings.TrimSpace(pipelineRef.Project) != "" {
		return projectmeta.Canonical(pipelineRef.Project, namespace)
	}
	return projectmeta.Canonical(explicitProject, namespace)
}
