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
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

var (
	quantFormat   string
	quantType     string
	quantMaxMemGB int32
)

var quantizeCmd = &cobra.Command{
	Use:   "quantize <cache-name>",
	Short: "Quantize a cached model",
	Long: `Trigger quantization of a ModelCache resource.

This patches the ModelCache with quantization settings, causing the controller
to create a quantization job after the download completes. The model transitions
through Provisioning → Quantizing → Ready.

Examples:
  # Quantize a model to GGUF Q4_K_M (default)
  flexinfer quantize llama3-8b

  # Quantize with a specific GGUF type
  flexinfer quantize llama3-8b --format GGUF --type Q5_K_M

  # Quantize with custom memory limit
  flexinfer quantize llama3-70b --format GGUF --type Q4_K_M --max-memory-gb 64`,
	Args: cobra.ExactArgs(1),
	RunE: runQuantize,
}

func init() {
	quantizeCmd.Flags().StringVar(&quantFormat, "format", "GGUF", "Quantization format (GGUF, AWQ, GPTQ, EXL2, FP8)")
	quantizeCmd.Flags().StringVar(&quantType, "type", "Q4_K_M", "Quantization type (for GGUF: Q2_K, Q3_K_S, Q4_K_M, Q5_K_M, Q6_K, Q8_0)")
	quantizeCmd.Flags().Int32Var(&quantMaxMemGB, "max-memory-gb", 0, "Maximum memory for quantization job in GB (0 = default)")
}

func runQuantize(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	cacheName := args[0]

	k8sClient, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	// Fetch the ModelCache
	cache := &aiv1alpha1.ModelCache{}
	key := client.ObjectKey{Name: cacheName, Namespace: namespace}
	if err := k8sClient.Get(ctx(), key, cache); err != nil {
		return fmt.Errorf("failed to get ModelCache %q: %w", cacheName, err)
	}

	// Build the quantization spec patch
	quantSpec := &aiv1alpha1.QuantizationSpec{
		Format:   aiv1alpha1.QuantizationFormat(quantFormat),
		GGUFType: quantType,
	}
	if quantMaxMemGB > 0 {
		quantSpec.MaxMemoryGB = &quantMaxMemGB
	}

	// Apply patch
	patch := cache.DeepCopy()
	patch.Spec.Quantization = quantSpec

	patchData, err := json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{
			"quantization": patch.Spec.Quantization,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to marshal patch: %w", err)
	}

	if err := k8sClient.Patch(ctx(), cache, client.RawPatch(client.MergeFrom(cache).Type(), patchData)); err != nil {
		return fmt.Errorf("failed to patch ModelCache: %w", err)
	}

	_, _ = fmt.Fprintf(out, "Quantization requested for ModelCache %q\n", cacheName)
	_, _ = fmt.Fprintf(out, "  Format: %s\n", quantFormat)
	_, _ = fmt.Fprintf(out, "  Type:   %s\n", quantType)
	if quantMaxMemGB > 0 {
		_, _ = fmt.Fprintf(out, "  Memory: %dGB\n", quantMaxMemGB)
	}
	_, _ = fmt.Fprintf(out, "  Phase:  %s\n", cache.Status.Phase)
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintf(out, "Watch progress: flexinfer cache status -n %s\n", namespace)

	return nil
}
