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
	"path/filepath"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// What happens to a Model's Local cache directory when the Model is deleted.
//
// A `strategy: Local` cache is a hostPath directory on one GPU node,
// <hostPath>/<namespace>/<name>. Nothing in Kubernetes owns it, so until this
// policy existed every deleted Model left its weights on the node for good.
// Debug canaries of one 27B artifact left eight 18 GB copies on the 7900 XTX
// root in a fortnight, and that disk trip (2026-11-06) took the card out for
// 17 hours.
//
// Two settings decide, the Model annotation first and the controller default
// second:
//
//   - flexinfer.ai/local-cache-on-delete: retain | delete (on the Model);
//   - DEFAULT_LOCAL_CACHE_ON_DELETE: retain | delete (controller env; the chart
//     renders it from controller.runtime.localCache.onDelete).
//
// The default is retain, today's behavior, because operators deliberately
// reuse a deleted canary's Model name to skip re-staging the same artifact.
// `delete` runs a one-shot Job on the Model's node that removes the directory;
// the Job carries no owner reference (the Model is going away), finishes in
// seconds for tens of GB, and is reaped by its TTL.
const (
	// LocalCacheOnDeleteRetain keeps the directory (the historical behavior).
	LocalCacheOnDeleteRetain = "retain"
	// LocalCacheOnDeleteDelete removes the directory through a node Job.
	LocalCacheOnDeleteDelete = "delete"

	// EnvLocalCacheOnDelete is the controller's default policy variable.
	EnvLocalCacheOnDelete = "DEFAULT_LOCAL_CACHE_ON_DELETE"

	localCacheCleanupJobSuffix = "-cache-cleanup"
	localCacheCleanupMount     = "/models"
	// localCacheCleanupJobTTL keeps the finished Job (and its log line with the
	// bytes freed) around long enough for an operator to read it.
	localCacheCleanupJobTTL = int32(600)
)

// ParseLocalCacheOnDelete validates a policy string. Empty means retain. Any
// other spelling is an error: a typo must not silently turn a requested
// cleanup into a retain, or the reverse.
func ParseLocalCacheOnDelete(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", LocalCacheOnDeleteRetain:
		return LocalCacheOnDeleteRetain, nil
	case LocalCacheOnDeleteDelete:
		return LocalCacheOnDeleteDelete, nil
	default:
		return "", fmt.Errorf("local cache on-delete policy %q: want %q or %q", raw, LocalCacheOnDeleteRetain, LocalCacheOnDeleteDelete)
	}
}

// localCacheOnDeletePolicy resolves the effective policy for a Model and says
// where it came from ("annotation" or "default"). An invalid annotation is
// reported and falls back to the controller default, so a misspelled
// annotation never deletes.
func (r *ModelReconciler) localCacheOnDeletePolicy(model *aiv1alpha2.Model) (policy, source string, err error) {
	if raw, ok := model.Annotations[AnnotationLocalCacheOnDelete]; ok {
		if p, perr := ParseLocalCacheOnDelete(raw); perr == nil {
			return p, "annotation", nil
		} else {
			err = perr
		}
	}
	p, perr := ParseLocalCacheOnDelete(r.LocalCacheOnDelete)
	if perr != nil {
		// The manager validates the env at startup; a bad value here means a
		// test or caller set the field directly. Retain is the safe answer.
		return LocalCacheOnDeleteRetain, "default", perr
	}
	return p, "default", err
}

// localCacheBasePath is the hostPath base under which Local caches live:
// spec.cache.hostPath, or the controller default.
func localCacheBasePath(model *aiv1alpha2.Model) string {
	if model.Spec.Cache != nil && model.Spec.Cache.HostPath != "" {
		return model.Spec.Cache.HostPath
	}
	return "/var/lib/flexinfer/models"
}

// localCacheCleanupJobName is the Job a deleted Model's cleanup runs under.
func localCacheCleanupJobName(model *aiv1alpha2.Model) string {
	return model.Name + localCacheCleanupJobSuffix
}

// cleanupLocalCache applies the on-delete policy to a Model with a Local
// cache. With `delete` it creates the node Job; with `retain` it logs the
// directory it leaves behind. It never fails the Model's finalizer: the Job
// is best-effort, like the flash-tmpfs cleanup.
func (r *ModelReconciler) cleanupLocalCache(ctx context.Context, model *aiv1alpha2.Model) error {
	if cacheStrategy(model) != "Local" {
		return nil
	}
	logger := log.FromContext(ctx)
	cachePath := resolveLocalCachePath(model)

	policy, source, perr := r.localCacheOnDeletePolicy(model)
	if perr != nil {
		logger.Error(perr, "Ignoring invalid Local cache on-delete policy", "path", cachePath)
	}
	if policy != LocalCacheOnDeleteDelete {
		logger.Info("Local cache retained on delete", "path", cachePath, "policy", policy, "source", source)
		return nil
	}

	nodeSelector, tolerations := modelNodeSelectorAndTolerations(model)
	if len(nodeSelector) == 0 {
		if model.Status.GPU != nil && model.Status.GPU.Node != "" {
			nodeSelector = map[string]string{"kubernetes.io/hostname": model.Status.GPU.Node}
		} else {
			// Without a node there is nowhere to send the Job. Say so rather
			// than guess: the directory stays and the operator can find it.
			logger.Info("Local cache retained on delete: no node to run the cleanup on", "path", cachePath)
			r.recordEvent(model, corev1.EventTypeWarning, "LocalCacheRetained",
				"Local cache %s not removed: the Model names no node (spec.nodeSelector or status.gpu.node)", cachePath)
			return nil
		}
	}

	job, err := r.jobForLocalCacheCleanup(model, nodeSelector, tolerations)
	if err != nil {
		return err
	}
	// No owner reference: the Model is being deleted and the Job must outlive
	// it. TTLSecondsAfterFinished reaps the Job.
	if err := r.Create(ctx, job); err != nil {
		if errors.IsAlreadyExists(err) {
			return nil
		}
		return fmt.Errorf("create Local cache cleanup job: %w", err)
	}
	logger.Info("Created Local cache cleanup job", "job", job.Name, "path", cachePath, "nodeSelector", nodeSelector, "source", source)
	r.recordEvent(model, corev1.EventTypeNormal, "LocalCacheCleanup",
		"Removing Local cache %s through Job %s (policy %s from %s)", cachePath, job.Name, policy, source)
	return nil
}

// jobForLocalCacheCleanup builds the one-shot Job that removes a Model's Local
// cache directory. It mounts the cache *base* directory, not the Model's
// directory, so the directory itself can be removed (a mount point cannot) and
// a DirectoryOrCreate hostPath cannot resurrect it.
func (r *ModelReconciler) jobForLocalCacheCleanup(model *aiv1alpha2.Model, nodeSelector map[string]string, tolerations []corev1.Toleration) (*batchv1.Job, error) {
	base := localCacheBasePath(model)
	cachePath := resolveLocalCachePath(model)

	// The Job removes <base>/<namespace>/<name> and nothing else. Refuse any
	// shape that is not exactly that, here and again inside the container.
	rel, err := filepath.Rel(base, cachePath)
	if err != nil || rel != filepath.Join(model.Namespace, model.Name) || strings.HasPrefix(rel, "..") {
		return nil, fmt.Errorf("local cache path %q is not <base>/<namespace>/<name> under %q", cachePath, base)
	}
	target := filepath.Join(localCacheCleanupMount, rel)

	script := fmt.Sprintf(`set -eu
TARGET=%q
case "$TARGET" in
  %s/*/*) ;;
  *) echo "refusing to remove $TARGET"; exit 1 ;;
esac
if [ ! -e "$TARGET" ]; then
  echo "Local cache already absent: $TARGET"
  exit 0
fi
SIZE_KB=$(du -skx "$TARGET" 2>/dev/null | cut -f1 || echo 0)
rm -rf -- "$TARGET"
echo "Local cache removed: $TARGET (${SIZE_KB} KiB)"
`, target, localCacheCleanupMount)

	job := buildCacheJob(CacheJobParams{
		Name:      localCacheCleanupJobName(model),
		Namespace: model.Namespace,
		Labels:    r.labelsForModel(model),
		Annotations: map[string]string{
			AnnotationSource:    model.Spec.Source,
			AnnotationCacheKind: "local-cleanup",
			AnnotationCachePath: cachePath,
		},
		NodeSelector:            nodeSelector,
		Tolerations:             tolerations,
		BackoffLimit:            1,
		RestartPolicy:           corev1.RestartPolicyNever,
		TTLSecondsAfterFinished: ptr.To(localCacheCleanupJobTTL),
		ContainerName:           "cleanup",
		Image:                   ImageAlpine,
		Command:                 []string{"/bin/sh", "-c"},
		Args:                    []string{script},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("32Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("64Mi"),
			},
		},
		VolumeMounts: []corev1.VolumeMount{{
			Name:      "cache-base",
			MountPath: localCacheCleanupMount,
		}},
		Volumes: []corev1.Volume{{
			Name: "cache-base",
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: base,
					Type: hostPathTypePtr(corev1.HostPathDirectoryOrCreate),
				},
			},
		}},
	})
	return job, nil
}

// cancelPendingLocalCacheCleanup deletes an unfinished cleanup Job left by a
// previous Model of the same name. A Model recreated under a deleted Model's
// name wants that cache (operators reuse canary names on purpose), so a
// cleanup that has not run yet must not run now. A Job that already finished
// is left for its TTL. Best-effort; never blocks reconcile.
func (r *ModelReconciler) cancelPendingLocalCacheCleanup(ctx context.Context, model *aiv1alpha2.Model) {
	if cacheStrategy(model) != "Local" {
		return
	}
	job := &batchv1.Job{}
	key := types.NamespacedName{Namespace: model.Namespace, Name: localCacheCleanupJobName(model)}
	if err := r.Get(ctx, key, job); err != nil {
		return
	}
	if job.Status.Succeeded > 0 || job.Status.Failed > 0 {
		return
	}
	logger := log.FromContext(ctx)
	propagation := client.PropagationPolicy("Background")
	if err := r.Delete(ctx, job, propagation); err != nil && !errors.IsNotFound(err) {
		logger.Error(err, "Failed to cancel pending Local cache cleanup job", "job", job.Name)
		return
	}
	logger.Info("Cancelled pending Local cache cleanup job: the Model was recreated", "job", job.Name)
	r.recordEvent(model, corev1.EventTypeNormal, "LocalCacheCleanupCancelled",
		"Cancelled pending cleanup Job %s: this Model reuses the cache", job.Name)
}

// recordEvent emits an event when a recorder is wired (tests build the
// reconciler without one).
func (r *ModelReconciler) recordEvent(model *aiv1alpha2.Model, eventType, reason, format string, args ...any) {
	if r.Recorder == nil {
		return
	}
	r.Recorder.Eventf(model, eventType, reason, format, args...)
}
