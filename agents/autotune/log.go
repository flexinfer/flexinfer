package autotune

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ExperimentAction describes the outcome of an experiment step.
type ExperimentAction string

const (
	ActionBaseline      ExperimentAction = "baseline"
	ActionAccepted      ExperimentAction = "accepted"
	ActionRejected      ExperimentAction = "rejected"
	ActionRolloutFailed ExperimentAction = "rollout_failed"
	ActionSkipped       ExperimentAction = "skipped"
)

// ExperimentEntry records one step of the autotune experiment.
type ExperimentEntry struct {
	Step        int
	Timestamp   time.Time
	Action      ExperimentAction
	TPS         float64 // tokens per second; 0 if unavailable
	Improvement float64 // percentage improvement vs baseline; 0 for baseline
	ConfigDelta string  // human-readable config change, e.g. "maxNumSeqs=16"
	Error       string  // error message if action is rollout_failed
}

// ExperimentSummary captures the final outcome of an autotune run.
type ExperimentSummary struct {
	ModelName     string                 `json:"modelName"`
	StartTime     time.Time              `json:"startTime"`
	EndTime       time.Time              `json:"endTime"`
	BaselineTPS   float64                `json:"baselineTps"`
	BestTPS       float64                `json:"bestTps"`
	Improvement   float64                `json:"improvementPct"`
	TotalSteps    int                    `json:"totalSteps"`
	AcceptedSteps int                    `json:"acceptedSteps"`
	BestConfig    map[string]interface{} `json:"bestConfig"`
}

// ExperimentLogger records experiment entries and persists them to a ConfigMap.
type ExperimentLogger struct {
	kubeClient kubernetes.Interface
	namespace  string
	modelName  string
	entries    []ExperimentEntry
	now        func() time.Time
}

// NewExperimentLogger creates a new experiment logger.
func NewExperimentLogger(kubeClient kubernetes.Interface, namespace, modelName string) *ExperimentLogger {
	return &ExperimentLogger{
		kubeClient: kubeClient,
		namespace:  namespace,
		modelName:  modelName,
		now:        time.Now,
	}
}

// Add appends an experiment entry.
func (l *ExperimentLogger) Add(entry ExperimentEntry) {
	entry.Timestamp = l.now()
	l.entries = append(l.entries, entry)
}

// Entries returns all recorded entries.
func (l *ExperimentLogger) Entries() []ExperimentEntry {
	return l.entries
}

// FormatTSV formats the experiment log as tab-separated values.
func (l *ExperimentLogger) FormatTSV() string {
	var sb strings.Builder
	sb.WriteString("step\ttimestamp\taction\ttps\timprovement\tconfig_delta\terror\n")
	for _, e := range l.entries {
		tpsStr := "-"
		if e.TPS > 0 {
			tpsStr = fmt.Sprintf("%.1f", e.TPS)
		}
		impStr := "-"
		if e.Action != ActionBaseline && e.Action != ActionRolloutFailed {
			impStr = fmt.Sprintf("%+.2f%%", e.Improvement)
		}
		delta := e.ConfigDelta
		if delta == "" {
			delta = "-"
		}
		errStr := e.Error
		if errStr == "" {
			errStr = ""
		}
		sb.WriteString(fmt.Sprintf("%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
			e.Step,
			e.Timestamp.Format(time.RFC3339),
			e.Action,
			tpsStr,
			impStr,
			delta,
			errStr,
		))
	}
	return sb.String()
}

// Save persists the experiment log, best config, and summary to a ConfigMap.
func (l *ExperimentLogger) Save(ctx context.Context, summary ExperimentSummary) error {
	cmName := l.modelName + "-autotune-log"

	bestConfigJSON, err := json.MarshalIndent(summary.BestConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal best config: %w", err)
	}
	summaryJSON, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal summary: %w", err)
	}

	data := map[string]string{
		"results.tsv":      l.FormatTSV(),
		"best_config.json": string(bestConfigJSON),
		"summary.json":     string(summaryJSON),
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: l.namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "flexinfer-autotune",
				"flexinfer.ai/model":           l.modelName,
			},
		},
		Data: data,
	}

	existing, err := l.kubeClient.CoreV1().ConfigMaps(l.namespace).Get(ctx, cmName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = l.kubeClient.CoreV1().ConfigMaps(l.namespace).Create(ctx, cm, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return fmt.Errorf("get configmap: %w", err)
	}

	existing.Data = data
	existing.Labels = cm.Labels
	_, err = l.kubeClient.CoreV1().ConfigMaps(l.namespace).Update(ctx, existing, metav1.UpdateOptions{})
	return err
}

// ConfigMapName returns the name of the ConfigMap used for storing results.
func (l *ExperimentLogger) ConfigMapName() string {
	return l.modelName + "-autotune-log"
}
