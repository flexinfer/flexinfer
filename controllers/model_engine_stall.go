package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
	"github.com/flexinfer/flexinfer/pkg/metrics"
)

const (
	ConditionEngineStalled = "EngineStalled"
	ReasonEngineStalled    = "EngineStalled"
	ReasonGPUUnresponsive  = "GPUUnresponsive"
	// EngineStallRecoveryAnnotation lives on the parent, so deletion cannot erase
	// the restart budget. Remove it to explicitly authorize another recovery.
	EngineStallRecoveryAnnotation = "flexinfer.ai/engine-stall-recovery"
)

type engineStallRecovery struct {
	PodUID         types.UID `json:"podUID"`
	At             time.Time `json:"at"`
	Node           string    `json:"node"`
	Blocked        bool      `json:"blocked"`
	Message        string    `json:"message"`
	Ready          bool      `json:"ready"`
	GPUUnavailable bool      `json:"gpuUnavailable"`
}
type engineStallObservation struct {
	mu         sync.Mutex
	detector   backend.StallDetector
	lastScrape time.Time
	sample     backend.VLLMStallSample
	stalled    bool
}

// observeEngineStall runs even for unready pods: the exec readiness probe may
// discover the frozen counters before the controller does.
func (r *ModelReconciler) observeEngineStall(ctx context.Context, model *aiv1alpha2.Model, now time.Time, read func(context.Context, *corev1.Pod) (backend.VLLMStallSample, error)) (bool, error) {
	if model.Spec.Backend != backend.NameVLLM {
		return false, nil
	}
	origStatus := model.Status.DeepCopy()
	key := types.NamespacedName{Name: model.Name, Namespace: model.Namespace}
	marker := engineStallRecovery{}
	raw := model.Annotations[EngineStallRecoveryAnnotation]
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &marker); err != nil {
			return true, fmt.Errorf("invalid engine stall recovery annotation: %w", err)
		}
	}
	if raw == "" {
		if cond := modelCondition(model.Status.Conditions, ConditionEngineStalled); cond != nil && cond.Reason == ReasonGPUUnresponsive {
			r.engineStallObservations.Range(func(k, v any) bool {
				if strings.HasPrefix(k.(string), model.Namespace+"/"+model.Name+"/") {
					r.engineStallObservations.Delete(k)
				}
				return true
			})
			setModelCondition(model, ConditionEngineStalled, false, "RecoveryRequested", "Operator cleared engine stall recovery marker")
		}
	}
	fail := func(message string) (bool, error) {
		marker.Message = message
		marker.Blocked = true
		if err := r.saveEngineStallRecovery(ctx, model, marker); err != nil {
			return true, err
		}
		// Scale the parent Deployment to zero, preventing its ReplicaSet from
		// repeatedly recreating failed GPU processes while the recovery latch holds.
		dep := &appsv1.Deployment{}
		if err := r.Get(ctx, key, dep); err == nil {
			if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 0 {
				base := dep.DeepCopy()
				zero := int32(0)
				dep.Spec.Replicas = &zero
				if err := r.Patch(ctx, dep, client.MergeFrom(base)); err != nil {
					return true, err
				}
			}
		} else if client.IgnoreNotFound(err) != nil {
			return true, err
		}
		model.Status.Phase = aiv1alpha2.ModelPhaseFailed
		model.Status.Message = message
		setModelCondition(model, ConditionEngineStalled, true, ReasonGPUUnresponsive, message)
		setModelCondition(model, aiv1alpha2.ConditionModelReady, false, ReasonGPUUnresponsive, message)
		metrics.ModelEngineStalled.WithLabelValues(model.Name, model.Namespace).Set(1)
		return true, r.statusUpdateIfChanged(ctx, model, origStatus)
	}
	if marker.Blocked {
		node := &corev1.Node{}
		if err := r.Get(ctx, types.NamespacedName{Name: marker.Node}, node); err == nil {
			available := false
			for _, resource := range []corev1.ResourceName{"amd.com/gpu", "nvidia.com/gpu"} {
				quantity := node.Status.Allocatable[resource]
				available = available || quantity.Sign() > 0
			}
			if !available && !marker.GPUUnavailable {
				marker.GPUUnavailable = true
				if err := r.saveEngineStallRecovery(ctx, model, marker); err != nil {
					return true, err
				}
			} else if available && marker.GPUUnavailable {
				base := model.DeepCopy()
				delete(model.Annotations, EngineStallRecoveryAnnotation)
				if err := r.Patch(ctx, model, client.MergeFrom(base)); err != nil {
					return true, err
				}
				setModelCondition(model, ConditionEngineStalled, false, "GPURecovered", "Node GPU allocatable recovered")
				metrics.ModelEngineStalled.WithLabelValues(model.Name, model.Namespace).Set(0)
				return false, nil
			}
		}
		return fail(marker.Message)
	}
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods, client.InNamespace(model.Namespace), client.MatchingLabels{LabelModel: model.Name}); err != nil {
		return false, err
	}
	prefix := model.Namespace + "/" + model.Name + "/"
	active := map[string]bool{}
	for _, pod := range pods.Items {
		active[prefix+string(model.UID)+"/"+string(pod.UID)+"/"+pod.Name] = true
	}
	r.engineStallObservations.Range(func(k, v any) bool {
		if strings.HasPrefix(k.(string), prefix) && !active[k.(string)] {
			r.engineStallObservations.Delete(k)
		}
		return true
	})
	// Shared runtime pods do not carry the per-model selector. Leave their
	// EngineStalled condition to reconcileViaRuntime instead of clearing it here.
	if len(pods.Items) == 0 && raw == "" {
		return false, nil
	}
	replacementReady := false
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.DeletionTimestamp != nil {
			continue
		}
		ready := false
		for _, c := range pod.Status.Conditions {
			if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
				ready = true
			}
		}
		if raw != "" && pod.UID == marker.PodUID {
			// Retry a deletion interrupted after persisting the restart budget.
			uid := pod.UID
			if err := client.IgnoreNotFound(r.Delete(ctx, pod, &client.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}})); err != nil {
				return true, err
			}
			continue
		}
		replacementReady = replacementReady || ready
		if pod.Status.PodIP == "" {
			continue
		}
		obsKey := model.Namespace + "/" + model.Name + "/" + string(model.UID) + "/" + string(pod.UID) + "/" + pod.Name
		value, _ := r.engineStallObservations.LoadOrStore(obsKey, &engineStallObservation{detector: backend.StallDetector{Window: backend.StallWindowForSpec(&backend.ModelSpec{Config: model.Spec.GetConfigMap()})}})
		obs := value.(*engineStallObservation)
		obs.mu.Lock()
		window := backend.StallWindowForSpec(&backend.ModelSpec{Config: model.Spec.GetConfigMap()})
		if obs.detector.Window != window {
			obs.detector.Window = window
			obs.detector.Reset()
			obs.lastScrape = time.Time{}
		}
		if obs.lastScrape.IsZero() || now.Sub(obs.lastScrape) >= 30*time.Second {
			if !obs.lastScrape.IsZero() && now.Sub(obs.lastScrape) > 90*time.Second {
				obs.detector.Reset()
			}
			sample, err := read(ctx, pod)
			obs.lastScrape = now
			if err != nil {
				obs.detector.Reset()
				obs.stalled = false
			} else {
				obs.sample = sample
				obs.stalled = obs.detector.Observe(sample, now)
			}
		}
		stalled, sample := obs.stalled, obs.sample
		obs.mu.Unlock()
		if !stalled {
			continue
		}
		message := fmt.Sprintf("engine stalled on node %s: %s", pod.Spec.NodeName, sample.String())
		metrics.ModelEngineStalled.WithLabelValues(model.Name, model.Namespace).Set(1)
		if raw != "" {
			return fail("GPUUnresponsive: " + message)
		}
		marker = engineStallRecovery{PodUID: pod.UID, At: now, Node: pod.Spec.NodeName, Message: message}
		if err := r.saveEngineStallRecovery(ctx, model, marker); err != nil {
			return true, err
		}
		model.Status.Phase = aiv1alpha2.ModelPhaseFailed
		clearLoadingStatus(model)
		model.Status.Message = message
		setModelCondition(model, ConditionEngineStalled, true, ReasonEngineStalled, message)
		setModelCondition(model, aiv1alpha2.ConditionModelReady, false, ReasonEngineStalled, message)
		if err := r.statusUpdateIfChanged(ctx, model, origStatus); err != nil {
			return true, err
		}
		if r.Recorder != nil {
			r.Recorder.Event(model, corev1.EventTypeWarning, ReasonEngineStalled, message)
		}
		uid := pod.UID
		if err := client.IgnoreNotFound(r.Delete(ctx, pod, &client.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}})); err != nil {
			return true, err
		}
		metrics.ModelEngineStallRestartsTotal.WithLabelValues(model.Name, model.Namespace).Inc()
		r.engineStallObservations.Delete(obsKey)
		return true, nil
	}
	if raw != "" && replacementReady && !marker.Ready {
		marker.Ready = true
		if err := r.saveEngineStallRecovery(ctx, model, marker); err != nil {
			return true, err
		}
	}
	if raw != "" && !replacementReady {
		b, _ := backend.Get(model.Spec.Backend)
		timeout := b.StartupTimeout()
		if configurable, ok := b.(backend.StartupProbeConfigurer); ok {
			p := configurable.StartupProbeForSpec(&backend.ModelSpec{Config: model.Spec.GetConfigMap(), StartupTimeout: modelColdStartTimeout(model)})
			if p != nil {
				timeout = time.Duration(p.FailureThreshold*p.PeriodSeconds) * time.Second
			}
		}
		if !marker.Ready && now.Sub(marker.At) >= timeout {
			return fail(fmt.Sprintf("GPUUnresponsive on node %s: replacement did not become Ready within %s", marker.Node, timeout))
		}
		model.Status.Phase = aiv1alpha2.ModelPhaseFailed
		clearLoadingStatus(model)
		setModelCondition(model, aiv1alpha2.ConditionModelReady, false, ReasonEngineStalled, "Waiting for engine stall recovery")
		return true, r.statusUpdateIfChanged(ctx, model, origStatus)
	}
	metrics.ModelEngineStalled.WithLabelValues(model.Name, model.Namespace).Set(0)
	setModelCondition(model, ConditionEngineStalled, false, "EngineProgressing", "No engine stall observed")
	return false, nil
}
func (r *ModelReconciler) saveEngineStallRecovery(ctx context.Context, model *aiv1alpha2.Model, marker engineStallRecovery) error {
	data, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	if model.Annotations[EngineStallRecoveryAnnotation] == string(data) {
		return nil
	}
	base := model.DeepCopy()
	if model.Annotations == nil {
		model.Annotations = map[string]string{}
	}
	model.Annotations[EngineStallRecoveryAnnotation] = string(data)
	return r.Patch(ctx, model, client.MergeFrom(base))
}
func readEngineStallMetrics(ctx context.Context, pod *corev1.Pod) (backend.VLLMStallSample, error) {
	return backend.ReadVLLMStallSample(ctx, "http://"+net.JoinHostPort(pod.Status.PodIP, "8000")+"/metrics")
}
