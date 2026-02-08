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

	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

var cacheCmd = &cobra.Command{
	Use:   "cache",
	Short: "Manage model caches",
	Long: `Commands for managing ModelCache resources.

ModelCache resources define how model weights are stored and distributed
across cluster nodes. Strategies include:
  - SharedPVC: Shared storage using a ReadWriteMany PVC
  - NodeLocal: Local disk cache on each GPU node
  - Memory: RAM cache using /dev/shm for faster loading`,
}

var cacheStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show status of all model caches",
	Long: `Display status of ModelCache resources including storage strategy,
path, and ready state.

Examples:
  # Show all model caches
  flexinfer cache status

  # Show caches in a specific namespace
  flexinfer cache status -n my-namespace`,
	RunE: runCacheStatus,
}

func init() {
	cacheCmd.AddCommand(cacheStatusCmd)
}

func runCacheStatus(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()

	k8sClient, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	// List ModelCaches
	cacheList := &aiv1alpha1.ModelCacheList{}
	listOpts := []client.ListOption{}
	if !allNs {
		listOpts = append(listOpts, client.InNamespace(namespace))
	}

	if err := k8sClient.List(ctx(), cacheList, listOpts...); err != nil {
		return fmt.Errorf("failed to list ModelCaches: %w", err)
	}

	if len(cacheList.Items) == 0 {
		_, _ = fmt.Fprintln(out, "No ModelCache resources found")
		return nil
	}

	// Sort by name
	sort.Slice(cacheList.Items, func(i, j int) bool {
		return cacheList.Items[i].Name < cacheList.Items[j].Name
	})

	// Print header
	_, _ = fmt.Fprintf(out, "%-25s %-12s %-45s %-8s %s\n", "NAME", "STRATEGY", "PATH", "READY", "SOURCE")
	_, _ = fmt.Fprintf(out, "%-25s %-12s %-45s %-8s %s\n", "----", "--------", "----", "-----", "------")

	for _, mc := range cacheList.Items {
		strategy := string(mc.Spec.StorageStrategy)
		if strategy == "" {
			strategy = "Auto"
		}

		// Highlight Memory strategy
		if mc.Spec.StorageStrategy == aiv1alpha1.StorageStrategyMemory {
			strategy = "Memory"
		}

		path := mc.Status.Path
		if len(path) > 45 {
			path = "..." + path[len(path)-42:]
		}

		ready := fmt.Sprintf("%d/%d", mc.Status.ReadyNodes, mc.Status.TotalNodes)
		switch mc.Status.Phase {
		case aiv1alpha1.ModelCachePhaseReady:
			ready = "Ready"
		case aiv1alpha1.ModelCachePhaseFailed:
			ready = "Failed"
		}

		source := truncate(mc.Spec.Source, 40)

		_, _ = fmt.Fprintf(out, "%-25s %-12s %-45s %-8s %s\n",
			truncate(mc.Name, 25),
			strategy,
			path,
			ready,
			source,
		)
	}

	// Print summary for Memory caches
	var memoryCaches []string
	for _, mc := range cacheList.Items {
		if mc.Spec.StorageStrategy == aiv1alpha1.StorageStrategyMemory {
			memoryCaches = append(memoryCaches, mc.Name)
		}
	}

	if len(memoryCaches) > 0 {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintf(out, "RAM-cached models: %s\n", strings.Join(memoryCaches, ", "))
		_, _ = fmt.Fprintln(out, "Note: RAM cache uses /dev/shm (default 50% of system RAM)")
	}

	return nil
}
