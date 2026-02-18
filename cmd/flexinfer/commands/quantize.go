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
	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	"github.com/flexinfer/flexinfer/pkg/quantization"
)

var (
	quantFormat    string
	quantType      string
	quantBits      int32
	quantGroupSize int32
	quantUseGPU    bool
	quantMaxMemGB  int32
	quantRecApply  bool
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

var quantizeFormatsCmd = &cobra.Command{
	Use:   "formats",
	Short: "List quantization formats and backend compatibility",
	Args:  cobra.NoArgs,
	RunE:  runQuantizeFormats,
}

var quantizeStatusCmd = &cobra.Command{
	Use:   "status <cache-name>",
	Short: "Show quantization status for a ModelCache",
	Args:  cobra.ExactArgs(1),
	RunE:  runQuantizeStatus,
}

var quantizeRecommendCmd = &cobra.Command{
	Use:   "recommend <cache-name>",
	Short: "Recommend quantization settings from model and GPU constraints",
	Args:  cobra.ExactArgs(1),
	RunE:  runQuantizeRecommend,
}

func init() {
	quantizeCmd.Flags().StringVar(&quantFormat, "format", "GGUF", "Quantization format (GGUF, AWQ, GPTQ, EXL2, FP8)")
	quantizeCmd.Flags().StringVar(&quantType, "type", "Q4_K_M", "Quantization type (for GGUF: Q2_K, Q3_K_S, Q4_K_M, Q5_K_M, Q6_K, Q8_0)")
	quantizeCmd.Flags().Int32Var(&quantBits, "bits", 4, "Quantization bits for AWQ/GPTQ/EXL2/FP8 formats")
	quantizeCmd.Flags().Int32Var(&quantGroupSize, "group-size", 128, "Quantization group size for AWQ/GPTQ formats")
	quantizeCmd.Flags().BoolVar(&quantUseGPU, "use-gpu", true, "Use GPU for quantization (required for AWQ/GPTQ/EXL2/FP8)")
	quantizeCmd.Flags().Int32Var(&quantMaxMemGB, "max-memory-gb", 0, "Maximum memory for quantization job in GB (0 = default)")
	quantizeRecommendCmd.Flags().BoolVar(&quantRecApply, "apply", false, "Apply the recommendation to the ModelCache spec")
	quantizeCmd.AddCommand(quantizeFormatsCmd)
	quantizeCmd.AddCommand(quantizeStatusCmd)
	quantizeCmd.AddCommand(quantizeRecommendCmd)
}

func runQuantizeFormats(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()

	type formatInfo struct {
		bits  string
		notes string
	}
	info := map[aiv1alpha1.QuantizationFormat]formatInfo{
		aiv1alpha1.QuantizationFormatGGUF: {bits: "2-8", notes: "Best for consumer GPUs"},
		aiv1alpha1.QuantizationFormatAWQ:  {bits: "4", notes: "NVIDIA-focused throughput"},
		aiv1alpha1.QuantizationFormatGPTQ: {bits: "4-8", notes: "Wide NVIDIA compatibility"},
		aiv1alpha1.QuantizationFormatEXL2: {bits: "2-6", notes: "ExLlamaV2 optimized"},
		aiv1alpha1.QuantizationFormatFP8:  {bits: "8", notes: "Datacenter GPU optimization"},
	}
	order := []aiv1alpha1.QuantizationFormat{
		aiv1alpha1.QuantizationFormatGGUF,
		aiv1alpha1.QuantizationFormatAWQ,
		aiv1alpha1.QuantizationFormatGPTQ,
		aiv1alpha1.QuantizationFormatEXL2,
		aiv1alpha1.QuantizationFormatFP8,
	}

	_, _ = fmt.Fprintf(out, "%-8s %-6s %-24s %-11s %s\n", "FORMAT", "BITS", "BACKENDS", "STATUS", "NOTES")
	_, _ = fmt.Fprintf(out, "%-8s %-6s %-24s %-11s %s\n", "------", "----", "--------", "------", "-----")

	for _, format := range order {
		compatible := append([]string(nil), quantization.FormatBackendCompatibility[format]...)
		sort.Strings(compatible)

		status := "planned"
		if _, err := quantization.GetBuilder(format); err == nil {
			status = "implemented"
		}

		details := info[format]
		_, _ = fmt.Fprintf(
			out, "%-8s %-6s %-24s %-11s %s\n",
			string(format),
			details.bits,
			strings.Join(compatible, ","),
			status,
			details.notes,
		)
	}

	return nil
}

func runQuantize(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	cacheName := args[0]
	format := aiv1alpha1.QuantizationFormat(strings.ToUpper(strings.TrimSpace(quantFormat)))
	if format == "" {
		return fmt.Errorf("quantization format is required")
	}
	builder, err := quantization.GetBuilder(format)
	if err != nil {
		return fmt.Errorf("quantization format %q is not available: %w", format, err)
	}

	qType := strings.TrimSpace(quantType)
	if format == aiv1alpha1.QuantizationFormatGGUF {
		if qType == "" {
			qType = quantization.DefaultGGUFType
		} else {
			qType = strings.ToUpper(qType)
		}
		if !quantization.IsValidGGUFType(qType) {
			return fmt.Errorf("invalid GGUF type %q", qType)
		}
	}

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
		Format: format,
	}
	effectiveBits := quantBits
	if (format == aiv1alpha1.QuantizationFormatFP8) && !cmd.Flags().Changed("bits") {
		effectiveBits = int32(quantization.DefaultFP8Bits)
	}

	if format == aiv1alpha1.QuantizationFormatGGUF {
		quantSpec.GGUFType = qType
	}
	if format == aiv1alpha1.QuantizationFormatAWQ || format == aiv1alpha1.QuantizationFormatGPTQ {
		quantSpec.Bits = &effectiveBits
		quantSpec.GroupSize = &quantGroupSize
		quantSpec.UseGPU = quantUseGPU
	}
	if format == aiv1alpha1.QuantizationFormatEXL2 {
		quantSpec.Bits = &effectiveBits
		quantSpec.UseGPU = quantUseGPU
	}
	if format == aiv1alpha1.QuantizationFormatFP8 {
		quantSpec.Bits = &effectiveBits
		quantSpec.UseGPU = quantUseGPU
	}
	if quantMaxMemGB > 0 {
		quantSpec.MaxMemoryGB = &quantMaxMemGB
	}
	if err := builder.Validate(quantSpec); err != nil {
		return fmt.Errorf("invalid quantization configuration: %w", err)
	}

	// Apply patch
	original := cache.DeepCopy()
	cache.Spec.Quantization = quantSpec
	if err := k8sClient.Patch(ctx(), cache, client.MergeFrom(original)); err != nil {
		return fmt.Errorf("failed to patch ModelCache: %w", err)
	}

	_, _ = fmt.Fprintf(out, "Quantization requested for ModelCache %q\n", cacheName)
	_, _ = fmt.Fprintf(out, "  Format: %s\n", format)
	if format == aiv1alpha1.QuantizationFormatGGUF {
		_, _ = fmt.Fprintf(out, "  Type:   %s\n", qType)
	} else if format == aiv1alpha1.QuantizationFormatEXL2 {
		_, _ = fmt.Fprintf(out, "  Type:   EXL2_B%d\n", effectiveBits)
		_, _ = fmt.Fprintf(out, "  GPU:    %t\n", quantUseGPU)
	} else if format == aiv1alpha1.QuantizationFormatFP8 {
		_, _ = fmt.Fprintf(out, "  Type:   FP8_B%d\n", effectiveBits)
		_, _ = fmt.Fprintf(out, "  GPU:    %t\n", quantUseGPU)
	} else {
		_, _ = fmt.Fprintf(out, "  Type:   W%d_G%d\n", effectiveBits, quantGroupSize)
		_, _ = fmt.Fprintf(out, "  GPU:    %t\n", quantUseGPU)
	}
	if quantMaxMemGB > 0 {
		_, _ = fmt.Fprintf(out, "  Memory: %dGB\n", quantMaxMemGB)
	}
	_, _ = fmt.Fprintf(out, "  Phase:  %s\n", cache.Status.Phase)
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintf(out, "Watch progress: flexinfer cache status -n %s\n", namespace)

	return nil
}

func runQuantizeStatus(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	cacheName := args[0]

	k8sClient, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	cache := &aiv1alpha1.ModelCache{}
	key := client.ObjectKey{Name: cacheName, Namespace: namespace}
	if err := k8sClient.Get(ctx(), key, cache); err != nil {
		return fmt.Errorf("failed to get ModelCache %q: %w", cacheName, err)
	}

	phase := string(cache.Status.Phase)
	if phase == "" {
		phase = "Unknown"
	}

	requested := "-"
	if cache.Spec.Quantization != nil {
		requested = string(cache.Spec.Quantization.Format)
		if cache.Spec.Quantization.GGUFType != "" {
			requested = fmt.Sprintf("%s/%s", requested, cache.Spec.Quantization.GGUFType)
		} else if cache.Spec.Quantization.Format == aiv1alpha1.QuantizationFormatEXL2 && cache.Spec.Quantization.Bits != nil {
			requested = fmt.Sprintf("%s/EXL2_B%d", requested, *cache.Spec.Quantization.Bits)
		} else if cache.Spec.Quantization.Format == aiv1alpha1.QuantizationFormatFP8 && cache.Spec.Quantization.Bits != nil {
			requested = fmt.Sprintf("%s/FP8_B%d", requested, *cache.Spec.Quantization.Bits)
		} else if cache.Spec.Quantization.Bits != nil && cache.Spec.Quantization.GroupSize != nil {
			requested = fmt.Sprintf("%s/W%d_G%d", requested, *cache.Spec.Quantization.Bits, *cache.Spec.Quantization.GroupSize)
		}
	}

	_, _ = fmt.Fprintf(out, "ModelCache: %s\n", cache.Name)
	_, _ = fmt.Fprintf(out, "Namespace:  %s\n", cache.Namespace)
	_, _ = fmt.Fprintf(out, "Phase:      %s\n", phase)
	_, _ = fmt.Fprintf(out, "Requested:  %s\n", requested)
	job := &batchv1.Job{}
	jobKey := client.ObjectKey{Name: cacheName + "-quantize", Namespace: namespace}
	if err := k8sClient.Get(ctx(), jobKey, job); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get quantization job %q: %w", jobKey.Name, err)
		}
	} else {
		_, _ = fmt.Fprintf(
			out,
			"Job:        %s (active=%d succeeded=%d failed=%d)\n",
			job.Name,
			job.Status.Active,
			job.Status.Succeeded,
			job.Status.Failed,
		)
	}

	if cache.Status.Quantization == nil {
		_, _ = fmt.Fprintln(out, "Quantization: pending")
		return nil
	}

	q := cache.Status.Quantization
	applied := q.Format
	if q.Type != "" {
		applied = fmt.Sprintf("%s/%s", q.Format, q.Type)
	}
	_, _ = fmt.Fprintf(out, "Applied:    %s\n", applied)
	if q.OriginalSizeBytes > 0 {
		_, _ = fmt.Fprintf(out, "Original:   %d bytes\n", q.OriginalSizeBytes)
	}
	if q.CompressedSizeBytes > 0 {
		_, _ = fmt.Fprintf(out, "Compressed: %d bytes\n", q.CompressedSizeBytes)
	}
	if q.CompressionRatio != "" {
		_, _ = fmt.Fprintf(out, "Ratio:      %sx\n", q.CompressionRatio)
	}
	if q.QuantizationTime != "" {
		_, _ = fmt.Fprintf(out, "Duration:   %s\n", q.QuantizationTime)
	}

	return nil
}

func runQuantizeRecommend(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	cacheName := args[0]

	k8sClient, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	cache := &aiv1alpha1.ModelCache{}
	key := client.ObjectKey{Name: cacheName, Namespace: namespace}
	if err := k8sClient.Get(ctx(), key, cache); err != nil {
		return fmt.Errorf("failed to get ModelCache %q: %w", cacheName, err)
	}

	rec := quantization.RecommendSpec(quantization.RecommendationInput{
		Source:       cache.Spec.Source,
		NodeSelector: cache.Spec.NodeSelector,
	})
	if rec.Spec == nil {
		return fmt.Errorf("no recommendation available for %q", cacheName)
	}

	builder, err := quantization.GetBuilder(rec.Spec.Format)
	if err != nil {
		return fmt.Errorf("recommended format %q is not available: %w", rec.Spec.Format, err)
	}
	if err := builder.Validate(rec.Spec); err != nil {
		return fmt.Errorf("recommended configuration is invalid: %w", err)
	}

	_, _ = fmt.Fprintf(out, "ModelCache:   %s\n", cache.Name)
	_, _ = fmt.Fprintf(out, "Namespace:    %s\n", cache.Namespace)
	_, _ = fmt.Fprintf(out, "Recommended:  %s\n", quantizationSpecSummary(rec.Spec))
	if rec.GPUVendor != "" && rec.GPUVendor != "Unknown" {
		if rec.GPUArchitecture != "" {
			_, _ = fmt.Fprintf(out, "GPU target:   %s/%s\n", rec.GPUVendor, rec.GPUArchitecture)
		} else {
			_, _ = fmt.Fprintf(out, "GPU target:   %s\n", rec.GPUVendor)
		}
	}
	if rec.HasModelSizeEstimate {
		_, _ = fmt.Fprintf(out, "Model hint:   %.1fB\n", rec.ModelSizeBillions)
	}
	_, _ = fmt.Fprintf(out, "Reason:       %s\n", rec.Reason)

	if !quantRecApply {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintf(out, "Apply with: flexinfer quantize recommend %s --apply -n %s\n", cache.Name, namespace)
		return nil
	}

	original := cache.DeepCopy()
	cache.Spec.Quantization = rec.Spec
	if err := k8sClient.Patch(ctx(), cache, client.MergeFrom(original)); err != nil {
		return fmt.Errorf("failed to patch ModelCache with recommendation: %w", err)
	}

	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintf(out, "Applied recommendation to ModelCache %q\n", cache.Name)
	return nil
}

func quantizationSpecSummary(spec *aiv1alpha1.QuantizationSpec) string {
	if spec == nil {
		return "-"
	}
	switch spec.Format {
	case aiv1alpha1.QuantizationFormatGGUF:
		ggufType := strings.TrimSpace(spec.GGUFType)
		if ggufType == "" {
			ggufType = quantization.DefaultGGUFType
		}
		return fmt.Sprintf("GGUF/%s", ggufType)
	case aiv1alpha1.QuantizationFormatAWQ, aiv1alpha1.QuantizationFormatGPTQ:
		bits := int32(quantization.DefaultAWQBits)
		if spec.Format == aiv1alpha1.QuantizationFormatGPTQ {
			bits = int32(quantization.DefaultGPTQBits)
		}
		if spec.Bits != nil {
			bits = *spec.Bits
		}
		group := int32(quantization.DefaultQuantizationGroupSize)
		if spec.GroupSize != nil {
			group = *spec.GroupSize
		}
		return fmt.Sprintf("%s/W%d_G%d", spec.Format, bits, group)
	case aiv1alpha1.QuantizationFormatEXL2:
		bits := int32(quantization.DefaultEXL2Bits)
		if spec.Bits != nil {
			bits = *spec.Bits
		}
		return fmt.Sprintf("EXL2/EXL2_B%d", bits)
	case aiv1alpha1.QuantizationFormatFP8:
		bits := int32(quantization.DefaultFP8Bits)
		if spec.Bits != nil {
			bits = *spec.Bits
		}
		return fmt.Sprintf("FP8/FP8_B%d", bits)
	default:
		return string(spec.Format)
	}
}
