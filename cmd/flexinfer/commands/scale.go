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
	"strconv"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

var scaleCmd = &cobra.Command{
	Use:   "scale <name> <replicas>",
	Short: "Scale a ModelDeployment",
	Long: `Scale a ModelDeployment to the specified number of replicas.

Examples:
  # Scale to 3 replicas
  flexinfer scale qwen3-8b-amd 3

  # Scale to zero (serverless)
  flexinfer scale qwen3-8b-amd 0`,
	Args: cobra.ExactArgs(2),
	RunE: runScale,
}

func runScale(cmd *cobra.Command, args []string) error {
	name := args[0]
	replicasStr := args[1]
	out := cmd.OutOrStdout()

	replicas, err := strconv.ParseInt(replicasStr, 10, 32)
	if err != nil {
		return fmt.Errorf("invalid replicas value: %w", err)
	}

	if replicas < 0 {
		return fmt.Errorf("replicas must be >= 0")
	}

	k8sClient, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	// Get the ModelDeployment
	md := &aiv1alpha1.ModelDeployment{}
	if err := k8sClient.Get(ctx(), types.NamespacedName{Name: name, Namespace: namespace}, md); err != nil {
		return fmt.Errorf("ModelDeployment %s not found: %w", name, err)
	}

	// Update replicas
	oldReplicas := int32(1)
	if md.Spec.Replicas != nil {
		oldReplicas = *md.Spec.Replicas
	}

	newReplicas := int32(replicas)
	md.Spec.Replicas = &newReplicas

	if err := k8sClient.Update(ctx(), md); err != nil {
		return fmt.Errorf("failed to update ModelDeployment: %w", err)
	}

	_, _ = fmt.Fprintf(out, "ModelDeployment %s scaled: %d -> %d replicas\n", name, oldReplicas, newReplicas)
	return nil
}
