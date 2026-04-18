package controllers

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// populateLoadingSubstage sets model.Status.LoadingSubstage + Message based on
// the most recently created pod for this model. Intended to be called right
// after Phase is set to Loading. Never returns an error: substage is a
// best-effort refinement and missing-pod cases simply leave the fields unset.
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
	if sub == "" && msg == "" {
		return
	}
	model.Status.LoadingSubstage = sub
	model.Status.Message = msg
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
// Priority (in order): ImagePulling > Initializing > LoadingWeights implied by
// running-but-not-ready > HealthCheckPending (when Ready is false on a running
// container). LoadingWeights and Compiling substages require backend-specific
// log scraping to populate authoritatively — not covered here; a follow-up
// slice can refine those.
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
