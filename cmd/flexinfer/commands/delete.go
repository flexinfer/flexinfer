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

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

var forceDelete bool

var deleteCmd = &cobra.Command{
	Use:     "delete <name>",
	Aliases: []string{"rm"},
	Short:   "Delete a ModelDeployment",
	Long: `Delete a ModelDeployment and its associated resources.

Examples:
  # Delete a deployment
  flexinfer delete qwen3-8b-amd

  # Delete without confirmation
  flexinfer delete qwen3-8b-amd --force`,
	Args: cobra.ExactArgs(1),
	RunE: runDelete,
}

func init() {
	deleteCmd.Flags().BoolVar(&forceDelete, "force", false, "Skip confirmation")
}

func runDelete(cmd *cobra.Command, args []string) error {
	name := args[0]

	k8sClient, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	// Get the ModelDeployment first to confirm it exists
	md := &aiv1alpha1.ModelDeployment{}
	if err := k8sClient.Get(ctx(), types.NamespacedName{Name: name, Namespace: namespace}, md); err != nil {
		return fmt.Errorf("ModelDeployment %s not found: %w", name, err)
	}

	// Confirm deletion
	if !forceDelete {
		fmt.Printf("Delete ModelDeployment %s/%s? [y/N]: ", namespace, name)
		var response string
		_, _ = fmt.Scanln(&response)
		if response != "y" && response != "Y" {
			fmt.Println("Deletion cancelled")
			return nil
		}
	}

	// Delete the ModelDeployment
	if err := k8sClient.Delete(ctx(), md); err != nil {
		return fmt.Errorf("failed to delete ModelDeployment: %w", err)
	}

	fmt.Printf("ModelDeployment %s deleted\n", name)
	return nil
}
