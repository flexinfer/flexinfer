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

//nolint:staticcheck // CLI keeps legacy v1alpha1 ModelDeployment support while v1alpha2 tooling is stabilized.
package commands

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

var statusCmd = &cobra.Command{
	Use:   "status <name>",
	Short: "Show detailed status of a ModelDeployment",
	Long: `Display detailed status information for a ModelDeployment including
conditions, GPU allocation, endpoints, and recent events.

Examples:
  # Show status of a deployment
  flexinfer status qwen3-8b-amd

  # Show status in a specific namespace
  flexinfer status qwen3-8b-amd -n my-namespace`,
	Args: cobra.ExactArgs(1),
	RunE: runStatus,
}

func runStatus(cmd *cobra.Command, args []string) error {
	fmt.Fprintln(cmd.ErrOrStderr(), "WARNING: v1alpha1 ModelDeployment is deprecated. Please migrate to v1alpha2 Model. See: flexinfer migrate generate")
	name := args[0]
	out := cmd.OutOrStdout()

	k8sClient, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	// Get the ModelDeployment
	md := &aiv1alpha1.ModelDeployment{}
	if err := k8sClient.Get(ctx(), types.NamespacedName{Name: name, Namespace: namespace}, md); err != nil {
		return fmt.Errorf("failed to get ModelDeployment %s: %w", name, err)
	}

	// Print header
	_, _ = fmt.Fprintf(out, "Name:      %s\n", md.Name)
	_, _ = fmt.Fprintf(out, "Namespace: %s\n", md.Namespace)
	_, _ = fmt.Fprintf(out, "Backend:   %s\n", md.Spec.Backend)
	_, _ = fmt.Fprintf(out, "Model:     %s\n", md.Spec.Model)
	_, _ = fmt.Fprintln(out)

	// Print spec details
	_, _ = fmt.Fprintln(out, "Spec:")
	if md.Spec.Replicas != nil {
		_, _ = fmt.Fprintf(out, "  Replicas:       %d\n", *md.Spec.Replicas)
	}
	if md.Spec.MinReplicas != nil {
		_, _ = fmt.Fprintf(out, "  Min Replicas:   %d\n", *md.Spec.MinReplicas)
	}
	if md.Spec.ModelCacheRef != nil {
		_, _ = fmt.Fprintf(out, "  Model Cache:    %s\n", *md.Spec.ModelCacheRef)
	}

	// Backend-specific config
	if md.Spec.MLCLLM != nil {
		_, _ = fmt.Fprintln(out, "  MLC-LLM Config:")
		if md.Spec.MLCLLM.Mode != "" {
			_, _ = fmt.Fprintf(out, "    Mode:         %s\n", md.Spec.MLCLLM.Mode)
		}
		if md.Spec.MLCLLM.ModelLibPath != "" {
			_, _ = fmt.Fprintf(out, "    Model Lib:    %s\n", md.Spec.MLCLLM.ModelLibPath)
		}
	}
	_, _ = fmt.Fprintln(out)

	// Serverless configuration
	if md.Spec.MinReplicas != nil && *md.Spec.MinReplicas == 0 {
		_, _ = fmt.Fprintln(out, "Serverless:")
		_, _ = fmt.Fprintf(out, "  Enabled:        Yes (minReplicas=0)\n")

		// Idle timeout
		idleTimeout := int32(300)
		if md.Spec.IdleTimeoutSeconds != nil {
			idleTimeout = *md.Spec.IdleTimeoutSeconds
		}
		_, _ = fmt.Fprintf(out, "  Idle Timeout:   %ds\n", idleTimeout)

		// Cold start timeout
		coldStartTimeout := int32(60)
		if md.Spec.ColdStartTimeoutSeconds != nil {
			coldStartTimeout = *md.Spec.ColdStartTimeoutSeconds
		}
		_, _ = fmt.Fprintf(out, "  Cold Start:     %ds max\n", coldStartTimeout)

		// Current state
		replicas := int32(1)
		if md.Spec.Replicas != nil {
			replicas = *md.Spec.Replicas
		}

		if replicas == 0 {
			_, _ = fmt.Fprintf(out, "  State:          Scaled to zero (waiting for traffic)\n")
		} else {
			_, _ = fmt.Fprintf(out, "  State:          Running (%d replica)\n", replicas)
		}

		// Idle time and scale-down prediction
		if md.Status.LastAccessTime != nil {
			idleTime := time.Since(md.Status.LastAccessTime.Time)
			_, _ = fmt.Fprintf(out, "  Last Access:    %s ago\n", formatAge(md.Status.LastAccessTime.Time))
			_, _ = fmt.Fprintf(out, "  Idle For:       %s\n", formatAge(md.Status.LastAccessTime.Time))

			if replicas > 0 {
				remainingIdle := time.Duration(idleTimeout)*time.Second - idleTime
				if remainingIdle > 0 {
					_, _ = fmt.Fprintf(out, "  Scale Down In:  ~%s\n", formatDurationShort(remainingIdle))
				} else {
					_, _ = fmt.Fprintf(out, "  Scale Down In:  imminent\n")
				}
			}
		}
		_, _ = fmt.Fprintln(out)
	}

	// Print status
	_, _ = fmt.Fprintln(out, "Status:")
	_, _ = fmt.Fprintf(out, "  Phase:          %s\n", md.Status.Phase)
	if md.Status.TokensPerSecond != "" {
		_, _ = fmt.Fprintf(out, "  Tokens/sec:     %s\n", md.Status.TokensPerSecond)
	}
	_, _ = fmt.Fprintln(out)

	// GPU allocation
	if md.Status.AllocatedGPU != nil {
		gpu := md.Status.AllocatedGPU
		_, _ = fmt.Fprintln(out, "  GPU Allocation:")
		if gpu.Node != "" {
			_, _ = fmt.Fprintf(out, "    Node:         %s\n", gpu.Node)
		}
		if gpu.Type != "" {
			_, _ = fmt.Fprintf(out, "    Type:         %s\n", gpu.Type)
		}
		if gpu.Architecture != "" {
			_, _ = fmt.Fprintf(out, "    Architecture: %s\n", gpu.Architecture)
		}
		if gpu.Vendor != "" {
			_, _ = fmt.Fprintf(out, "    Vendor:       %s\n", gpu.Vendor)
		}
		if gpu.MemoryMB > 0 {
			_, _ = fmt.Fprintf(out, "    Memory:       %d MB\n", gpu.MemoryMB)
		}
		_, _ = fmt.Fprintln(out)
	}

	// Endpoints
	if md.Status.Endpoints != nil {
		_, _ = fmt.Fprintln(out, "  Endpoints:")
		if md.Status.Endpoints.Internal != "" {
			_, _ = fmt.Fprintf(out, "    Internal:     %s\n", md.Status.Endpoints.Internal)
		}
		if md.Status.Endpoints.External != "" {
			_, _ = fmt.Fprintf(out, "    External:     %s\n", md.Status.Endpoints.External)
		}
		_, _ = fmt.Fprintln(out)
	}

	// Conditions
	if len(md.Status.Conditions) > 0 {
		_, _ = fmt.Fprintln(out, "Conditions:")
		for _, cond := range md.Status.Conditions {
			status := "Unknown"
			switch cond.Status {
			case "True":
				status = "True"
			case "False":
				status = "False"
			}
			age := formatAge(cond.LastTransitionTime.Time)
			_, _ = fmt.Fprintf(out, "  %-20s %-8s %-20s %s\n", cond.Type, status, cond.Reason, age)
			if cond.Message != "" && cond.Status == "False" {
				_, _ = fmt.Fprintf(out, "    Message: %s\n", cond.Message)
			}
		}
		_, _ = fmt.Fprintln(out)
	}

	// Get events
	clientset, err := getClientset()
	if err == nil {
		events, err := clientset.CoreV1().Events(namespace).List(ctx(), metav1.ListOptions{})
		if err == nil {
			var mdEvents []corev1.Event
			for _, event := range events.Items {
				if event.InvolvedObject.Name == name && event.InvolvedObject.Kind == "ModelDeployment" {
					mdEvents = append(mdEvents, event)
				}
			}

			if len(mdEvents) > 0 {
				_, _ = fmt.Fprintln(out, "Recent Events:")
				// Show last 5 events
				start := 0
				if len(mdEvents) > 5 {
					start = len(mdEvents) - 5
				}
				for _, event := range mdEvents[start:] {
					age := formatAge(event.LastTimestamp.Time)
					eventType := event.Type
					if eventType == "Warning" {
						eventType = "Warning"
					} else {
						eventType = "Normal"
					}
					_, _ = fmt.Fprintf(out, "  %-8s %-8s %-20s %s\n", age, eventType, event.Reason, truncate(event.Message, 60))
				}
			}
		}
	}

	return nil
}

// formatAge formats a time.Time as a human-readable age string
func formatAge(t time.Time) string {
	if t.IsZero() {
		return "-"
	}

	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// formatDurationShort formats a duration in a short human-readable format
func formatDurationShort(d time.Duration) string {
	if d < 0 {
		return "0s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}
