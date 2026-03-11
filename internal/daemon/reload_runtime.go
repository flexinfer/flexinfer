package daemon

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/crb2nu/loom/pkg/registry"
)

func (d *Daemon) invalidateServersForReload(oldReg, newReg *registry.Registry) []string {
	if d == nil || d.procMgr == nil || newReg == nil {
		return nil
	}

	var invalidated []string
	for _, name := range d.procMgr.List() {
		oldSpec, _ := serverSpecForReload(oldReg, name, d.cfg.Target)
		newSpec, _ := serverSpecForReload(newReg, name, d.cfg.Target)

		reason := reloadInvalidationReason(oldSpec, newSpec)
		if reason == "" {
			continue
		}

		if d.logger != nil {
			d.logger.Info("invalidating running server after reload", "server", name, "reason", reason)
		}
		if d.pool != nil {
			d.pool.ClearServer(name)
		}
		if err := d.procMgr.Stop(name); err != nil && d.logger != nil {
			d.logger.Warn("failed to stop server during reload", "server", name, "error", err)
		}
		d.runningServers.Delete(name)
		if d.eventBus != nil {
			d.eventBus.Publish(EventProcessStop, map[string]any{
				"server": name,
				"reason": reason,
			})
		}
		invalidated = append(invalidated, name)
	}

	return invalidated
}

func serverSpecForReload(reg *registry.Registry, serverName, target string) (*registry.TargetSpec, error) {
	if reg == nil {
		return nil, nil
	}
	return reg.GetServerSpec(serverName, target)
}

func reloadInvalidationReason(oldSpec, newSpec *registry.TargetSpec) string {
	switch {
	case launchConfigChanged(oldSpec, newSpec):
		return "launch_config_changed"
	case usesRuntimeLaunchTemplates(newSpec):
		return "runtime_templates_present"
	default:
		return ""
	}
}

func launchConfigChanged(oldSpec, newSpec *registry.TargetSpec) bool {
	if oldSpec == nil || newSpec == nil {
		return oldSpec != newSpec
	}

	if oldSpec.Command != newSpec.Command || oldSpec.Type != newSpec.Type {
		return true
	}
	if !reflect.DeepEqual(oldSpec.Args, newSpec.Args) {
		return true
	}
	if !equalStringMap(oldSpec.Env, newSpec.Env) {
		return true
	}
	return !reflect.DeepEqual(oldSpec.SSH, newSpec.SSH)
}

func equalStringMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		if bv, ok := b[k]; !ok || av != bv {
			return false
		}
	}
	return true
}

func usesRuntimeLaunchTemplates(spec *registry.TargetSpec) bool {
	if spec == nil {
		return false
	}
	if containsRuntimeTemplateRef(spec.Command) || containsRuntimeTemplateRef(spec.Type) {
		return true
	}
	for _, arg := range spec.Args {
		if containsRuntimeTemplateRef(strings.TrimSpace(toString(arg))) {
			return true
		}
	}
	for _, value := range spec.Env {
		if containsRuntimeTemplateRef(value) {
			return true
		}
	}
	if spec.SSH == nil {
		return false
	}
	return containsRuntimeTemplateRef(spec.SSH.Host) ||
		containsRuntimeTemplateRef(spec.SSH.User) ||
		containsRuntimeTemplateRef(spec.SSH.KeyFile) ||
		containsRuntimeTemplateRef(spec.SSH.KnownHostsFile)
}

func containsRuntimeTemplateRef(value string) bool {
	return strings.Contains(value, "${env:") ||
		strings.Contains(value, "${keychain:") ||
		strings.Contains(value, "${secret:")
}

func toString(v any) string {
	return fmt.Sprint(v)
}
