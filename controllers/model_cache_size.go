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
	"encoding/json"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// Cache jobs report how much they staged through the container termination
// message, the same channel quantization jobs use for their metadata (see
// readQuantizationMetadataFromJob). The controller reads it when the Job first
// reports Succeeded and then carries the value forward: a Job and its pods are
// garbage-collected long before the weights are, so a reconcile that cannot
// find a measurement must never conclude the cache is empty.

// cacheSizeReportFunc defines report_cache_size(), which every cache job
// prepends to its script and calls on each success path -- including the
// "already staged" short-circuits, since a job that finds its marker and exits
// early has still confirmed a populated cache and should report its size.
//
// du -sk is POSIX and present on both the busybox (alpine) and coreutils
// (debian) images these jobs run on; GNU's `du -sb` is not available on
// busybox. Block usage is also the better answer to "will this volume fill up"
// than apparent size. The measurement is reported in KiB rather than bytes so
// the multiplication happens in Go: a 32-bit shell would overflow the
// arithmetic expansion somewhere north of a 2TiB cache.
//
// Every step is guarded so that an unmeasurable cache degrades to "unknown"
// rather than failing a staging job that has already done its real work.
const cacheSizeReportFunc = `
report_cache_size() {
  _rcs_target="$1"
  [ -e "$_rcs_target" ] || return 0
  _rcs_kib=$(du -sk "$_rcs_target" 2>/dev/null | head -n 1 | cut -f1) || return 0
  case "$_rcs_kib" in
    '' | *[!0-9]*) return 0 ;;
  esac
  printf '{"sizeKiB":%s}' "$_rcs_kib" > /dev/termination-log 2>/dev/null || true
}
`

// cacheJobSizeMetadata mirrors the termination-log JSON that cache jobs emit.
type cacheJobSizeMetadata struct {
	SizeKiB int64 `json:"sizeKiB,omitempty"`
}

// readCacheSizeFromJob returns the size, in bytes, that a completed cache job
// reported for the artifact it staged.
//
// It returns 0 when nothing reported one: a Job created before this reporting
// existed, a pod already garbage-collected, an unwritable termination log. 0
// means "unknown", never "empty" -- callers must not overwrite a recorded size
// with it.
//
// Cache jobs carry a single container (see buildCacheJob), so unlike the
// quantization reader there is no container name to disambiguate. Only success
// paths call report_cache_size, so a retried job's failed attempts contribute
// nothing and taking the largest reported value picks the run that finished.
func (r *ModelReconciler) readCacheSizeFromJob(ctx context.Context, namespace, jobName string) int64 {
	podList := &corev1.PodList{}
	if err := r.List(ctx, podList, client.InNamespace(namespace), client.MatchingLabels{"job-name": jobName}); err != nil {
		return 0
	}

	var kib int64
	for i := range podList.Items {
		for _, cs := range podList.Items[i].Status.ContainerStatuses {
			if cs.State.Terminated == nil {
				continue
			}
			msg := strings.TrimSpace(cs.State.Terminated.Message)
			if msg == "" {
				continue
			}
			var meta cacheJobSizeMetadata
			if err := json.Unmarshal([]byte(msg), &meta); err != nil {
				continue
			}
			if meta.SizeKiB > kib {
				kib = meta.SizeKiB
			}
		}
	}
	return kib * 1024
}

// carryForwardCacheSize picks the size to record this pass: a fresh
// measurement wins, otherwise the previously recorded one survives. Staged
// weights outlive the Job that staged them, so losing the measurement must not
// zero the number an operator is using for capacity planning.
func carryForwardCacheSize(measured int64, previous *aiv1alpha2.CacheStatus) int64 {
	if measured > 0 {
		return measured
	}
	if previous != nil {
		return previous.SizeBytes
	}
	return 0
}

// stagedCacheSize resolves status.cache.sizeBytes for a cache whose staging Job
// has just been observed. succeeded reports whether that Job completed.
//
// The Job is only read when it succeeded and the size is not already settled,
// so a steady-state reconcile of a measured cache costs nothing. A cache that
// is re-entering Ready -- a source change deletes and recreates the staging
// Job, and the new artifact may be a different size -- is always re-measured.
func (r *ModelReconciler) stagedCacheSize(ctx context.Context, namespace, jobName string, succeeded bool, previous *aiv1alpha2.CacheStatus) int64 {
	if !succeeded {
		return carryForwardCacheSize(0, previous)
	}
	if previous != nil && previous.Ready && previous.SizeBytes > 0 {
		return previous.SizeBytes
	}
	return carryForwardCacheSize(r.readCacheSizeFromJob(ctx, namespace, jobName), previous)
}
