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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/flexinfer/flexinfer/pkg/constants"
)

// GPULease describes a transient, scheduler-honored hold on a shared-GPU group.
// A training/quant workload acquires a lease so chooseSharedGroupLeaders parks
// the serving incumbent and keeps it parked, freeing the amd.com/gpu slot, then
// releases the lease so serving re-promotes.
//
// Carrier (slice 1): a labeled ConfigMap. The ExpiresAt TTL is the crash-safety
// backstop -- the election ignores an expired lease so a dead acquirer cannot
// strand serving forever.
type GPULease struct {
	Group      string
	Node       string
	Owner      string
	AcquiredAt time.Time
	ExpiresAt  time.Time
}

// active reports whether the lease is currently honored at time now. A zero
// ExpiresAt means "no TTL" (honored indefinitely while the ConfigMap exists);
// otherwise the lease is honored only while now < ExpiresAt.
func (l *GPULease) active(now time.Time) bool {
	if l == nil || l.Group == "" {
		return false
	}
	if l.ExpiresAt.IsZero() {
		return true
	}
	return now.Before(l.ExpiresAt)
}

// leaseFromConfigMap decodes a lease ConfigMap. Returns nil if the ConfigMap is
// not a well-formed lease (missing the group data key).
func leaseFromConfigMap(cm *corev1.ConfigMap) *GPULease {
	if cm == nil {
		return nil
	}
	group := cm.Data[constants.GPULeaseDataGroup]
	if group == "" {
		return nil
	}
	l := &GPULease{
		Group: group,
		Node:  cm.Data[constants.GPULeaseDataNode],
		Owner: cm.Data[constants.GPULeaseDataOwner],
	}
	if t, err := time.Parse(time.RFC3339, cm.Data[constants.GPULeaseDataAcquiredAt]); err == nil {
		l.AcquiredAt = t
	}
	if t, err := time.Parse(time.RFC3339, cm.Data[constants.GPULeaseDataExpiresAt]); err == nil {
		l.ExpiresAt = t
	}
	return l
}

// gpuLeaseConfigMap renders the lease carrier ConfigMap for a group.
func gpuLeaseConfigMap(namespace string, lease GPULease) *corev1.ConfigMap {
	data := map[string]string{
		constants.GPULeaseDataGroup:      lease.Group,
		constants.GPULeaseDataNode:       lease.Node,
		constants.GPULeaseDataOwner:      lease.Owner,
		constants.GPULeaseDataAcquiredAt: lease.AcquiredAt.UTC().Format(time.RFC3339),
	}
	if !lease.ExpiresAt.IsZero() {
		data[constants.GPULeaseDataExpiresAt] = lease.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      constants.GPULeaseConfigMapName(lease.Group),
			Namespace: namespace,
			Labels: map[string]string{
				constants.LabelGPULeaseGroup: lease.Group,
			},
		},
		Data: data,
	}
}

// findActiveLease returns the active (unexpired) lease for a group, or nil. It
// is the single read the election uses to decide park-and-hold; it filters
// expired leases so crash-safety is automatic.
func findActiveLease(ctx context.Context, c client.Reader, namespace, group string, now time.Time) (*GPULease, error) {
	cm := &corev1.ConfigMap{}
	key := client.ObjectKey{Namespace: namespace, Name: constants.GPULeaseConfigMapName(group)}
	if err := c.Get(ctx, key, cm); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	lease := leaseFromConfigMap(cm)
	if lease == nil || !lease.active(now) {
		return nil, nil
	}
	return lease, nil
}

// groupHasActiveLease reports whether a shared-GPU group is currently leased.
// On read error it returns (false, err); callers in the election path treat a
// read error as "not leased" so a transient API failure never silently strands
// the serving lane (fail-open toward serving).
func (r *ModelReconciler) groupHasActiveLease(ctx context.Context, namespace, group string, now time.Time) (*GPULease, error) {
	var reader client.Reader = r.APIReader
	if reader == nil {
		reader = r.Client
	}
	return findActiveLease(ctx, reader, namespace, group, now)
}

// acquireGPULease creates or refreshes the lease ConfigMap for a group with the
// given TTL. Idempotent: re-acquiring extends the TTL in place. The caller
// should set an owner reference (to its ModelCache) so the lease is GC'd if the
// owner is deleted -- combined with the TTL this gives two independent
// crash-safety backstops.
func acquireGPULease(ctx context.Context, c client.Client, namespace string, lease GPULease) (*corev1.ConfigMap, error) {
	desired := gpuLeaseConfigMap(namespace, lease)
	existing := &corev1.ConfigMap{}
	key := client.ObjectKey{Namespace: namespace, Name: desired.Name}
	if err := c.Get(ctx, key, existing); err != nil {
		if apierrors.IsNotFound(err) {
			if err := c.Create(ctx, desired); err != nil {
				return nil, err
			}
			return desired, nil
		}
		return nil, err
	}
	existing.Labels = desired.Labels
	existing.Data = desired.Data
	if err := c.Update(ctx, existing); err != nil {
		return nil, err
	}
	return existing, nil
}

// releaseGPULease deletes the lease ConfigMap for a group. Not-found is treated
// as success (already released / never held).
func releaseGPULease(ctx context.Context, c client.Client, namespace, group string) error {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      constants.GPULeaseConfigMapName(group),
			Namespace: namespace,
		},
	}
	if err := c.Delete(ctx, cm); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}
