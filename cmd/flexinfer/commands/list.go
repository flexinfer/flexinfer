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
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls", "get"},
	Short:   "List ModelDeployments",
	Long: `List all ModelDeployment resources in the current namespace.

Examples:
  # List deployments in default namespace
  flexinfer list

  # List deployments in all namespaces
  flexinfer list -A

  # List deployments in a specific namespace
  flexinfer list -n my-namespace`,
	RunE: runList,
}

func runList(cmd *cobra.Command, args []string) error {
	k8sClient, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	var mdList aiv1alpha1.ModelDeploymentList
	listOpts := []client.ListOption{}
	if ns := getNamespace(); ns != "" {
		listOpts = append(listOpts, client.InNamespace(ns))
	}

	if err := k8sClient.List(ctx(), &mdList, listOpts...); err != nil {
		return fmt.Errorf("failed to list ModelDeployments: %w", err)
	}

	if len(mdList.Items) == 0 {
		fmt.Println("No ModelDeployments found")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if allNs {
		fmt.Fprintln(w, "NAMESPACE\tNAME\tBACKEND\tMODEL\tSTATUS\tTPS\tGPU")
	} else {
		fmt.Fprintln(w, "NAME\tBACKEND\tMODEL\tSTATUS\tTPS\tGPU")
	}

	for _, md := range mdList.Items {
		// Extract GPU info
		gpu := "-"
		if md.Status.AllocatedGPU != nil {
			gpuType := md.Status.AllocatedGPU.Type
			if gpuType == "" {
				gpuType = "GPU"
			}
			arch := md.Status.AllocatedGPU.Architecture
			vendor := md.Status.AllocatedGPU.Vendor
			if arch != "" {
				gpu = fmt.Sprintf("%s (%s)", gpuType, arch)
			} else if vendor != "" {
				gpu = fmt.Sprintf("%s (%s)", gpuType, vendor)
			} else {
				gpu = gpuType
			}
		}

		// Status
		status := string(md.Status.Phase)
		if status == "" {
			status = "Unknown"
		}

		// TPS
		tps := md.Status.TokensPerSecond
		if tps == "" {
			tps = "-"
		} else {
			// Format TPS with suffix
			tps = fmt.Sprintf("%s/s", tps)
		}

		// Model (truncate if too long)
		model := truncate(md.Spec.Model, 35)

		if allNs {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				md.Namespace, md.Name, md.Spec.Backend, model, status, tps, gpu)
		} else {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
				md.Name, md.Spec.Backend, model, status, tps, gpu)
		}
	}

	return w.Flush()
}

// truncate shortens a string if it exceeds maxLen
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
