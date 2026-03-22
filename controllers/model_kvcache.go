/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/constants"
)

// reconcileKVCachePressure checks KV-cache utilization from agent annotations
// and reacts according to the model's KVCache pressure policy.
func (r *ModelReconciler) reconcileKVCachePressure(ctx context.Context, model *aiv1alpha2.Model) {
	if model.Spec.KVCache == nil {
		return
	}

	log := log.FromContext(ctx)

	// Read KV-cache utilization from the node where the model is running.
	if model.Status.GPU == nil || model.Status.GPU.Node == "" {
		return
	}

	node := &corev1.Node{}
	if err := r.Get(ctx, types.NamespacedName{Name: model.Status.GPU.Node}, node); err != nil {
		log.V(1).Info("cannot read node for KV-cache check", "node", model.Status.GPU.Node, "error", err)
		return
	}

	utilStr := ""
	if node.Annotations != nil {
		utilStr = node.Annotations[constants.NodeAnnotationKVCacheUsage]
	}
	if utilStr == "" {
		return
	}

	util, err := strconv.ParseFloat(strings.TrimSpace(utilStr), 64)
	if err != nil {
		return
	}

	// Update status
	if model.Status.KVCache == nil {
		model.Status.KVCache = &aiv1alpha2.KVCacheStatus{}
	}
	model.Status.KVCache.Utilization = fmt.Sprintf("%.4f", util)

	highWatermark := model.Spec.GetKVCacheHighWatermark()
	lowWatermark := model.Spec.GetKVCacheLowWatermark()

	// Check for eviction restore: model is scaled to 0, so no utilization signal.
	// Restore after cooldown to allow the model to come back up.
	if model.Status.KVCache.Evicted {
		cooldown := model.Spec.GetKVCacheReconfigureCooldown()
		if model.Status.KVCache.EvictedAt != nil && time.Since(model.Status.KVCache.EvictedAt.Time) >= cooldown {
			log.Info("KV-cache eviction cooldown elapsed, restoring model",
				"model", model.Name)
			model.Status.KVCache.Evicted = false
			model.Status.KVCache.EvictedAt = nil
			model.Status.KVCache.LastAction = "EvictRestored"
			model.Status.KVCache.Pressure = false
			r.Recorder.Event(model, corev1.EventTypeNormal, "KVCacheEvictRestored",
				"Eviction cooldown elapsed, restoring model replicas")
			return
		}
		// Still in cooldown — don't evaluate pressure further.
		model.Status.KVCache.LastAction = "EvictActive"
		return
	}

	// Check for reconfigure restore: if reconfigured and util dropped below low watermark,
	// and cooldown has elapsed, restore original config.
	if model.Status.KVCache.Reconfigured && util < lowWatermark {
		cooldown := model.Spec.GetKVCacheReconfigureCooldown()
		if model.Status.KVCache.ReconfiguredAt != nil && time.Since(model.Status.KVCache.ReconfiguredAt.Time) >= cooldown {
			log.Info("KV-cache pressure relieved, restoring original config",
				"model", model.Name, "utilization", util, "lowWatermark", lowWatermark)
			model.Status.KVCache.Reconfigured = false
			model.Status.KVCache.ReconfiguredMaxNumSeqs = nil
			model.Status.KVCache.OriginalMaxNumSeqs = nil
			model.Status.KVCache.ReconfiguredAt = nil
			model.Status.KVCache.LastAction = "Restored"
			model.Status.KVCache.Pressure = false
			r.Recorder.Event(model, corev1.EventTypeNormal, "KVCacheRestored",
				fmt.Sprintf("KV-cache utilization %.2f below low watermark %.2f, restored original maxNumSeqs", util, lowWatermark))
			return
		}
	}

	if util < highWatermark {
		model.Status.KVCache.Pressure = false
		return
	}

	// Pressure detected
	model.Status.KVCache.Pressure = true
	now := metav1.Now()
	model.Status.KVCache.LastPressureTime = &now

	policy := model.Spec.GetKVCachePressurePolicy()

	switch policy {
	case aiv1alpha2.KVCachePressurePolicyObserve:
		model.Status.KVCache.LastAction = "Observed"
		r.Recorder.Event(model, corev1.EventTypeWarning, "KVCachePressure",
			fmt.Sprintf("KV-cache utilization %.2f exceeds high watermark %.2f (policy: Observe)", util, highWatermark))

	case aiv1alpha2.KVCachePressurePolicyReconfigure:
		r.handleKVCacheReconfigure(ctx, model, util, highWatermark, lowWatermark)

	case aiv1alpha2.KVCachePressurePolicyEvict:
		r.handleKVCacheEvict(ctx, model, util, highWatermark)
	}
}

// handleKVCacheReconfigure reduces maxNumSeqs to alleviate KV-cache pressure.
// On the next reconcile, ensureDeployment merges the override into backend args,
// triggering a rolling update of the model deployment.
func (r *ModelReconciler) handleKVCacheReconfigure(ctx context.Context, model *aiv1alpha2.Model, util, highWatermark, lowWatermark float64) {
	log := log.FromContext(ctx)

	// Already reconfigured — don't reduce further, wait for cooldown + restore.
	if model.Status.KVCache.Reconfigured {
		model.Status.KVCache.LastAction = "ReconfigureActive"
		return
	}

	// Determine current maxNumSeqs from spec config.
	currentMaxSeqs := int32(model.Spec.ConfigInt("maxNumSeqs", 256))

	// Reduce by 50%, minimum 1.
	reduced := currentMaxSeqs / 2
	if reduced < 1 {
		reduced = 1
	}

	now := metav1.Now()
	model.Status.KVCache.Reconfigured = true
	model.Status.KVCache.ReconfiguredAt = &now
	model.Status.KVCache.OriginalMaxNumSeqs = &currentMaxSeqs
	model.Status.KVCache.ReconfiguredMaxNumSeqs = &reduced
	model.Status.KVCache.LastAction = fmt.Sprintf("Reconfigured:maxNumSeqs=%d->%d", currentMaxSeqs, reduced)

	log.Info("KV-cache pressure: reconfiguring maxNumSeqs",
		"model", model.Name, "utilization", util,
		"highWatermark", highWatermark,
		"originalMaxNumSeqs", currentMaxSeqs,
		"reducedMaxNumSeqs", reduced)

	r.Recorder.Event(model, corev1.EventTypeWarning, "KVCacheReconfigure",
		fmt.Sprintf("KV-cache utilization %.2f exceeds high watermark %.2f, reducing maxNumSeqs %d -> %d",
			util, highWatermark, currentMaxSeqs, reduced))
}

// handleKVCacheEvict scales down the model to 0 replicas to relieve KV-cache pressure.
// The desiredReplicas() function reads KVCacheStatus.Evicted on the next reconcile
// and returns 0, which triggers ensureDeployment to scale down the deployment.
func (r *ModelReconciler) handleKVCacheEvict(ctx context.Context, model *aiv1alpha2.Model, util, highWatermark float64) {
	log := log.FromContext(ctx)

	if model.Status.KVCache.Evicted {
		model.Status.KVCache.LastAction = "EvictActive"
		return
	}

	now := metav1.Now()
	model.Status.KVCache.Evicted = true
	model.Status.KVCache.EvictedAt = &now
	model.Status.KVCache.LastAction = "Evicted"

	log.Info("KV-cache pressure: evicting model",
		"model", model.Name, "utilization", util, "highWatermark", highWatermark)

	r.Recorder.Event(model, corev1.EventTypeWarning, "KVCacheEvict",
		fmt.Sprintf("KV-cache utilization %.2f exceeds high watermark %.2f, scaling down to 0 replicas",
			util, highWatermark))
}
