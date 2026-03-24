package bridge

import "github.com/crb2nu/loom/pkg/projectmeta"

// ProjectFromNamespace derives the project/repo identity from a namespace.
func ProjectFromNamespace(namespace string) string {
	return projectmeta.FromNamespace(namespace)
}

// CanonicalProject prefers an attached pipeline project when available, then
// falls back to an explicit project or namespace-derived project identity.
func CanonicalProject(project, namespace string, pipelineRef *PipelineRef) string {
	if pipelineRef != nil {
		return projectmeta.Canonical(pipelineRef.Project, namespace)
	}
	return projectmeta.Canonical(project, namespace)
}
