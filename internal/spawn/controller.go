package spawn

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Controller defines the interface for managing spawn lifecycles.
type Controller interface {
	Spawn(ctx context.Context, req Request) (string, error)
	Stop(ctx context.Context, spawnID string) error
	Get(spawnID string) (*State, bool)
	List() []*State
	Reconcile(ctx context.Context) error
}

// K8sController implements Controller using Kubernetes pods as the source of
// truth. On each Reconcile cycle it lists pods by the managed-by label and
// updates the in-memory state map accordingly, eliminating the "stale after
// restart" bug where local JSON files diverged from actual pod status.
type K8sController struct {
	client    kubernetes.Interface
	namespace string
	store     Store
	logger    *slog.Logger

	mu     sync.RWMutex
	spawns map[string]*State
}

// NewK8sController creates a new K8s-native spawn controller.
func NewK8sController(client kubernetes.Interface, namespace string, store Store, logger *slog.Logger) *K8sController {
	if logger == nil {
		logger = slog.Default()
	}
	return &K8sController{
		client:    client,
		namespace: namespace,
		store:     store,
		logger:    logger.With("component", "spawn-controller"),
		spawns:    make(map[string]*State),
	}
}

// SetK8sClient injects a Kubernetes client and namespace after construction.
// This is used when the controller is created before the K8s backend is fully
// initialised (e.g., in the HUD startup sequence where the backend clientset
// is only available after NewK8sBackend succeeds).
func (c *K8sController) SetK8sClient(client kubernetes.Interface, namespace string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.client = client
	c.namespace = namespace
}

// Spawn records a new spawn in the in-memory map and persistent store. The
// actual pod creation is left to the caller (the HUD orchestrator or an
// external workflow) because it requires the devbox backend, Dockerfile
// generation, config injection, and session registration -- concerns that
// belong to the orchestration layer, not the controller.
//
// Returns the generated spawn ID.
func (c *K8sController) Spawn(ctx context.Context, req Request) (string, error) {
	if req.AgentType == "" {
		req.AgentType = "claude-code"
	}
	switch req.AgentType {
	case "claude-code", "codex", "gemini":
		// ok
	default:
		return "", fmt.Errorf("unsupported agent type: %s", req.AgentType)
	}
	if req.TaskDescription == "" {
		return "", fmt.Errorf("task_description is required")
	}
	if req.Project == "" {
		return "", fmt.Errorf("project is required")
	}

	spawnID := NewSpawnID()
	agentID := fmt.Sprintf("spawn-%s-%s", req.AgentType, spawnID[6:])

	state := &State{
		SpawnID:   spawnID,
		AgentID:   agentID,
		Status:    StatusPending,
		Request:   req,
		StartedAt: time.Now(),
	}

	c.mu.Lock()
	c.spawns[spawnID] = state
	c.mu.Unlock()

	if c.store != nil {
		if err := c.store.Save(ctx, state); err != nil {
			c.logger.Warn("failed to persist spawn state",
				"spawn_id", spawnID, "error", err)
		}
	}

	return spawnID, nil
}

// UpdateState updates the in-memory state and persists it. This is called by
// the orchestration layer as the spawn progresses through lifecycle stages.
func (c *K8sController) UpdateState(ctx context.Context, state *State) {
	c.mu.Lock()
	c.spawns[state.SpawnID] = state
	c.mu.Unlock()

	if c.store != nil {
		if err := c.store.Save(ctx, state); err != nil {
			c.logger.Warn("failed to persist spawn state",
				"spawn_id", state.SpawnID, "error", err)
		}
	}
}

// Stop marks a spawn as stopped and deletes the associated pod.
func (c *K8sController) Stop(ctx context.Context, spawnID string) error {
	c.mu.Lock()
	state, ok := c.spawns[spawnID]
	if !ok {
		c.mu.Unlock()
		return fmt.Errorf("spawn %s not found", spawnID)
	}
	c.mu.Unlock()

	// Delete the pod if one exists and a K8s client is available.
	if state.PodName != "" && c.client != nil {
		err := c.client.CoreV1().Pods(c.namespace).Delete(ctx, state.PodName, metav1.DeleteOptions{})
		if err != nil {
			c.logger.Warn("failed to delete spawn pod",
				"spawn_id", spawnID, "pod", state.PodName, "error", err)
		}
	}

	c.mu.Lock()
	state.Status = StatusStopped
	now := time.Now()
	state.EndedAt = &now
	c.mu.Unlock()

	if c.store != nil {
		if err := c.store.Save(ctx, state); err != nil {
			c.logger.Warn("failed to persist spawn state",
				"spawn_id", spawnID, "error", err)
		}
	}

	return nil
}

// Get returns a specific spawn state.
func (c *K8sController) Get(spawnID string) (*State, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.spawns[spawnID]
	return s, ok
}

// List returns all spawn states.
func (c *K8sController) List() []*State {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]*State, 0, len(c.spawns))
	for _, s := range c.spawns {
		result = append(result, s)
	}
	return result
}

// Reconcile synchronises the in-memory state map with actual Kubernetes pod
// status. This is the key fix for the stale-after-restart bug: instead of
// trusting local JSON, we query the cluster for pods labeled
// app.kubernetes.io/managed-by=loom-spawn and derive state from their phase.
func (c *K8sController) Reconcile(ctx context.Context) error {
	if c.client == nil {
		// No K8s client configured — skip reconciliation silently.
		return nil
	}

	selector := fmt.Sprintf("%s=%s", ManagedByLabel, ManagedByValue)
	pods, err := c.client.CoreV1().Pods(c.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return fmt.Errorf("list spawn pods: %w", err)
	}

	// Build a set of spawn IDs from live pods.
	livePods := make(map[string]*corev1.Pod, len(pods.Items))
	for i := range pods.Items {
		pod := &pods.Items[i]
		spawnID := pod.Labels[SpawnIDLabel]
		if spawnID == "" {
			continue
		}
		livePods[spawnID] = pod
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Update existing entries from pod status.
	for spawnID, state := range c.spawns {
		pod, exists := livePods[spawnID]
		if !exists {
			// Pod gone -- if the spawn was non-terminal, mark it as failed.
			if !IsTerminal(state.Status) {
				state.Status = StatusFailed
				state.Error = "pod not found during reconciliation"
				now := time.Now()
				state.EndedAt = &now
				c.persistLocked(ctx, state)
			}
			continue
		}
		// Update state from pod phase.
		newStatus := podPhaseToStatus(pod.Status.Phase)
		if state.Status != newStatus && !IsTerminal(state.Status) {
			state.Status = newStatus
			state.PodName = pod.Name
			if IsTerminal(newStatus) {
				now := time.Now()
				state.EndedAt = &now
			}
			c.persistLocked(ctx, state)
		}
	}

	// Discover pods that are not tracked (e.g., after a full restart with no
	// persisted state). Create new entries from pod labels.
	for spawnID, pod := range livePods {
		if _, tracked := c.spawns[spawnID]; tracked {
			continue
		}
		state := &State{
			SpawnID:   spawnID,
			AgentID:   pod.Labels[AgentIDLabel],
			PodName:   pod.Name,
			Status:    podPhaseToStatus(pod.Status.Phase),
			StartedAt: pod.CreationTimestamp.Time,
			Request: Request{
				AgentType: pod.Labels["loom.dev/agent-type"],
				Project:   pod.Labels["loom.dev/project"],
			},
		}
		c.spawns[spawnID] = state
		c.persistLocked(ctx, state)
		c.logger.Info("discovered untracked spawn pod",
			"spawn_id", spawnID, "pod", pod.Name, "status", state.Status)
	}

	return nil
}

// RecoverFromStore loads persisted state from the store and populates the
// in-memory map. Non-terminal entries are left as-is for the next Reconcile
// call to resolve against actual pod status.
func (c *K8sController) RecoverFromStore(ctx context.Context) error {
	if c.store == nil {
		return nil
	}
	states, err := c.store.LoadAll(ctx)
	if err != nil {
		return fmt.Errorf("load persisted spawns: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	for _, st := range states {
		c.spawns[st.SpawnID] = st
	}

	c.logger.Info("recovered spawn state from store", "count", len(states))
	return nil
}

// ActiveCount returns the number of spawns in a non-terminal state.
func (c *K8sController) ActiveCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	count := 0
	for _, s := range c.spawns {
		if !IsTerminal(s.Status) {
			count++
		}
	}
	return count
}

// persistLocked saves to the store. Caller must hold c.mu.
func (c *K8sController) persistLocked(ctx context.Context, state *State) {
	if c.store == nil {
		return
	}
	if err := c.store.Save(ctx, state); err != nil {
		c.logger.Warn("failed to persist spawn state",
			"spawn_id", state.SpawnID, "error", err)
	}
}

// podPhaseToStatus maps a Kubernetes pod phase to a spawn Status.
func podPhaseToStatus(phase corev1.PodPhase) Status {
	switch phase {
	case corev1.PodPending:
		return StatusPending
	case corev1.PodRunning:
		return StatusRunning
	case corev1.PodSucceeded:
		return StatusCompleted
	case corev1.PodFailed:
		return StatusFailed
	default:
		return StatusUnknown
	}
}

// NewSpawnID generates a unique spawn ID using crypto/rand.
func NewSpawnID() string {
	var buf [6]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("spawn-%d", time.Now().UnixNano())
	}
	return "spawn-" + hex.EncodeToString(buf[:])
}
