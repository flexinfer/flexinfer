package autotune

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestExperimentLogger_FormatTSV(t *testing.T) {
	t.Parallel()
	logger := NewExperimentLogger(fake.NewSimpleClientset(), "test-ns", "test-model")
	fixedTime := time.Date(2026, 3, 10, 10, 0, 0, 0, time.UTC)
	logger.now = func() time.Time { return fixedTime }

	logger.Add(ExperimentEntry{
		Step:   0,
		Action: ActionBaseline,
		TPS:    73.2,
	})
	logger.Add(ExperimentEntry{
		Step:        1,
		Action:      ActionAccepted,
		TPS:         78.1,
		Improvement: 6.69,
		ConfigDelta: "maxNumSeqs=16",
	})
	logger.Add(ExperimentEntry{
		Step:        2,
		Action:      ActionRejected,
		TPS:         71.0,
		Improvement: -3.01,
		ConfigDelta: "maxNumSeqs=32",
	})
	logger.Add(ExperimentEntry{
		Step:        3,
		Action:      ActionRolloutFailed,
		ConfigDelta: "maxNumSeqs=1",
		Error:       "timeout",
	})

	tsv := logger.FormatTSV()

	lines := strings.Split(strings.TrimSpace(tsv), "\n")
	require.Len(t, lines, 5) // header + 4 entries

	// Check header.
	assert.Equal(t, "step\ttimestamp\taction\ttps\timprovement\tconfig_delta\terror", lines[0])

	// Baseline row: TPS present, improvement is "-".
	assert.Contains(t, lines[1], "baseline")
	assert.Contains(t, lines[1], "73.2")
	assert.Contains(t, lines[1], "-\t-") // improvement and delta are both "-"

	// Accepted row: TPS and improvement present.
	assert.Contains(t, lines[2], "accepted")
	assert.Contains(t, lines[2], "78.1")
	assert.Contains(t, lines[2], "+6.69%")
	assert.Contains(t, lines[2], "maxNumSeqs=16")

	// Rejected row: negative improvement.
	assert.Contains(t, lines[3], "rejected")
	assert.Contains(t, lines[3], "-3.01%")

	// Rollout failed: TPS is "-", improvement is "-".
	assert.Contains(t, lines[4], "rollout_failed")
	assert.Contains(t, lines[4], "timeout")
}

func TestExperimentLogger_SaveToConfigMap(t *testing.T) {
	t.Parallel()
	clientset := fake.NewSimpleClientset()
	logger := NewExperimentLogger(clientset, "test-ns", "my-model")
	fixedTime := time.Date(2026, 3, 10, 10, 0, 0, 0, time.UTC)
	logger.now = func() time.Time { return fixedTime }

	logger.Add(ExperimentEntry{
		Step:   0,
		Action: ActionBaseline,
		TPS:    73.2,
	})

	summary := ExperimentSummary{
		ModelName:     "my-model",
		StartTime:     fixedTime,
		EndTime:       fixedTime.Add(10 * time.Minute),
		BaselineTPS:   73.2,
		BestTPS:       80.0,
		Improvement:   9.29,
		TotalSteps:    5,
		AcceptedSteps: 2,
		BestConfig:    map[string]any{"maxNumSeqs": float64(16)},
	}

	err := logger.Save(context.Background(), summary)
	require.NoError(t, err)

	// Verify ConfigMap was created.
	cm, err := clientset.CoreV1().ConfigMaps("test-ns").Get(context.Background(), "my-model-autotune-log", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Contains(t, cm.Data, "results.tsv")
	assert.Contains(t, cm.Data, "best_config.json")
	assert.Contains(t, cm.Data, "summary.json")
	assert.Contains(t, cm.Data["best_config.json"], "maxNumSeqs")
	assert.Equal(t, "flexinfer-autotune", cm.Labels["app.kubernetes.io/managed-by"])

	// Save again to test update path.
	err = logger.Save(context.Background(), summary)
	require.NoError(t, err)
}

func TestExperimentLogger_ConfigMapName(t *testing.T) {
	t.Parallel()
	logger := NewExperimentLogger(fake.NewSimpleClientset(), "ns", "my-model")
	assert.Equal(t, "my-model-autotune-log", logger.ConfigMapName())
}
