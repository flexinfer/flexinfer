package daemon

// HubDelegateConfig controls which MCP servers are delegated to the hub
// instead of being spawned locally. When the hub is connected and healthy,
// calls for servers in the delegate list are routed through the hub WebSocket
// transparently. If the hub is down, normal local routing takes over.
type HubDelegateConfig struct {
	// Servers lists MCP server names eligible for hub delegation.
	// When a server name is in this list and the hub is healthy,
	// the daemon skips local subprocess spawning and routes the
	// call through the hub pool.
	Servers []string `yaml:"servers,omitempty"`
}

// DefaultHubDelegateServers returns the default list of servers eligible
// for hub delegation. These are cluster-hosted services that benefit from
// running in-cluster rather than as local subprocesses.
func DefaultHubDelegateServers() []string {
	return []string{"agent_context", "devbox"}
}

// hubDelegateEligible reports whether the named server should be delegated
// to the hub. Delegation requires all three conditions:
//  1. The server name is in the configured delegate list.
//  2. The hub connection infrastructure is available (hubPool and hubClient).
//  3. Hub auth has not been disabled due to authentication failures.
//
// The check is lightweight: no network calls, only field reads.
func (d *Daemon) hubDelegateEligible(serverName string) bool {
	if d.hubPool == nil || d.hubClient == nil {
		return false
	}
	if d.hubAuthDisabled {
		return false
	}

	servers := d.fileCfg.HubDelegate.Servers
	if len(servers) == 0 {
		return false
	}

	for _, s := range servers {
		if s == serverName {
			return true
		}
	}
	return false
}
