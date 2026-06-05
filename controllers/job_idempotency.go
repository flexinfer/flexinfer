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
	"os"
	"sync"

	"github.com/flexinfer/flexinfer/pkg/metrics"
	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// AnnotationControllerInstance records which controller process (pod) created a
// Job. During a rolling update two controller generations can briefly run
// concurrently (the outgoing pod is still reconciling while the incoming pod
// becomes ready). Stamping the creating instance lets a later sweep identify
// and reap Jobs left behind by a superseded generation.
const AnnotationControllerInstance = "flexinfer.ai/controller-instance"

var (
	controllerInstanceOnce sync.Once
	controllerInstanceID   string
)

// ControllerInstanceID returns a stable identifier for this controller process.
// It prefers the HOSTNAME environment variable (the pod name under Kubernetes)
// and falls back to os.Hostname(). The result is computed once and cached. It
// is empty only when neither source is available, in which case the
// provenance annotation is simply omitted.
func ControllerInstanceID() string {
	controllerInstanceOnce.Do(func() {
		controllerInstanceID = resolveControllerInstanceID(os.Getenv("HOSTNAME"), os.Hostname)
	})
	return controllerInstanceID
}

// resolveControllerInstanceID contains the pure resolution logic behind
// ControllerInstanceID, extracted so it can be unit-tested deterministically
// without depending on the host environment. envHostname wins when non-empty;
// otherwise the osHostname result is used; on error or empty it returns "".
func resolveControllerInstanceID(envHostname string, osHostname func() (string, error)) string {
	if envHostname != "" {
		return envHostname
	}
	if osHostname != nil {
		if h, err := osHostname(); err == nil {
			return h
		}
	}
	return ""
}

// createJobIdempotent creates job while tolerating the AlreadyExists race that
// occurs when two controller generations reconcile the same parent object
// during a rolling update. Without this guard, the losing reconcile returns a
// hard error and requeues, producing error spam and (for expensive pipeline
// jobs) repeated churn.
//
// The Job is stamped with AnnotationControllerInstance for provenance before
// the create attempt.
//
// It returns created=true only when this call actually created the Job.
// created=false with err=nil means an equivalent Job already existed (the
// desired post-condition is still satisfied). Any non-AlreadyExists error is
// returned unchanged.
//
// jobType labels the conflict counter so the rollout race can be observed per
// pipeline stage (e.g. "abliterate", "quantize", "download"). It does not
// affect the create itself.
func createJobIdempotent(ctx context.Context, w client.Writer, job *batchv1.Job, jobType string) (created bool, err error) {
	if id := ControllerInstanceID(); id != "" {
		if job.Annotations == nil {
			job.Annotations = map[string]string{}
		}
		job.Annotations[AnnotationControllerInstance] = id
	}
	if err := w.Create(ctx, job); err != nil {
		if apierrors.IsAlreadyExists(err) {
			// The race fired: another controller generation already created an
			// equivalent Job. Record it so the (now silently tolerated) rollout
			// conflict stays observable instead of vanishing into success.
			metrics.ModelCacheJobCreateConflictsTotal.WithLabelValues(jobType).Inc()
			return false, nil
		}
		return false, err
	}
	return true, nil
}
