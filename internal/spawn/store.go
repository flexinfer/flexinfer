package spawn

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Store persists spawn state for recovery after restart.
type Store interface {
	Save(ctx context.Context, state *State) error
	Load(ctx context.Context, id string) (*State, error)
	LoadAll(ctx context.Context) ([]*State, error)
	Delete(ctx context.Context, id string) error
}

// ---------- FileStore ----------

// FileStore persists spawn state as JSON files on disk, providing backward
// compatibility with the original HUD persistence layer.
type FileStore struct {
	dir string
}

// NewFileStore creates a FileStore, ensuring the directory exists.
func NewFileStore(dir string) (*FileStore, error) {
	if dir == "" {
		return nil, fmt.Errorf("spawn store directory must not be empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create spawn store dir: %w", err)
	}
	return &FileStore{dir: dir}, nil
}

// Save persists a spawn state to disk as <spawn_id>.json.
func (s *FileStore) Save(_ context.Context, state *State) error {
	if state == nil {
		return fmt.Errorf("cannot save nil spawn state")
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal spawn state: %w", err)
	}
	path := s.path(state.SpawnID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write spawn state: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename spawn state: %w", err)
	}
	return nil
}

// Load reads a single spawn state by ID.
func (s *FileStore) Load(_ context.Context, id string) (*State, error) {
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read spawn state %s: %w", id, err)
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("unmarshal spawn state %s: %w", id, err)
	}
	return &st, nil
}

// LoadAll reads all persisted spawn states from disk.
func (s *FileStore) LoadAll(_ context.Context) ([]*State, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read spawn store dir: %w", err)
	}
	var states []*State
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		var st State
		if err := json.Unmarshal(data, &st); err != nil {
			continue
		}
		states = append(states, &st)
	}
	return states, nil
}

// Delete removes the persisted state file for a spawn.
func (s *FileStore) Delete(_ context.Context, id string) error {
	path := s.path(id)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete spawn state %s: %w", id, err)
	}
	return nil
}

// PruneCompleted removes state files for terminal spawns older than maxAge.
func (s *FileStore) PruneCompleted(ctx context.Context, maxAge time.Duration) error {
	states, err := s.LoadAll(ctx)
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-maxAge)
	for _, st := range states {
		if !IsTerminal(st.Status) {
			continue
		}
		if st.EndedAt != nil && st.EndedAt.Before(cutoff) {
			if err := s.Delete(ctx, st.SpawnID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *FileStore) path(id string) string {
	return filepath.Join(s.dir, id+".json")
}

// DefaultStoreDir returns the default file store directory.
func DefaultStoreDir() string {
	if cfgDir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(cfgDir, "loom", "spawns")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "loom", "spawns")
}

// ---------- K8sConfigMapStore ----------

// K8sConfigMapStore stores spawn state as data entries in a Kubernetes
// ConfigMap, providing cluster-native persistence that survives node-local
// filesystem loss.
type K8sConfigMapStore struct {
	client    kubernetes.Interface
	namespace string
	name      string // ConfigMap name
}

// NewK8sConfigMapStore creates a ConfigMap-backed store.
func NewK8sConfigMapStore(client kubernetes.Interface, namespace, name string) *K8sConfigMapStore {
	if name == "" {
		name = "loom-spawn-state"
	}
	return &K8sConfigMapStore{
		client:    client,
		namespace: namespace,
		name:      name,
	}
}

// Save persists a spawn state as a ConfigMap data entry keyed by spawn ID.
func (s *K8sConfigMapStore) Save(ctx context.Context, state *State) error {
	if state == nil {
		return fmt.Errorf("cannot save nil spawn state")
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal spawn state: %w", err)
	}

	cm, err := s.getOrCreateCM(ctx)
	if err != nil {
		return err
	}

	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}
	cm.Data[state.SpawnID] = string(data)

	_, err = s.client.CoreV1().ConfigMaps(s.namespace).Update(ctx, cm, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("update configmap %s: %w", s.name, err)
	}
	return nil
}

// Load reads a single spawn state by ID from the ConfigMap.
func (s *K8sConfigMapStore) Load(ctx context.Context, id string) (*State, error) {
	cm, err := s.client.CoreV1().ConfigMaps(s.namespace).Get(ctx, s.name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get configmap %s: %w", s.name, err)
	}
	raw, ok := cm.Data[id]
	if !ok {
		return nil, nil
	}
	var st State
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return nil, fmt.Errorf("unmarshal spawn state %s: %w", id, err)
	}
	return &st, nil
}

// LoadAll reads all spawn states from the ConfigMap.
func (s *K8sConfigMapStore) LoadAll(ctx context.Context) ([]*State, error) {
	cm, err := s.client.CoreV1().ConfigMaps(s.namespace).Get(ctx, s.name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get configmap %s: %w", s.name, err)
	}
	var states []*State
	for _, raw := range cm.Data {
		var st State
		if err := json.Unmarshal([]byte(raw), &st); err != nil {
			continue
		}
		states = append(states, &st)
	}
	return states, nil
}

// Delete removes a spawn state entry from the ConfigMap.
func (s *K8sConfigMapStore) Delete(ctx context.Context, id string) error {
	cm, err := s.client.CoreV1().ConfigMaps(s.namespace).Get(ctx, s.name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("get configmap %s: %w", s.name, err)
	}
	if cm.Data == nil {
		return nil
	}
	delete(cm.Data, id)
	_, err = s.client.CoreV1().ConfigMaps(s.namespace).Update(ctx, cm, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("update configmap %s: %w", s.name, err)
	}
	return nil
}

// getOrCreateCM fetches or creates the backing ConfigMap.
func (s *K8sConfigMapStore) getOrCreateCM(ctx context.Context) (*corev1.ConfigMap, error) {
	cm, err := s.client.CoreV1().ConfigMaps(s.namespace).Get(ctx, s.name, metav1.GetOptions{})
	if err == nil {
		return cm, nil
	}
	if !errors.IsNotFound(err) {
		return nil, fmt.Errorf("get configmap %s: %w", s.name, err)
	}
	cm = &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.name,
			Namespace: s.namespace,
			Labels: map[string]string{
				ManagedByLabel: ManagedByValue,
			},
		},
		Data: make(map[string]string),
	}
	created, err := s.client.CoreV1().ConfigMaps(s.namespace).Create(ctx, cm, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("create configmap %s: %w", s.name, err)
	}
	return created, nil
}
