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
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	pkgrt "github.com/flexinfer/flexinfer/pkg/runtime"
)

// ConditionRuntimeEligible is present, False, while a launch override the
// runtime load payload cannot carry keeps a Model off the node-local runtime.
// It is removed once the override is gone; Models without one never carry it,
// so this adds no status write to the rest of the fleet.
const ConditionRuntimeEligible = "RuntimeEligible"

// ReasonLaunchOverride is the RuntimeEligible reason for a spec.config launch
// override (spec.config.command) the runtime would silently drop.
const ReasonLaunchOverride = "LaunchOverride"

// EventRuntimeLaunchOverride is the warning recorded when a launch override is
// what routes a Model to the Deployment path instead of the runtime.
const EventRuntimeLaunchOverride = "RuntimeLaunchOverride"

// runtimeLaunchOverrideEventCooldown throttles EventRuntimeLaunchOverride: it
// restates a static spec property, so once an hour per Model is plenty.
const runtimeLaunchOverrideEventCooldown = time.Hour

// syncRuntimeLaunchOverride records why a Model with a launch override the
// runtime cannot carry is served by a Deployment, and reports whether it has
// one (the caller then hands it off from the runtime like dedicatedDeployment).
//
// The condition is set even when config.dedicatedDeployment already routes the
// Model to the Deployment path, so the blocker is visible before anyone drops
// the opt-out to move the lane runtime-managed. The event fires only when the
// override is what decides the path. Status is written only when the condition
// changes, which keeps a parked lane's steady pass write-free.
func (r *ModelReconciler) syncRuntimeLaunchOverride(ctx context.Context, model *aiv1alpha2.Model) (bool, error) {
	key, override := pkgrt.UncarriedLaunchOverride(model)
	var before *metav1.Condition
	if cond := modelCondition(model.Status.Conditions, ConditionRuntimeEligible); cond != nil {
		snapshot := *cond
		before = &snapshot
	}
	if !override {
		if before == nil {
			return false, nil
		}
		meta.RemoveStatusCondition(&model.Status.Conditions, ConditionRuntimeEligible)
		return false, r.persistStatus(ctx, model)
	}

	message := pkgrt.LaunchOverrideIneligibleReason(key)
	setModelCondition(model, ConditionRuntimeEligible, false, ReasonLaunchOverride, message)
	if !model.Spec.ConfigBool("dedicatedDeployment", false) &&
		r.shouldEmitThrottledEvent(model, EventRuntimeLaunchOverride, runtimeLaunchOverrideEventCooldown) {
		r.Recorder.Event(model, corev1.EventTypeWarning, EventRuntimeLaunchOverride,
			"Serving via a dedicated Deployment, not the node runtime: "+message)
	}
	if after := modelCondition(model.Status.Conditions, ConditionRuntimeEligible); before != nil && after != nil && *before == *after {
		return true, nil
	}
	return true, r.persistStatus(ctx, model)
}
