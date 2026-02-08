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
	"text/tabwriter"
	"time"

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
	out := cmd.OutOrStdout()

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
		_, _ = fmt.Fprintln(out, "No ModelDeployments found")
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if allNs {
		_, _ = fmt.Fprintln(w, "NAMESPACE\tNAME\tBACKEND\tSTATUS\tREPLICAS\tIDLE\tTPS")
	} else {
		_, _ = fmt.Fprintln(w, "NAME\tBACKEND\tSTATUS\tREPLICAS\tIDLE\tTPS")
	}

	for _, md := range mdList.Items {
		// Status with serverless indicator
		status := string(md.Status.Phase)
		if status == "" {
			status = "Unknown"
		}

		// Check if scaled to zero (serverless state)
		replicas := int32(1)
		minReplicas := int32(0)
		if md.Spec.Replicas != nil {
			replicas = *md.Spec.Replicas
		}
		if md.Spec.MinReplicas != nil {
			minReplicas = *md.Spec.MinReplicas
		}

		// Build replicas string
		replicasStr := fmt.Sprintf("%d", replicas)
		if minReplicas == 0 {
			// Serverless mode - show current/max
			replicasStr = fmt.Sprintf("%d (0→1)", replicas)
			if replicas == 0 {
				status = "Scaled(0)"
			}
		}

		// Calculate idle time
		idleStr := "-"
		if md.Status.LastAccessTime != nil {
			idleTime := time.Since(md.Status.LastAccessTime.Time)
			idleStr = formatDuration(idleTime)
		}

		// TPS
		tps := md.Status.TokensPerSecond
		if tps == "" {
			tps = "-"
		} else {
			// Format TPS with suffix
			tps = fmt.Sprintf("%s/s", tps)
		}

		if allNs {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				md.Namespace, md.Name, md.Spec.Backend, status, replicasStr, idleStr, tps)
		} else {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
				md.Name, md.Spec.Backend, status, replicasStr, idleStr, tps)
		}
	}

	return w.Flush()
}

// formatDuration formats a duration in human-readable format
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// truncate shortens a string if it exceeds maxLen
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
