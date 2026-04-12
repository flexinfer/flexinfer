package monitor

import (
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/notify"
)

const (
	// DefaultRingSize is the default number of latency readings to keep
	// for sparkline history (60 readings).
	DefaultRingSize = 60

	// notifyDebounce is the minimum interval between repeated server-down
	// notifications for the same server.
	notifyDebounce = 5 * time.Minute
)

// RingBuffer is a fixed-size circular buffer for sparkline data.
// It stores float64 values and overwrites the oldest when full.
type RingBuffer struct {
	data []float64
	size int
	head int // next write position
	len  int // current number of elements
}

// NewRingBuffer creates a RingBuffer with the given capacity.
func NewRingBuffer(size int) *RingBuffer {
	if size <= 0 {
		size = DefaultRingSize
	}
	return &RingBuffer{
		data: make([]float64, size),
		size: size,
	}
}

// Push adds a value to the ring buffer. If the buffer is full,
// the oldest value is overwritten.
func (rb *RingBuffer) Push(value float64) {
	rb.data[rb.head] = value
	rb.head = (rb.head + 1) % rb.size
	if rb.len < rb.size {
		rb.len++
	}
}

// Values returns all values in order from oldest to newest.
func (rb *RingBuffer) Values() []float64 {
	if rb.len == 0 {
		return nil
	}
	result := make([]float64, rb.len)
	// If the buffer is not full, values start at index 0.
	// If full, the oldest value is at rb.head (since head just wrapped).
	start := 0
	if rb.len == rb.size {
		start = rb.head
	}
	for i := range rb.len {
		result[i] = rb.data[(start+i)%rb.size]
	}
	return result
}

// Len returns the current number of elements in the buffer.
func (rb *RingBuffer) Len() int {
	return rb.len
}

// ServerHealthEntry is an enriched server record combining health status,
// server info, and latency sparkline history.
type ServerHealthEntry struct {
	Name        string   `json:"name"`
	Categories  []string `json:"categories,omitempty"`
	Description string   `json:"description,omitempty"`
	Running     bool     `json:"running"`

	// Health status (from the best available endpoint: local or hub)
	Healthy      bool    `json:"healthy"`
	ConsecFails  int     `json:"consec_fails"`
	AvgLatencyMs float64 `json:"avg_latency_ms"`
	ErrorMessage string  `json:"error_message,omitempty"`
	Target       string  `json:"target"`    // "local", "hub", or "unavailable"
	Transport    string  `json:"transport"` // "ws", "stdio", "sse", "ssh", or ""

	// Sparkline history (last DefaultRingSize readings, oldest first)
	LatencyHistory []float64 `json:"latency_history"`

	// Divergence between health monitor and router (nil when they agree).
	Divergence *bridge.HealthDivergence `json:"divergence,omitempty"`

	// Derived stats
	ToolCount int `json:"tool_count"`
}

// HealthSummary is the overall health summary across all servers.
type HealthSummary struct {
	TotalServers    int `json:"total_servers"`
	HealthyServers  int `json:"healthy_servers"`
	DegradedServers int `json:"degraded_servers"`
	DownServers     int `json:"down_servers"`
	IdleServers     int `json:"idle_servers"`
}

type healthClass string

const (
	healthClassHealthy  healthClass = "healthy"
	healthClassDegraded healthClass = "degraded"
	healthClassDown     healthClass = "down"
	healthClassIdle     healthClass = "idle"
)

// HealthMonitor tracks server health and maintains sparkline latency
// history for each server. It merges data from the Health() and Servers()
// bridge calls.
//
// HealthMonitor embeds BaseMonitor for lifecycle management (stop, pollLoop)
// but keeps its own complex Refresh implementation with internal state
// (sparkline history, notification dedup).
type HealthMonitor struct {
	BaseMonitor[[]ServerHealthEntry]
	client bridge.Caller

	history map[string]*RingBuffer // server name -> latency ring buffer
	summary HealthSummary

	// Notification debounce state (protected by base mu).
	notifiedDown map[string]time.Time // server name -> last notification time
	prevDown     map[string]bool      // servers that were down on previous refresh
}

// NewHealthMonitor creates a HealthMonitor backed by the given caller.
func NewHealthMonitor(client bridge.Caller, logger *slog.Logger) *HealthMonitor {
	m := &HealthMonitor{
		client:       client,
		history:      make(map[string]*RingBuffer),
		notifiedDown: make(map[string]time.Time),
		prevDown:     make(map[string]bool),
	}
	m.InitBase(logger, nil, "health-monitor")
	return m
}

// Start begins the background polling goroutine at the given interval.
func (m *HealthMonitor) Start(interval time.Duration) {
	m.StartLoop(interval, m.Refresh)
}

// Servers returns the current enriched server health entries.
func (m *HealthMonitor) Servers() []ServerHealthEntry {
	m.RLock()
	defer m.RUnlock()

	snap := m.GetSnapshot()
	// Return a copy to avoid data races on the slice.
	out := make([]ServerHealthEntry, len(snap))
	copy(out, snap)
	return out
}

// Summary returns the current health summary.
func (m *HealthMonitor) Summary() HealthSummary {
	m.RLock()
	defer m.RUnlock()
	return m.summary
}

// Refresh fetches health and server data, merges them, updates sparkline
// history, and recomputes the summary.
func (m *HealthMonitor) Refresh() error {
	// Fetch health data.
	var healthResult *bridge.HealthResult
	rawHealth, healthErr := m.client.Call("loom/health", nil)
	if healthErr != nil {
		m.Logger.Warn("health: failed to fetch health", "error", healthErr)
	} else {
		var hr bridge.HealthResult
		if err := json.Unmarshal(rawHealth, &hr); err != nil {
			m.Logger.Warn("health: failed to unmarshal health", "error", err)
			healthErr = err
		} else {
			healthResult = &hr
		}
	}

	// Fetch server list.
	var serversResult *bridge.ServersResult
	rawServers, serversErr := m.client.Call("loom/servers", nil)
	if serversErr != nil {
		m.Logger.Warn("health: failed to fetch servers", "error", serversErr)
	} else {
		var sr bridge.ServersResult
		if err := json.Unmarshal(rawServers, &sr); err != nil {
			m.Logger.Warn("health: failed to unmarshal servers", "error", err)
			serversErr = err
		} else {
			serversResult = &sr
		}
	}

	// Fetch aggregated tool list to derive per-server tool counts.
	// Tool names are namespaced as "server__toolname".
	toolCounts := make(map[string]int)
	rawTools, toolsErr := m.client.Call("loom/tools", nil)
	if toolsErr != nil {
		m.Logger.Debug("health: failed to fetch tools for counts", "error", toolsErr)
	} else {
		var toolsResult bridge.ToolsResult
		if err := json.Unmarshal(rawTools, &toolsResult); err != nil {
			m.Logger.Debug("health: failed to unmarshal tools", "error", err)
		} else {
			for _, t := range toolsResult.Tools {
				if parts := strings.SplitN(t.Name, "__", 2); len(parts) == 2 {
					toolCounts[parts[0]]++
				}
			}
		}
	}

	// If both failed, nothing to update.
	if healthErr != nil && serversErr != nil {
		return healthErr
	}

	// Build a map of server info by name for merging.
	serverMap := make(map[string]bridge.ServerInfo)
	if serversResult != nil {
		for _, s := range serversResult.Servers {
			serverMap[s.Name] = s
		}
	}

	// Build a map of health by name for merging.
	healthMap := make(map[string]bridge.ServerHealth)
	if healthResult != nil {
		for name, h := range healthResult.Servers {
			healthMap[name] = h
		}
	}

	// Collect all known server names (union of both sources).
	nameSet := make(map[string]struct{})
	for name := range serverMap {
		nameSet[name] = struct{}{}
	}
	for name := range healthMap {
		nameSet[name] = struct{}{}
	}

	m.Lock()

	entries := make([]ServerHealthEntry, 0, len(nameSet))
	summary := HealthSummary{TotalServers: len(nameSet)}

	for name := range nameSet {
		entry := ServerHealthEntry{
			Name:   name,
			Target: "unavailable",
		}

		// Merge server info if available.
		if info, ok := serverMap[name]; ok {
			entry.Categories = info.Categories
			entry.Description = info.Description
			entry.Running = info.Running
			if entry.Transport == "" {
				entry.Transport = info.Transport
			}
		}

		// Merge health info if available. Prefer the active target endpoint.
		if health, ok := healthMap[name]; ok {
			entry.Target = health.Target
			entry.Transport = health.Transport
			var active bridge.HealthEntry
			switch health.Target {
			case "local":
				active = health.Local
			case "hub":
				active = health.Hub
			default:
				// If target is something else, try local first, then hub.
				if health.Local.Healthy {
					active = health.Local
					entry.Target = "local"
				} else {
					active = health.Hub
					entry.Target = "hub"
				}
			}
			entry.Healthy = active.Healthy
			entry.ConsecFails = active.ConsecFails
			entry.AvgLatencyMs = active.AvgLatencyMs
			entry.ErrorMessage = active.ErrorMessage
			entry.Divergence = health.Divergence
		}

		// Set tool count from the parsed tool namespace.
		entry.ToolCount = toolCounts[name]

		// Update sparkline history.
		ring, ok := m.history[name]
		if !ok {
			ring = NewRingBuffer(DefaultRingSize)
			m.history[name] = ring
		}
		ring.Push(entry.AvgLatencyMs)
		entry.LatencyHistory = ring.Values()

		// Classify for summary.
		//
		// In hub/gateway mode, Running only reflects local process state.
		// A server can be healthy via hub target while local process is stopped.
		switch classifyHealthEntry(entry) {
		case healthClassIdle:
			summary.IdleServers++
		case healthClassDown:
			summary.DownServers++
		case healthClassDegraded:
			summary.DegradedServers++
		default:
			summary.HealthyServers++
		}

		entries = append(entries, entry)
	}

	// Detect server state transitions for desktop notifications.
	nowDown := make(map[string]bool)
	for _, e := range entries {
		isDown := e.Running && (e.Target == "unavailable" || (!e.Healthy && e.ConsecFails > 3))
		if isDown {
			nowDown[e.Name] = true
		}

		// Server went down: notify with debounce.
		if isDown && !m.prevDown[e.Name] {
			if last, ok := m.notifiedDown[e.Name]; !ok || time.Since(last) >= notifyDebounce {
				m.notifiedDown[e.Name] = time.Now()
				go func(name string) {
					if err := notify.NotifyServerDown(name); err != nil {
						m.Logger.Debug("server-down notification failed", "server", name, "error", err)
					}
				}(e.Name)
			}
		}

		// Server recovered: notify and clear suppression.
		if !isDown && m.prevDown[e.Name] {
			delete(m.notifiedDown, e.Name)
			go func(name string) {
				if err := notify.NotifyServerRecovered(name); err != nil {
					m.Logger.Debug("server-recovered notification failed", "server", name, "error", err)
				}
			}(e.Name)
		}
	}
	m.prevDown = nowDown

	m.SetSnapshot(entries)
	m.summary = summary
	m.Unlock()

	// Notify listeners (e.g., SSE hub) with the fresh entries (outside lock).
	out := make([]ServerHealthEntry, len(entries))
	copy(out, entries)
	m.FireOnRefresh(out)

	return nil
}

func classifyHealthEntry(entry ServerHealthEntry) healthClass {
	// Stopped local process with no usable health target.
	// If the server has registered tools it is reachable through the hub
	// and should be considered healthy rather than idle.
	if !entry.Running && entry.Target == "unavailable" {
		if entry.ToolCount > 0 {
			return healthClassHealthy
		}
		return healthClassIdle
	}

	// A running server can briefly lose its active health target during
	// refresh races between loom/health and loom/servers. Treat that as
	// degraded until we have sustained failures, otherwise mobile/desktop
	// surfaces can show false "server down" criticals.
	if entry.Target == "unavailable" {
		if entry.ConsecFails > 3 {
			return healthClassDown
		}
		return healthClassDegraded
	}

	// Sustained failures indicate down.
	if !entry.Healthy && entry.ConsecFails > 3 {
		return healthClassDown
	}

	// Any remaining unhealthy/failing condition is degraded.
	if !entry.Healthy || entry.ConsecFails > 0 {
		return healthClassDegraded
	}

	return healthClassHealthy
}
