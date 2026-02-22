package benchmarker

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// ConfigMapStore saves benchmark results to Kubernetes ConfigMaps (legacy/native behavior).
type ConfigMapStore struct {
	kubeClient kubernetes.Interface
}

func NewConfigMapStore(client kubernetes.Interface) *ConfigMapStore {
	return &ConfigMapStore{kubeClient: client}
}

func (c *ConfigMapStore) Save(ctx context.Context, r BenchmarkRecord) error {
	logger := log.FromContext(ctx)

	nowStr := r.Timestamp.UTC().Format(time.RFC3339)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.ConfigMapName,
			Namespace: r.Namespace,
		},
		Data: map[string]string{
			"tokensPerSecond":  strconv.FormatFloat(r.TokensPerSecond, 'f', -1, 64),
			"model":            r.ModelName,
			"backend":          r.Backend,
			"warmupIterations": strconv.Itoa(r.WarmupIterations),
			"iterations":       strconv.Itoa(r.Iterations),
			"batchSize":        strconv.Itoa(r.BatchSize),
			"minDuration":      r.MinDuration.String(),
			"completionTokens": strconv.Itoa(r.CompletionTokens),
			"durationSeconds":  strconv.FormatFloat(r.Duration.Seconds(), 'f', -1, 64),
			"samples":          strconv.Itoa(r.Samples),
			"timestamp":        nowStr,
		},
	}

	logger.Info("Upserting ConfigMap with benchmark results", "configMap", r.ConfigMapName)
	_, err := c.kubeClient.CoreV1().ConfigMaps(r.Namespace).Create(ctx, cm, metav1.CreateOptions{})
	if err != nil && apierrors.IsAlreadyExists(err) {
		existing, getErr := c.kubeClient.CoreV1().ConfigMaps(r.Namespace).Get(ctx, r.ConfigMapName, metav1.GetOptions{})
		if getErr != nil {
			return fmt.Errorf("failed to get existing benchmark result configmap: %w", getErr)
		}
		existing.Data = cm.Data
		_, err = c.kubeClient.CoreV1().ConfigMaps(r.Namespace).Update(ctx, existing, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("failed to upsert benchmark result configmap: %w", err)
	}

	if err := c.upsertGlobalResult(ctx, r); err != nil {
		logger.Error(err, "Failed to write global benchmark result")
	}

	return nil
}

func (c *ConfigMapStore) upsertGlobalResult(ctx context.Context, r BenchmarkRecord) error {
	if r.NodeName == "" || r.GlobalConfigMap == "" {
		return nil
	}

	node, err := c.kubeClient.CoreV1().Nodes().Get(ctx, r.NodeName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	deviceClass := deviceClassFromNode(node)
	key := benchmarkKey(r.Backend, r.ModelName, deviceClass)
	metaKey := "meta_" + key

	meta := map[string]interface{}{
		"backend":          r.Backend,
		"model":            r.ModelName,
		"deviceClass":      deviceClass,
		"tokensPerSecond":  r.TokensPerSecond,
		"completionTokens": r.CompletionTokens,
		"durationSeconds":  r.Duration.Seconds(),
		"samples":          r.Samples,
		"timestamp":        r.Timestamp.UTC().Format(time.RFC3339),
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("failed to marshal benchmark result metadata: %w", err)
	}

	for i := 0; i < 3; i++ {
		cm, err := c.kubeClient.CoreV1().ConfigMaps(r.Namespace).Get(ctx, r.GlobalConfigMap, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				cm = &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      r.GlobalConfigMap,
						Namespace: r.Namespace,
					},
					Data: map[string]string{},
				}
				_, createErr := c.kubeClient.CoreV1().ConfigMaps(r.Namespace).Create(ctx, cm, metav1.CreateOptions{})
				if createErr != nil && !apierrors.IsAlreadyExists(createErr) {
					return createErr
				}
				continue
			}
			return err
		}

		if cm.Data == nil {
			cm.Data = map[string]string{}
		}
		cm.Data[key] = strconv.FormatFloat(r.TokensPerSecond, 'f', -1, 64)
		cm.Data[metaKey] = string(metaJSON)

		_, err = c.kubeClient.CoreV1().ConfigMaps(r.Namespace).Update(ctx, cm, metav1.UpdateOptions{})
		if err != nil {
			if apierrors.IsConflict(err) {
				continue
			}
			return err
		}
		return nil
	}

	return fmt.Errorf("failed to update global benchmark configmap after retries")
}
