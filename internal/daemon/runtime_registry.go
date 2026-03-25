package daemon

import "github.com/crb2nu/loom/pkg/registry"

// runtimeRegistryForTarget normalizes the daemon's registry to a single
// already-merged target view so concurrent runtime GetServerSpec calls stay
// read-only instead of mutating shared target maps inside the registry layer.
func runtimeRegistryForTarget(reg *registry.Registry, target string) (*registry.Registry, error) {
	if reg == nil {
		return nil, nil
	}

	normalized := &registry.Registry{
		Version:             reg.Version,
		EnvAliases:          cloneEnvAliases(reg.EnvAliases),
		Servers:             make([]*registry.Server, 0, len(reg.Servers)),
		Routing:             cloneRoutingRules(reg.Routing),
		PlatformPermissions: clonePlatformPermissions(reg.PlatformPermissions),
		SandboxPolicy:       cloneSandboxPolicy(reg.SandboxPolicy),
	}

	for _, server := range reg.Servers {
		if server == nil {
			normalized.Servers = append(normalized.Servers, nil)
			continue
		}

		spec, err := reg.GetServerSpec(server.Name, target)
		if err != nil {
			return nil, err
		}

		normalized.Servers = append(normalized.Servers, &registry.Server{
			Name:       server.Name,
			URL:        server.URL,
			Categories: append([]string(nil), server.Categories...),
			Common:     cloneTargetSpec(spec),
		})
	}

	return normalized, nil
}

func cloneEnvAliases(src map[string]registry.EnvVar) map[string]registry.EnvVar {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]registry.EnvVar, len(src))
	for k, v := range src {
		dst[k] = registry.EnvVar{Fallbacks: append([]string(nil), v.Fallbacks...)}
	}
	return dst
}

func cloneRoutingRules(src []*registry.RoutingRule) []*registry.RoutingRule {
	if len(src) == 0 {
		return nil
	}
	dst := make([]*registry.RoutingRule, 0, len(src))
	for _, rule := range src {
		if rule == nil {
			dst = append(dst, nil)
			continue
		}
		clone := &registry.RoutingRule{
			ToolName: rule.ToolName,
			Argument: rule.Argument,
			Default:  rule.Default,
			Cases:    append([]registry.RoutingCase(nil), rule.Cases...),
		}
		dst = append(dst, clone)
	}
	return dst
}

func clonePlatformPermissions(src map[string]*registry.PlatformPermission) map[string]*registry.PlatformPermission {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]*registry.PlatformPermission, len(src))
	for k, perm := range src {
		if perm == nil {
			dst[k] = nil
			continue
		}
		dst[k] = &registry.PlatformPermission{
			Settings:              cloneAnyMap(perm.Settings),
			Allow:                 append([]string(nil), perm.Allow...),
			Deny:                  append([]string(nil), perm.Deny...),
			AdditionalDirectories: append([]string(nil), perm.AdditionalDirectories...),
		}
	}
	return dst
}

func cloneSandboxPolicy(src *registry.SandboxPolicy) *registry.SandboxPolicy {
	if src == nil {
		return nil
	}
	return &registry.SandboxPolicy{
		RequireSandbox:   append([]string(nil), src.RequireSandbox...),
		RecommendSandbox: append([]string(nil), src.RecommendSandbox...),
		AutoProvision:    src.AutoProvision,
		DefaultBackend:   src.DefaultBackend,
	}
}

func cloneTargetSpec(src *registry.TargetSpec) *registry.TargetSpec {
	if src == nil {
		return nil
	}
	dst := &registry.TargetSpec{
		Description: src.Description,
		Command:     src.Command,
		Args:        append([]any(nil), src.Args...),
		Hint:        src.Hint,
		Timeout:     src.Timeout,
		AlwaysAllow: append([]string(nil), src.AlwaysAllow...),
		Type:        src.Type,
		Tools:       append([]registry.ToolSchema(nil), src.Tools...),
	}
	if len(src.Env) > 0 {
		dst.Env = make(map[string]string, len(src.Env))
		for k, v := range src.Env {
			dst.Env[k] = v
		}
	}
	if src.SSH != nil {
		ssh := *src.SSH
		dst.SSH = &ssh
	}
	return dst
}

func cloneAnyMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
