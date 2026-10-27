package controllers

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
)

// populateLoadingSubstage sets model.Status.LoadingSubstage + Message based on
// the most recently created pod for this model. Intended to be called right
// after Phase is set to Loading. Never returns an error: substage is a
// best-effort refinement and missing-pod cases simply leave the fields unset.
//
// Refinement ladder:
//  1. Pod/container state (ImagePulling / Initializing / running-but-not-ready).
//  2. If the pod is running-but-not-ready AND a KubeClient is available, tail
//     the container log and refine to LoadingWeights / Compiling /
//     HealthCheckPending when log signals are present.
func (r *ModelReconciler) populateLoadingSubstage(ctx context.Context, model *aiv1alpha2.Model) {
	if model == nil || r.Client == nil {
		return
	}

	pods := &corev1.PodList{}
	if err := r.List(ctx, pods,
		client.InNamespace(model.Namespace),
		client.MatchingLabels{LabelModel: model.Name},
	); err != nil {
		log.FromContext(ctx).V(1).Info("populateLoadingSubstage: list pods failed", "error", err.Error())
		return
	}
	if len(pods.Items) == 0 {
		return
	}

	// Prefer the most recently created pod; the deployment ReplicaSet churn
	// leaves older terminating pods around that would otherwise mislead us.
	sort.Slice(pods.Items, func(i, j int) bool {
		return pods.Items[i].CreationTimestamp.Time.After(pods.Items[j].CreationTimestamp.Time)
	})
	pod := &pods.Items[0]

	sub, msg := deriveLoadingSubstage(pod)

	// If the pod is running-but-not-ready, try to refine via log scraping.
	// This is the case where Initializing without log context is least
	// informative and where the LoadingWeights/Compiling/HealthCheckPending
	// distinctions actually matter.
	if sub == aiv1alpha2.LoadingSubstageInitializing && isRunningNotReady(pod) && r.KubeClient != nil {
		if logSub, logMsg := r.refineSubstageFromLog(ctx, pod); logSub != "" {
			sub = logSub
			msg = logMsg
		}
	}

	if sub == "" && msg == "" {
		return
	}
	// LoadingProgressAt tracks the last time the observable load state
	// changed. Only bump it when substage or message actually differ from
	// what status already holds, so a stalled load (same message every
	// reconcile) leaves the timestamp frozen and the proxy can detect it.
	if model.Status.LoadingSubstage != sub || model.Status.Message != msg {
		now := metav1.Now()
		model.Status.LoadingProgressAt = &now
	}
	model.Status.LoadingSubstage = sub
	model.Status.Message = msg
}

// isRunningNotReady returns true iff the pod has at least one primary container
// in Running state and no primary container reports Ready=true.
func isRunningNotReady(pod *corev1.Pod) bool {
	if pod == nil || len(pod.Status.ContainerStatuses) == 0 {
		return false
	}
	anyRunning := false
	anyReady := false
	for i := range pod.Status.ContainerStatuses {
		cs := &pod.Status.ContainerStatuses[i]
		if cs.State.Running != nil {
			anyRunning = true
		}
		if cs.Ready {
			anyReady = true
		}
	}
	return anyRunning && !anyReady
}

// refineSubstageFromLog pulls a bounded tail of the primary container's log
// and classifies the most-advanced-observed stage. Returns empty strings when
// no recognizable signal is present — caller keeps the pod-state-based default.
func (r *ModelReconciler) refineSubstageFromLog(ctx context.Context, pod *corev1.Pod) (aiv1alpha2.LoadingSubstage, string) {
	container := primaryContainerName(pod)
	tail := fetchPodLogTail(ctx, r.KubeClient, pod.Namespace, pod.Name, container, 200, 65536)
	if tail == "" {
		return "", ""
	}
	return parseVLLMLoadProgress(tail)
}

// primaryContainerName picks the first container that looks like the model
// runtime. Falls back to the first container name, or "model" when empty.
func primaryContainerName(pod *corev1.Pod) string {
	if pod == nil {
		return "model"
	}
	for _, c := range pod.Spec.Containers {
		if c.Name == "model" || c.Name == "runtime" || c.Name == "vllm" {
			return c.Name
		}
	}
	if len(pod.Spec.Containers) > 0 {
		return pod.Spec.Containers[0].Name
	}
	return "model"
}

// fetchPodLogTail retrieves the last tailLines of a container's log, bounded
// to maxBytes, and returns the content as a single string. Returns "" on any
// error. This is the same contract as readPodLogTail elsewhere in the
// controllers package but reads the full tail (up to maxBytes) instead of
// stopping at the first 8 KiB read.
func fetchPodLogTail(ctx context.Context, kubeClient kubernetes.Interface, namespace, podName, container string, tailLines, maxBytes int64) string {
	if kubeClient == nil {
		return ""
	}
	opts := &corev1.PodLogOptions{
		Container: container,
		TailLines: &tailLines,
	}
	req := kubeClient.CoreV1().Pods(namespace).GetLogs(podName, opts)
	stream, err := req.Stream(ctx)
	if err != nil {
		return ""
	}
	defer func() { _ = stream.Close() }()
	limited := io.LimitReader(stream, maxBytes)
	data, err := io.ReadAll(limited)
	if err != nil {
		return strings.TrimSpace(string(data))
	}
	return strings.TrimSpace(string(data))
}

// kvPoolBootLogTimeout bounds the boot-log read so a hung kubelet log tunnel
// cannot stall the status reconcile.
const kvPoolBootLogTimeout = 15 * time.Second

// readKVPoolBootLog is a seam for tests; the fake clientset cannot fail GetLogs.
var readKVPoolBootLog = fetchKVPoolBootLog

// fetchKVPoolBootLog reads a bounded prefix including the boot profile rather
// than a short tail that could have already lost the profile under traffic.
// Errors remain retryable; a successful log without profile lines is observed once.
func fetchKVPoolBootLog(ctx context.Context, kubeClient kubernetes.Interface, pod *corev1.Pod) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, kvPoolBootLogTimeout)
	defer cancel()
	stream, err := kubeClient.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: primaryContainerName(pod)}).Stream(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = stream.Close() }()
	data, err := io.ReadAll(io.LimitReader(stream, 4<<20))
	return string(data), err
}

// parseVLLMLoadProgress scans the log tail for stage-indicating lines and
// returns the most-advanced stage observed. Stage precedence (later is more
// advanced):
//
//	LoadingWeights → Compiling → HealthCheckPending
//
// We classify by the last matching line for each stage so the most recent
// observation wins. Returns empty strings when no recognized signal is found.
func parseVLLMLoadProgress(logTail string) (aiv1alpha2.LoadingSubstage, string) {
	if logTail == "" {
		return "", ""
	}

	var (
		lastShard     string
		lastCompiling string
		lastHealth    string
	)

	scanner := splitLines(logTail)
	for _, line := range scanner {
		lower := strings.ToLower(line)
		switch {
		case strings.Contains(lower, "uvicorn running on "):
			lastHealth = line
		case strings.Contains(lower, "application startup complete"):
			lastHealth = line
		case strings.Contains(lower, "capturing cuda graph"), strings.Contains(lower, "capturing hip graph"):
			lastCompiling = line
		case strings.Contains(lower, "torch.compile") && strings.Contains(lower, "compil"):
			lastCompiling = line
		case strings.Contains(lower, "loading safetensors checkpoint shards"):
			lastShard = line
		}
	}

	// HealthCheckPending wins when the HTTP server is up.
	if lastHealth != "" {
		return aiv1alpha2.LoadingSubstageHealthCheckPending, "backend HTTP server up, awaiting readiness probe"
	}
	if lastCompiling != "" {
		return aiv1alpha2.LoadingSubstageCompiling, "compiling kernels / capturing graphs"
	}
	if lastShard != "" {
		if cur, total, rate, ok := parseShardProgress(lastShard); ok {
			msg := fmt.Sprintf("loading weights (%d/%d shards, %s)", cur, total, rate)
			return aiv1alpha2.LoadingSubstageLoadingWeights, msg
		}
		// Match fell through the regex (unusual format) — emit a generic
		// LoadingWeights message rather than dropping back to Initializing.
		return aiv1alpha2.LoadingSubstageLoadingWeights, "loading weights"
	}
	return "", ""
}

// parseShardProgress extracts (current, total, rate) from a shard progress
// line; see backend.ParseVLLMShardProgress, shared with the runtime.
func parseShardProgress(line string) (int, int, string, bool) {
	return backend.ParseVLLMShardProgress(line)
}

// splitLines returns a slice of lines from input. Trailing empty line is
// omitted. Matches the set of behaviors we care about without pulling in
// bufio.Scanner for a tiny bounded input.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	// Drop a trailing empty element from a final "\n".
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// deriveLoadingSubstage maps a pod's observed container state to a
// LoadingSubstage + one-line message suitable for Model.status. The result is
// intended to refine Phase=Loading only; callers clear both fields when Phase
// transitions away from Loading.
//
// Returned substage/message are "" when pod is nil or when no container state
// maps cleanly — the caller should fall back to a plain "backend is starting"
// or similar generic message and leave LoadingSubstage empty.
//
// Priority (in order): ImagePulling > Initializing > running-but-not-ready.
// LoadingWeights / Compiling / HealthCheckPending refinements happen in the
// log-scraping pass (populateLoadingSubstage), not here — this helper stays
// pure and unit-testable against pod state alone.
func deriveLoadingSubstage(pod *corev1.Pod) (aiv1alpha2.LoadingSubstage, string) {
	if pod == nil {
		return "", ""
	}

	// Honor explicit Failed/Succeeded phases: caller uses higher-level signal.
	switch pod.Status.Phase {
	case corev1.PodSucceeded, corev1.PodFailed:
		return "", ""
	}

	// Inspect init containers first — if any are not ready, report their state.
	for i := range pod.Status.InitContainerStatuses {
		if sub, msg := substageFromContainerStatus(&pod.Status.InitContainerStatuses[i], true); sub != "" {
			return sub, msg
		}
	}

	// Then the primary containers.
	for i := range pod.Status.ContainerStatuses {
		if sub, msg := substageFromContainerStatus(&pod.Status.ContainerStatuses[i], false); sub != "" {
			return sub, msg
		}
	}

	// All containers present and Ready=true — caller would have chosen a
	// different phase. Default to empty so caller falls back to a plain message.
	return "", ""
}

// substageFromContainerStatus inspects one container's state and returns a
// substage if it can confidently classify the container's stage. Returns empty
// strings when the container is Ready or the state is not informative.
func substageFromContainerStatus(cs *corev1.ContainerStatus, isInit bool) (aiv1alpha2.LoadingSubstage, string) {
	if cs == nil {
		return "", ""
	}

	name := cs.Name
	if name == "" {
		name = "container"
	}

	switch {
	case cs.State.Waiting != nil:
		return substageFromWaitingReason(cs.State.Waiting, name, isInit)
	case cs.State.Running != nil:
		if cs.Ready {
			return "", ""
		}
		// Running but not yet Ready. Without a backend-specific probe we can
		// only say the container is up and waiting on the readiness probe.
		// LoadingWeights and Compiling require log scraping; until that lands,
		// surface the generic "starting" substage so the Model.status field
		// remains populated.
		return aiv1alpha2.LoadingSubstageInitializing, fmt.Sprintf("%s running, readiness probe not passing yet", name)
	case cs.State.Terminated != nil:
		if cs.State.Terminated.Reason != "" {
			return aiv1alpha2.LoadingSubstageInitializing, fmt.Sprintf("%s terminated: %s", name, cs.State.Terminated.Reason)
		}
		return aiv1alpha2.LoadingSubstageInitializing, fmt.Sprintf("%s terminated", name)
	}
	return "", ""
}

func substageFromWaitingReason(waiting *corev1.ContainerStateWaiting, name string, isInit bool) (aiv1alpha2.LoadingSubstage, string) {
	reason := waiting.Reason
	lower := strings.ToLower(reason)
	msg := waiting.Message
	if msg == "" {
		msg = reason
	}

	switch {
	case strings.Contains(lower, "imagepull"), lower == "errimagepull", lower == "imagepullbackoff":
		prefix := "pulling image"
		if isInit {
			prefix = "pulling init image"
		}
		if msg != "" {
			return aiv1alpha2.LoadingSubstageImagePulling, fmt.Sprintf("%s for %s: %s", prefix, name, truncateMessage(msg))
		}
		return aiv1alpha2.LoadingSubstageImagePulling, fmt.Sprintf("%s for %s", prefix, name)
	case lower == "containercreating", lower == "podinitializing":
		return aiv1alpha2.LoadingSubstageInitializing, fmt.Sprintf("%s %s", name, reason)
	case lower == "crashloopbackoff", strings.Contains(lower, "createcontainer"):
		// Not strictly a loading substage — surface as Initializing with the
		// message so operators see the failure hint instead of a silent
		// "Loading" state.
		return aiv1alpha2.LoadingSubstageInitializing, fmt.Sprintf("%s: %s", name, reason)
	}
	// Unknown waiting reason — return Initializing so the substage is at least
	// populated, but surface the original reason in the message.
	if reason != "" {
		return aiv1alpha2.LoadingSubstageInitializing, fmt.Sprintf("%s waiting: %s", name, reason)
	}
	return "", ""
}

// truncateMessage keeps messages short enough for the status field while still
// showing the useful prefix of any container-state message (which can include
// multi-line pull errors).
func truncateMessage(msg string) string {
	msg = strings.TrimSpace(msg)
	if idx := strings.IndexByte(msg, '\n'); idx >= 0 {
		msg = msg[:idx]
	}
	const maxLen = 160
	if len(msg) > maxLen {
		msg = msg[:maxLen-1] + "…"
	}
	return msg
}

// parseVLLMKVPool requires both profile lines; partial profiles are not usable.
// The parser is shared with the runtime (backend.ParseVLLMKVPool).
func parseVLLMKVPool(tail string) (int64, string, bool) {
	return backend.ParseVLLMKVPool(tail)
}

func updateKVPoolCondition(model *aiv1alpha2.Model) bool {
	maxSeqs := model.Spec.ConfigInt("maxNumSeqs", 0)
	if maxSeqs <= 0 || model.Status.KVPool == nil || model.Status.KVPool.MaxConcurrency == "" {
		meta.RemoveStatusCondition(&model.Status.Conditions, aiv1alpha2.ConditionKVPoolShort)
		return false
	}
	c, err := strconv.ParseFloat(model.Status.KVPool.MaxConcurrency, 64)
	if err != nil {
		return false
	}
	short := c < 0.75*float64(maxSeqs)
	reason := aiv1alpha2.ReasonKVPoolOK
	if short {
		reason = aiv1alpha2.ReasonKVPoolShort
	}
	setModelCondition(model, aiv1alpha2.ConditionKVPoolShort, short, reason,
		fmt.Sprintf("Profiled maxConcurrency=%sx; maxNumSeqs=%d (short below %.2fx)", model.Status.KVPool.MaxConcurrency, maxSeqs, 0.75*float64(maxSeqs)))
	return short
}
