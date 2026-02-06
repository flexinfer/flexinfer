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
	name := args[0]

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
	fmt.Printf("Name:      %s\n", md.Name)
	fmt.Printf("Namespace: %s\n", md.Namespace)
	fmt.Printf("Backend:   %s\n", md.Spec.Backend)
	fmt.Printf("Model:     %s\n", md.Spec.Model)
	fmt.Println()

	// Print spec details
	fmt.Println("Spec:")
	if md.Spec.Replicas != nil {
		fmt.Printf("  Replicas:       %d\n", *md.Spec.Replicas)
	}
	if md.Spec.MinReplicas != nil {
		fmt.Printf("  Min Replicas:   %d\n", *md.Spec.MinReplicas)
	}
	if md.Spec.ModelCacheRef != nil {
		fmt.Printf("  Model Cache:    %s\n", *md.Spec.ModelCacheRef)
	}

	// Backend-specific config
	if md.Spec.MLCLLM != nil {
		fmt.Println("  MLC-LLM Config:")
		if md.Spec.MLCLLM.Mode != "" {
			fmt.Printf("    Mode:         %s\n", md.Spec.MLCLLM.Mode)
		}
		if md.Spec.MLCLLM.ModelLibPath != "" {
			fmt.Printf("    Model Lib:    %s\n", md.Spec.MLCLLM.ModelLibPath)
		}
	}
	fmt.Println()

	// Serverless configuration
	if md.Spec.MinReplicas != nil && *md.Spec.MinReplicas == 0 {
		fmt.Println("Serverless:")
		fmt.Printf("  Enabled:        Yes (minReplicas=0)\n")

		// Idle timeout
		idleTimeout := int32(300)
		if md.Spec.IdleTimeoutSeconds != nil {
			idleTimeout = *md.Spec.IdleTimeoutSeconds
		}
		fmt.Printf("  Idle Timeout:   %ds\n", idleTimeout)

		// Cold start timeout
		coldStartTimeout := int32(60)
		if md.Spec.ColdStartTimeoutSeconds != nil {
			coldStartTimeout = *md.Spec.ColdStartTimeoutSeconds
		}
		fmt.Printf("  Cold Start:     %ds max\n", coldStartTimeout)

		// Current state
		replicas := int32(1)
		if md.Spec.Replicas != nil {
			replicas = *md.Spec.Replicas
		}

		if replicas == 0 {
			fmt.Printf("  State:          Scaled to zero (waiting for traffic)\n")
		} else {
			fmt.Printf("  State:          Running (%d replica)\n", replicas)
		}

		// Idle time and scale-down prediction
		if md.Status.LastAccessTime != nil {
			idleTime := time.Since(md.Status.LastAccessTime.Time)
			fmt.Printf("  Last Access:    %s ago\n", formatAge(md.Status.LastAccessTime.Time))
			fmt.Printf("  Idle For:       %s\n", formatAge(md.Status.LastAccessTime.Time))

			if replicas > 0 {
				remainingIdle := time.Duration(idleTimeout)*time.Second - idleTime
				if remainingIdle > 0 {
					fmt.Printf("  Scale Down In:  ~%s\n", formatDurationShort(remainingIdle))
				} else {
					fmt.Printf("  Scale Down In:  imminent\n")
				}
			}
		}
		fmt.Println()
	}

	// Print status
	fmt.Println("Status:")
	fmt.Printf("  Phase:          %s\n", md.Status.Phase)
	if md.Status.TokensPerSecond != "" {
		fmt.Printf("  Tokens/sec:     %s\n", md.Status.TokensPerSecond)
	}
	fmt.Println()

	// GPU allocation
	if md.Status.AllocatedGPU != nil {
		gpu := md.Status.AllocatedGPU
		fmt.Println("  GPU Allocation:")
		if gpu.Node != "" {
			fmt.Printf("    Node:         %s\n", gpu.Node)
		}
		if gpu.Type != "" {
			fmt.Printf("    Type:         %s\n", gpu.Type)
		}
		if gpu.Architecture != "" {
			fmt.Printf("    Architecture: %s\n", gpu.Architecture)
		}
		if gpu.Vendor != "" {
			fmt.Printf("    Vendor:       %s\n", gpu.Vendor)
		}
		if gpu.MemoryMB > 0 {
			fmt.Printf("    Memory:       %d MB\n", gpu.MemoryMB)
		}
		fmt.Println()
	}

	// Endpoints
	if md.Status.Endpoints != nil {
		fmt.Println("  Endpoints:")
		if md.Status.Endpoints.Internal != "" {
			fmt.Printf("    Internal:     %s\n", md.Status.Endpoints.Internal)
		}
		if md.Status.Endpoints.External != "" {
			fmt.Printf("    External:     %s\n", md.Status.Endpoints.External)
		}
		fmt.Println()
	}

	// Conditions
	if len(md.Status.Conditions) > 0 {
		fmt.Println("Conditions:")
		for _, cond := range md.Status.Conditions {
			status := "Unknown"
			switch cond.Status {
			case "True":
				status = "True"
			case "False":
				status = "False"
			}
			age := formatAge(cond.LastTransitionTime.Time)
			fmt.Printf("  %-20s %-8s %-20s %s\n", cond.Type, status, cond.Reason, age)
			if cond.Message != "" && cond.Status == "False" {
				fmt.Printf("    Message: %s\n", cond.Message)
			}
		}
		fmt.Println()
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
				fmt.Println("Recent Events:")
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
					fmt.Printf("  %-8s %-8s %-20s %s\n", age, eventType, event.Reason, truncate(event.Message, 60))
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
