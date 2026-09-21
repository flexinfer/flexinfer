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

package commands

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// AnnotationPrefetchRequestedAt mirrors the controller constant. The CLI does
// not import the controllers package, which pulls in the whole manager.
const AnnotationPrefetchRequestedAt = "flexinfer.ai/prefetch-requested-at"

var (
	cacheWaitTimeout  time.Duration
	cacheWaitInterval time.Duration
	cacheEvictConfirm bool
)

var cacheCmd = &cobra.Command{
	Use:   "cache",
	Short: "Inspect and manage model caches",
	Long: `Commands for inspecting and managing cached model weights.

A model's cache is what makes a cold start cheap: with weights already staged,
activation skips the download. Staging them costs storage for models that may
never be asked for, so spec.cache.prefetch trades storage against first-request
latency per model:

  Eager     stage as soon as the Model reconciles (default)
  OnDemand  stage once something wants the model
  Disabled  stage only on an explicit prefetch request

Storage strategies:
  SharedPVC  shared storage on a ReadWriteMany PVC
  Local      a hostPath directory on the GPU node (e.g. NVMe)
  Memory     RAM cache using /dev/shm
  None       no caching; the model downloads on every start`,
}

var cacheStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show cache state for every model",
	Long: `Display cache state across both model APIs: v1alpha2 Models (status.cache)
first, then any v1alpha1 ModelCache pipeline resources.

Examples:
  # Cache state for every model in the namespace
  flexinfer cache status

  # Across all namespaces
  flexinfer cache status -A`,
	RunE: runCacheStatus,
}

var cachePrefetchCmd = &cobra.Command{
	Use:   "prefetch MODEL",
	Short: "Stage a model's weights now, without starting its GPU pod",
	Long: `Request an immediate cache prefetch for a model.

The request is honored whatever spec.cache.prefetch.policy says, so it also
warms models parked on OnDemand or Disabled. If a previous staging job failed,
the request clears it so staging retries; a job that is still running is left
alone rather than restarted.

This stages weights only. It does not scale the model up or claim a GPU.

Examples:
  # Warm a model ahead of a batch of work
  flexinfer cache prefetch qwen38-27b-autoround-workhorse

  # Warm it and block until the weights are on disk
  flexinfer cache prefetch qwen38-27b-autoround-workhorse --wait`,
	Args: cobra.ExactArgs(1),
	RunE: runCachePrefetch,
}

var cacheWaitCmd = &cobra.Command{
	Use:   "wait MODEL",
	Short: "Block until a model's weights are staged",
	Long: `Poll a model's cache status until it reports Ready, the staging job fails,
or the timeout expires.

Exit status is 0 once the cache is ready, and non-zero on failure or timeout,
so this composes in scripts ahead of a request that would otherwise pay a cold
start.

Examples:
  flexinfer cache wait qwen38-27b-autoround-workhorse --timeout 30m`,
	Args: cobra.ExactArgs(1),
	RunE: runCacheWait,
}

var cacheCapacityCmd = &cobra.Command{
	Use:   "capacity",
	Short: "Report cache storage pressure and reclaim candidates",
	Long: `Summarize the PVCs backing model caches, which models occupy them, and which
staged caches have gone idle past their spec.cache.prefetch.idleTTL.

Reclaim candidates are reported, never acted on. Use 'flexinfer cache evict' to
act on one.

Examples:
  flexinfer cache capacity
  flexinfer cache capacity -A`,
	RunE: runCacheCapacity,
}

var cacheEvictCmd = &cobra.Command{
	Use:   "evict MODEL",
	Short: "Reclaim a model's cache storage (destructive)",
	Long: `Delete the cache PVC backing a model so its storage is reclaimed.

This is destructive: the next activation re-downloads the weights in full, which
for a large model can take tens of minutes. It requires --confirm.

Eviction is refused unless the PVC was auto-created for this one model, which is
established by a controller owner reference back to the Model. A PVC named
explicitly in spec.cache.pvcName, or shared with another model, is never
deleted. Local (hostPath) caches cannot be reclaimed from here at all, because
removing files from a node's disk needs a privileged pod on that node.

Examples:
  flexinfer cache evict stale-experiment-model --confirm`,
	Args: cobra.ExactArgs(1),
	RunE: runCacheEvict,
}

func init() {
	cacheCmd.AddCommand(cacheStatusCmd)
	cacheCmd.AddCommand(cachePrefetchCmd)
	cacheCmd.AddCommand(cacheWaitCmd)
	cacheCmd.AddCommand(cacheCapacityCmd)
	cacheCmd.AddCommand(cacheEvictCmd)

	cachePrefetchCmd.Flags().BoolVar(&cacheWaitAfterPrefetch, "wait", false, "block until the weights are staged")
	cachePrefetchCmd.Flags().DurationVar(&cacheWaitTimeout, "timeout", 30*time.Minute, "how long --wait blocks before giving up")
	cachePrefetchCmd.Flags().DurationVar(&cacheWaitInterval, "interval", 10*time.Second, "how often --wait polls")
	cacheWaitCmd.Flags().DurationVar(&cacheWaitTimeout, "timeout", 30*time.Minute, "how long to block before giving up")
	cacheWaitCmd.Flags().DurationVar(&cacheWaitInterval, "interval", 10*time.Second, "how often to poll")
	cacheEvictCmd.Flags().BoolVar(&cacheEvictConfirm, "confirm", false, "required: acknowledge that the weights will be re-downloaded")
}

var cacheWaitAfterPrefetch bool

// cacheState is the coarse, operator-facing cache state for one model.
func cacheState(model *aiv1alpha2.Model) string {
	cache := model.Status.Cache
	if cache == nil {
		return "Unknown"
	}
	if cache.Ready {
		return "Ready"
	}
	if cache.PrefetchDeferred {
		return "Deferred"
	}
	if cache.JobPhase != "" {
		return cache.JobPhase
	}
	return "Pending"
}

func runCacheStatus(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()

	k8sClient, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	listOpts := []client.ListOption{}
	if !allNs {
		listOpts = append(listOpts, client.InNamespace(namespace))
	}

	modelList := &aiv1alpha2.ModelList{}
	if err := k8sClient.List(ctx(), modelList, listOpts...); err != nil {
		return fmt.Errorf("failed to list Models: %w", err)
	}

	sort.Slice(modelList.Items, func(i, j int) bool {
		return modelList.Items[i].Name < modelList.Items[j].Name
	})

	if len(modelList.Items) == 0 {
		_, _ = fmt.Fprintln(out, "No Model resources found")
	} else {
		_, _ = fmt.Fprintf(out, "%-34s %-10s %-9s %-10s %-9s %-10s %s\n",
			"MODEL", "STRATEGY", "POLICY", "CACHE", "SIZE", "STAGED", "MESSAGE")
		_, _ = fmt.Fprintf(out, "%-34s %-10s %-9s %-10s %-9s %-10s %s\n",
			"-----", "--------", "------", "-----", "----", "------", "-------")

		for i := range modelList.Items {
			model := &modelList.Items[i]
			strategy := "-"
			size := "-"
			staged := "-"
			message := ""
			if cache := model.Status.Cache; cache != nil {
				if cache.Strategy != "" {
					strategy = cache.Strategy
				}
				if cache.SizeBytes > 0 {
					size = humanBytes(cache.SizeBytes)
				}
				if cache.LastPrefetchTime != nil {
					staged = formatAge(cache.LastPrefetchTime.Time)
				}
				message = cache.Message
			}

			_, _ = fmt.Fprintf(out, "%-34s %-10s %-9s %-10s %-9s %-10s %s\n",
				truncate(model.Name, 34),
				truncate(strategy, 10),
				truncate(model.PrefetchSpec().GetPolicy(), 9),
				truncate(cacheState(model), 10),
				size,
				staged,
				truncate(message, 44),
			)
		}
	}

	return printLegacyModelCaches(cmd, k8sClient, listOpts)
}

// printLegacyModelCaches renders the v1alpha1 ModelCache pipeline resources.
// They answer a different question from the Model table above — how an artifact
// pipeline is progressing, rather than whether a model's weights are staged —
// so both are shown. The section is skipped entirely when the cluster has no
// ModelCache resources, which is the common case on a v1alpha2-only fleet.
func printLegacyModelCaches(cmd *cobra.Command, k8sClient client.Client, listOpts []client.ListOption) error {
	out := cmd.OutOrStdout()

	cacheList := &aiv1alpha1.ModelCacheList{}
	if err := k8sClient.List(ctx(), cacheList, listOpts...); err != nil {
		return fmt.Errorf("failed to list ModelCaches: %w", err)
	}
	if len(cacheList.Items) == 0 {
		return nil
	}
	_, _ = fmt.Fprintln(out)

	sort.Slice(cacheList.Items, func(i, j int) bool {
		return cacheList.Items[i].Name < cacheList.Items[j].Name
	})

	_, _ = fmt.Fprintf(out, "%-25s %-12s %-35s %-10s %-8s %-12s %s\n",
		"MODELCACHE", "STRATEGY", "PATH", "PHASE", "FORMAT", "COMPRESSION", "SOURCE")
	_, _ = fmt.Fprintf(out, "%-25s %-12s %-35s %-10s %-8s %-12s %s\n",
		"----------", "--------", "----", "-----", "------", "-----------", "------")

	var memoryCaches []string
	for _, mc := range cacheList.Items {
		strategy := string(mc.Spec.StorageStrategy)
		if strategy == "" {
			strategy = "Auto"
		}
		if mc.Spec.StorageStrategy == aiv1alpha1.StorageStrategyMemory {
			strategy = "Memory"
			memoryCaches = append(memoryCaches, mc.Name)
		}

		path := mc.Status.Path
		if len(path) > 35 {
			path = "..." + path[len(path)-32:]
		}

		phase := string(mc.Status.Phase)
		if phase == "" {
			phase = fmt.Sprintf("%d/%d", mc.Status.ReadyNodes, mc.Status.TotalNodes)
		}

		quantFormat := "-"
		compression := "-"
		if mc.Status.Quantization != nil {
			quantFormat = mc.Status.Quantization.Format
			if mc.Status.Quantization.Type != "" {
				quantFormat = fmt.Sprintf("%s/%s", mc.Status.Quantization.Format, mc.Status.Quantization.Type)
			}
			if mc.Status.Quantization.CompressionRatio != "" {
				compression = mc.Status.Quantization.CompressionRatio + "x"
			}
		} else if mc.Spec.Quantization != nil {
			quantFormat = string(mc.Spec.Quantization.Format)
			if mc.Spec.Quantization.GGUFType != "" {
				quantFormat = fmt.Sprintf("%s/%s", mc.Spec.Quantization.Format, mc.Spec.Quantization.GGUFType)
			}
			compression = "pending"
		}

		_, _ = fmt.Fprintf(out, "%-25s %-12s %-35s %-10s %-8s %-12s %s\n",
			truncate(mc.Name, 25), strategy, path, phase, quantFormat, compression,
			truncate(mc.Spec.Source, 35),
		)
	}

	if len(memoryCaches) > 0 {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintf(out, "RAM-cached models: %s\n", strings.Join(memoryCaches, ", "))
		_, _ = fmt.Fprintln(out, "Note: RAM cache uses /dev/shm (default 50% of system RAM)")
	}

	return nil
}

func runCachePrefetch(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	name := args[0]

	k8sClient, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	model := &aiv1alpha2.Model{}
	key := types.NamespacedName{Name: name, Namespace: namespace}
	if err := k8sClient.Get(ctx(), key, model); err != nil {
		return fmt.Errorf("failed to get Model %q: %w", name, err)
	}

	requestedAt := time.Now().UTC().Format(time.RFC3339)
	patch := client.MergeFrom(model.DeepCopy())
	if model.Annotations == nil {
		model.Annotations = map[string]string{}
	}
	model.Annotations[AnnotationPrefetchRequestedAt] = requestedAt

	if err := k8sClient.Patch(ctx(), model, patch); err != nil {
		return fmt.Errorf("failed to request prefetch for %q: %w", name, err)
	}

	_, _ = fmt.Fprintf(out, "Prefetch requested for %s at %s (policy: %s)\n",
		name, requestedAt, model.PrefetchSpec().GetPolicy())

	if !cacheWaitAfterPrefetch {
		_, _ = fmt.Fprintf(out, "Track it with: flexinfer cache wait %s\n", name)
		return nil
	}
	return waitForCacheReady(cmd, k8sClient, key)
}

func runCacheWait(cmd *cobra.Command, args []string) error {
	k8sClient, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	return waitForCacheReady(cmd, k8sClient, types.NamespacedName{Name: args[0], Namespace: namespace})
}

// waitForCacheReady polls until the model's cache is staged, its staging job
// fails, or the deadline passes.
func waitForCacheReady(cmd *cobra.Command, k8sClient client.Client, key types.NamespacedName) error {
	out := cmd.OutOrStdout()
	deadline := time.Now().Add(cacheWaitTimeout)
	lastMessage := ""

	for {
		model := &aiv1alpha2.Model{}
		if err := k8sClient.Get(ctx(), key, model); err != nil {
			return fmt.Errorf("failed to get Model %q: %w", key.Name, err)
		}

		state := cacheState(model)
		message := ""
		if model.Status.Cache != nil {
			message = model.Status.Cache.Message
		}
		if message != lastMessage {
			_, _ = fmt.Fprintf(out, "[%s] %s: %s\n", time.Now().Format("15:04:05"), state, message)
			lastMessage = message
		}

		switch state {
		case "Ready":
			_, _ = fmt.Fprintf(out, "Cache ready for %s\n", key.Name)
			return nil
		case "Failed":
			return fmt.Errorf("cache staging failed for %s: %s", key.Name, message)
		case "Deferred":
			// A deferral is terminal for a wait: nothing will stage the weights
			// until something asks for them. Say so instead of blocking for the
			// full timeout.
			return fmt.Errorf("cache staging is deferred for %s: %s\nrequest it with: flexinfer cache prefetch %s",
				key.Name, message, key.Name)
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for %s cache (last state: %s)",
				cacheWaitTimeout, key.Name, state)
		}

		select {
		case <-ctx().Done():
			return fmt.Errorf("interrupted while waiting for %s cache", key.Name)
		case <-time.After(cacheWaitInterval):
		}
	}
}

func runCacheCapacity(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	now := time.Now()

	k8sClient, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	listOpts := []client.ListOption{}
	if !allNs {
		listOpts = append(listOpts, client.InNamespace(namespace))
	}

	modelList := &aiv1alpha2.ModelList{}
	if err := k8sClient.List(ctx(), modelList, listOpts...); err != nil {
		return fmt.Errorf("failed to list Models: %w", err)
	}

	pvcList := &corev1.PersistentVolumeClaimList{}
	if err := k8sClient.List(ctx(), pvcList, listOpts...); err != nil {
		return fmt.Errorf("failed to list PersistentVolumeClaims: %w", err)
	}
	pvcsByName := map[string]*corev1.PersistentVolumeClaim{}
	for i := range pvcList.Items {
		pvc := &pvcList.Items[i]
		pvcsByName[pvc.Namespace+"/"+pvc.Name] = pvc
	}

	// Group staged models by the PVC backing them.
	type pvcUsage struct {
		pvc     *corev1.PersistentVolumeClaim
		models  []string
		staged  int64
		unknown int
	}
	usage := map[string]*pvcUsage{}
	var local []string
	var candidates []string

	for i := range modelList.Items {
		model := &modelList.Items[i]
		cache := model.Status.Cache
		if cache == nil {
			continue
		}

		if evictable, over := model.PrefetchEvictable(now); evictable {
			candidates = append(candidates, fmt.Sprintf("  %-34s idle %s past its idleTTL (priority %d)",
				truncate(model.Name, 34), formatDurationShort(over), model.PrefetchSpec().GetPriority()))
		}

		if cache.PVCName == "" {
			if cache.Ready && cache.Strategy != "" && cache.Strategy != "None" {
				local = append(local, fmt.Sprintf("  %-34s %s", truncate(model.Name, 34), cache.Strategy))
			}
			continue
		}

		key := model.Namespace + "/" + cache.PVCName
		entry, ok := usage[key]
		if !ok {
			entry = &pvcUsage{pvc: pvcsByName[key]}
			usage[key] = entry
		}
		entry.models = append(entry.models, model.Name)
		if cache.SizeBytes > 0 {
			entry.staged += cache.SizeBytes
		} else if cache.Ready {
			entry.unknown++
		}
	}

	if len(usage) == 0 {
		_, _ = fmt.Fprintln(out, "No PVC-backed model caches found")
	} else {
		_, _ = fmt.Fprintf(out, "%-32s %-10s %-10s %-9s %s\n",
			"CACHE PVC", "CAPACITY", "STAGED", "MODELS", "STORAGECLASS")
		_, _ = fmt.Fprintf(out, "%-32s %-10s %-10s %-9s %s\n",
			"---------", "--------", "------", "------", "------------")

		keys := make([]string, 0, len(usage))
		for k := range usage {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			entry := usage[k]
			capacity := "-"
			storageClass := "-"
			if entry.pvc != nil {
				if qty, ok := entry.pvc.Status.Capacity[corev1.ResourceStorage]; ok {
					capacity = qty.String()
				} else if qty, ok := entry.pvc.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
					capacity = qty.String() + "*"
				}
				if entry.pvc.Spec.StorageClassName != nil {
					storageClass = *entry.pvc.Spec.StorageClassName
				}
			}
			staged := "-"
			if entry.staged > 0 {
				staged = humanBytes(entry.staged)
			}
			if entry.unknown > 0 {
				staged += fmt.Sprintf(" +%d?", entry.unknown)
			}

			name := k
			if idx := strings.Index(k, "/"); idx >= 0 {
				name = k[idx+1:]
			}
			_, _ = fmt.Fprintf(out, "%-32s %-10s %-10s %-9d %s\n",
				truncate(name, 32), capacity, staged, len(entry.models), storageClass)
		}
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintln(out, "CAPACITY is the bound volume size; * marks a request that has not bound yet.")
		_, _ = fmt.Fprintln(out, "STAGED sums status.cache.sizeBytes; '+N?' counts staged models that report no size.")
	}

	if len(local) > 0 {
		sort.Strings(local)
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintln(out, "Node-local caches (not reclaimable from this CLI):")
		for _, line := range local {
			_, _ = fmt.Fprintln(out, line)
		}
	}

	_, _ = fmt.Fprintln(out)
	if len(candidates) == 0 {
		_, _ = fmt.Fprintln(out, "No reclaim candidates: no staged cache is idle past its spec.cache.prefetch.idleTTL.")
		return nil
	}
	sort.Strings(candidates)
	_, _ = fmt.Fprintln(out, "Reclaim candidates:")
	for _, line := range candidates {
		_, _ = fmt.Fprintln(out, line)
	}
	_, _ = fmt.Fprintln(out, "\nReclaim one with: flexinfer cache evict MODEL --confirm")
	return nil
}

func runCacheEvict(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	name := args[0]

	k8sClient, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	model := &aiv1alpha2.Model{}
	if err := k8sClient.Get(ctx(), types.NamespacedName{Name: name, Namespace: namespace}, model); err != nil {
		return fmt.Errorf("failed to get Model %q: %w", name, err)
	}

	cache := model.Status.Cache
	if cache == nil || cache.PVCName == "" {
		strategy := "None"
		if cache != nil && cache.Strategy != "" {
			strategy = cache.Strategy
		}
		return fmt.Errorf("model %q has no PVC-backed cache to reclaim (strategy: %s)\n"+
			"Local and Memory caches live on the node and need a privileged pod there to clear", name, strategy)
	}

	pvc := &corev1.PersistentVolumeClaim{}
	pvcKey := types.NamespacedName{Name: cache.PVCName, Namespace: model.Namespace}
	if err := k8sClient.Get(ctx(), pvcKey, pvc); err != nil {
		return fmt.Errorf("failed to get cache PVC %q: %w", cache.PVCName, err)
	}

	if !pvcOwnedByModel(pvc, model) {
		return fmt.Errorf("refusing to evict: PVC %q was not auto-created for model %q\n"+
			"It has no controller owner reference back to this Model, so it is shared or operator-managed.\n"+
			"Delete it deliberately with kubectl if that is really what you want", cache.PVCName, name)
	}

	// A PVC can be auto-created for one model and later adopted by others via
	// spec.cache.pvcName. Deleting it would evict all of them.
	sharers, err := modelsUsingPVC(k8sClient, model.Namespace, cache.PVCName, model.Name)
	if err != nil {
		return err
	}
	if len(sharers) > 0 {
		return fmt.Errorf("refusing to evict: PVC %q also backs %s", cache.PVCName, strings.Join(sharers, ", "))
	}

	size := "unknown size"
	if cache.SizeBytes > 0 {
		size = humanBytes(cache.SizeBytes)
	}

	if !cacheEvictConfirm {
		_, _ = fmt.Fprintf(out, "Would evict cache PVC %q for model %q (%s, strategy %s).\n",
			cache.PVCName, name, size, cache.Strategy)
		_, _ = fmt.Fprintf(out, "The next activation re-downloads %s from %s.\n", name, model.Spec.Source)
		return fmt.Errorf("refusing to evict without --confirm")
	}

	if err := k8sClient.Delete(ctx(), pvc); err != nil {
		return fmt.Errorf("failed to delete cache PVC %q: %w", cache.PVCName, err)
	}

	_, _ = fmt.Fprintf(out, "Evicted cache PVC %q for model %q (%s reclaimed).\n", cache.PVCName, name, size)
	_, _ = fmt.Fprintf(out, "Re-stage it with: flexinfer cache prefetch %s\n", name)
	return nil
}

// pvcOwnedByModel reports whether the PVC was auto-created for this model,
// established by a controller owner reference back to it.
func pvcOwnedByModel(pvc *corev1.PersistentVolumeClaim, model *aiv1alpha2.Model) bool {
	for _, ref := range pvc.OwnerReferences {
		if ref.Kind != "Model" || ref.Name != model.Name {
			continue
		}
		if ref.Controller != nil && *ref.Controller {
			return true
		}
	}
	return false
}

// modelsUsingPVC returns other models in the namespace whose cache resolves to
// the same PVC.
func modelsUsingPVC(k8sClient client.Client, ns, pvcName, exclude string) ([]string, error) {
	modelList := &aiv1alpha2.ModelList{}
	if err := k8sClient.List(ctx(), modelList, client.InNamespace(ns)); err != nil {
		return nil, fmt.Errorf("failed to list Models: %w", err)
	}
	var sharers []string
	for i := range modelList.Items {
		other := &modelList.Items[i]
		if other.Name == exclude {
			continue
		}
		if other.Status.Cache != nil && other.Status.Cache.PVCName == pvcName {
			sharers = append(sharers, other.Name)
			continue
		}
		if other.Spec.Cache != nil && other.Spec.Cache.PVCName == pvcName {
			sharers = append(sharers, other.Name)
		}
	}
	sort.Strings(sharers)
	return sharers, nil
}

// humanBytes renders a byte count in the units an operator reads sizes in.
func humanBytes(b int64) string {
	if b <= 0 {
		return "-"
	}
	qty := resource.NewQuantity(b, resource.BinarySI)
	return qty.String()
}
