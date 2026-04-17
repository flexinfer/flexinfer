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
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
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
	quantValFormat string
	quantValBaseP  float64
	quantValCandP  float64
	quantValBaseA  float64
	quantValCandA  float64

	quantValArtifactPath          string
	quantValArtifactLayout        string
	quantValArtifactFamily        string
	quantValArtifactJSON          bool
	quantValArtifactRunGeneration bool
	quantValArtifactScript        string
)

var quantizeArtifactLayoutAllowed = map[string]struct{}{
	"auto":               {},
	"hf-native":          {},
	"vllm-gptq":          {},
	"compressed-tensors": {},
}

var quantizeArtifactFamilyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

var quantizeRunLocalCommandFn = func(cmd *cobra.Command, program string, args []string) error {
	localCmd := exec.CommandContext(ctx(), program, args...)
	localCmd.Stdout = cmd.OutOrStdout()
	localCmd.Stderr = cmd.ErrOrStderr()
	return localCmd.Run()
}

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

var quantizeValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate quantization quality metrics against policy thresholds",
	Args:  cobra.NoArgs,
	RunE:  runQuantizeValidate,
}

var quantizeValidateArtifactCmd = &cobra.Command{
	Use:   "validate-artifact",
	Short: "Validate a quantized artifact layout using a local validator script",
	Long: `Validate quantized artifact layout and metadata using an external validator script.

The command is model-family aware but intentionally generic so additional families
can be added without CLI changes.

Examples:
  # Auto-detect layout/family
  flexinfer quantize validate-artifact --artifact-path ./models/gemma4-26b-a4b

  # Explicit layout/family with JSON output
  flexinfer quantize validate-artifact \
    --artifact-path ./models/gemma4-31b \
    --layout vllm-gptq \
    --family gemma4-31b \
    --json`,
	Args: cobra.NoArgs,
	RunE: runQuantizeValidateArtifact,
}

func init() {
	quantizeCmd.Flags().StringVar(&quantFormat, "format", "GGUF", "Quantization format (GGUF, AWQ, GPTQ, EXL2, FP8, COMPRESSED_TENSORS)")
	quantizeCmd.Flags().StringVar(&quantType, "type", "Q4_K_M", "Quantization type (for GGUF: Q2_K, Q3_K_S, Q4_K_M, Q5_K_M, Q6_K, Q8_0)")
	quantizeCmd.Flags().Int32Var(&quantBits, "bits", 4, "Quantization bits for AWQ/GPTQ/EXL2/FP8/COMPRESSED_TENSORS formats")
	quantizeCmd.Flags().Int32Var(&quantGroupSize, "group-size", 128, "Quantization group size for AWQ/GPTQ/COMPRESSED_TENSORS formats")
	quantizeCmd.Flags().BoolVar(&quantUseGPU, "use-gpu", true, "Use GPU for quantization (required for AWQ/GPTQ/EXL2/FP8/COMPRESSED_TENSORS)")
	quantizeCmd.Flags().Int32Var(&quantMaxMemGB, "max-memory-gb", 0, "Maximum memory for quantization job in GB (0 = default)")
	quantizeRecommendCmd.Flags().BoolVar(&quantRecApply, "apply", false, "Apply the recommendation to the ModelCache spec")
	quantizeValidateCmd.Flags().StringVar(&quantValFormat, "format", "", "Quantization format (GGUF, AWQ, GPTQ, EXL2, FP8, COMPRESSED_TENSORS)")
	quantizeValidateCmd.Flags().Float64Var(&quantValBaseP, "baseline-perplexity", 0, "Baseline perplexity from reference model")
	quantizeValidateCmd.Flags().Float64Var(&quantValCandP, "candidate-perplexity", 0, "Candidate perplexity from quantized artifact")
	quantizeValidateCmd.Flags().Float64Var(&quantValBaseA, "baseline-acceptance", 0, "Baseline acceptance rate (0-1 or 0-100)")
	quantizeValidateCmd.Flags().Float64Var(&quantValCandA, "candidate-acceptance", 0, "Candidate acceptance rate (0-1 or 0-100)")
	quantizeValidateArtifactCmd.Flags().StringVar(&quantValArtifactPath, "artifact-path", "", "Path to quantized model artifact (required)")
	quantizeValidateArtifactCmd.Flags().StringVar(&quantValArtifactLayout, "layout", "auto", "Artifact layout (auto|hf-native|vllm-gptq|compressed-tensors)")
	quantizeValidateArtifactCmd.Flags().StringVar(&quantValArtifactFamily, "family", "auto", "Model family (auto, gemma4-26b-a4b, gemma4-31b, or future family id)")
	quantizeValidateArtifactCmd.Flags().BoolVar(&quantValArtifactJSON, "json", false, "Emit machine-readable JSON output from validator")
	quantizeValidateArtifactCmd.Flags().BoolVar(&quantValArtifactRunGeneration, "run-generation", false, "Run generation sanity checks during validation")
	quantizeValidateArtifactCmd.Flags().StringVar(&quantValArtifactScript, "script", "build/scripts/validate_quantized_artifact.py", "Validator script path")
	quantizeCmd.AddCommand(quantizeFormatsCmd)
	quantizeCmd.AddCommand(quantizeStatusCmd)
	quantizeCmd.AddCommand(quantizeRecommendCmd)
	quantizeCmd.AddCommand(quantizeValidateCmd)
	quantizeCmd.AddCommand(quantizeValidateArtifactCmd)
}

func runQuantizeFormats(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()

	type formatInfo struct {
		bits  string
		notes string
	}
	info := map[aiv1alpha2.QuantizationFormat]formatInfo{
		aiv1alpha2.QuantizationFormatGGUF:              {bits: "2-8", notes: "Best for consumer GPUs"},
		aiv1alpha2.QuantizationFormatAWQ:               {bits: "4", notes: "NVIDIA-focused throughput"},
		aiv1alpha2.QuantizationFormatGPTQ:              {bits: "4-8", notes: "Wide NVIDIA compatibility"},
		aiv1alpha2.QuantizationFormatEXL2:              {bits: "2-6", notes: "ExLlamaV2 optimized"},
		aiv1alpha2.QuantizationFormatFP8:               {bits: "8", notes: "Datacenter GPU optimization"},
		aiv1alpha2.QuantizationFormatCompressedTensors: {bits: "4 (W4A16)", notes: "vLLM + LLM Compressor experiments"},
	}
	order := []aiv1alpha2.QuantizationFormat{
		aiv1alpha2.QuantizationFormatGGUF,
		aiv1alpha2.QuantizationFormatAWQ,
		aiv1alpha2.QuantizationFormatGPTQ,
		aiv1alpha2.QuantizationFormatEXL2,
		aiv1alpha2.QuantizationFormatFP8,
		aiv1alpha2.QuantizationFormatCompressedTensors,
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
	format := normalizeQuantizationFormatInput(quantFormat)
	if format == "" {
		return fmt.Errorf("quantization format is required")
	}
	builder, err := quantization.GetBuilder(format)
	if err != nil {
		return fmt.Errorf("quantization format %q is not available: %w", format, err)
	}

	qType := strings.TrimSpace(quantType)
	if format == aiv1alpha2.QuantizationFormatGGUF {
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
	quantSpec := &aiv1alpha2.QuantizationSpec{
		Format: format,
	}
	effectiveBits := quantBits
	if (format == aiv1alpha2.QuantizationFormatFP8) && !cmd.Flags().Changed("bits") {
		effectiveBits = int32(quantization.DefaultFP8Bits)
	}

	if format == aiv1alpha2.QuantizationFormatGGUF {
		quantSpec.GGUFType = qType
	}
	if format == aiv1alpha2.QuantizationFormatAWQ || format == aiv1alpha2.QuantizationFormatGPTQ || format == aiv1alpha2.QuantizationFormatCompressedTensors {
		quantSpec.Bits = &effectiveBits
		quantSpec.GroupSize = &quantGroupSize
		quantSpec.UseGPU = quantUseGPU
	}
	if format == aiv1alpha2.QuantizationFormatEXL2 {
		quantSpec.Bits = &effectiveBits
		quantSpec.UseGPU = quantUseGPU
	}
	if format == aiv1alpha2.QuantizationFormatFP8 {
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
	cache.Spec.Quantization = convertQuantizationSpecV2toV1(quantSpec)
	if err := k8sClient.Patch(ctx(), cache, client.MergeFrom(original)); err != nil {
		return fmt.Errorf("failed to patch ModelCache: %w", err)
	}

	_, _ = fmt.Fprintf(out, "Quantization requested for ModelCache %q\n", cacheName)
	_, _ = fmt.Fprintf(out, "  Format: %s\n", format)
	if format == aiv1alpha2.QuantizationFormatGGUF {
		_, _ = fmt.Fprintf(out, "  Type:   %s\n", qType)
	} else if format == aiv1alpha2.QuantizationFormatEXL2 {
		_, _ = fmt.Fprintf(out, "  Type:   EXL2_B%d\n", effectiveBits)
		_, _ = fmt.Fprintf(out, "  GPU:    %t\n", quantUseGPU)
	} else if format == aiv1alpha2.QuantizationFormatFP8 {
		_, _ = fmt.Fprintf(out, "  Type:   FP8_B%d\n", effectiveBits)
		_, _ = fmt.Fprintf(out, "  GPU:    %t\n", quantUseGPU)
	} else if format == aiv1alpha2.QuantizationFormatCompressedTensors {
		_, _ = fmt.Fprintf(out, "  Type:   %s\n", quantization.CompressedTensorsType(int(effectiveBits), int(quantGroupSize)))
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
		} else if aiv1alpha2.QuantizationFormat(cache.Spec.Quantization.Format) == aiv1alpha2.QuantizationFormatEXL2 && cache.Spec.Quantization.Bits != nil {
			requested = fmt.Sprintf("%s/EXL2_B%d", requested, *cache.Spec.Quantization.Bits)
		} else if aiv1alpha2.QuantizationFormat(cache.Spec.Quantization.Format) == aiv1alpha2.QuantizationFormatFP8 && cache.Spec.Quantization.Bits != nil {
			requested = fmt.Sprintf("%s/FP8_B%d", requested, *cache.Spec.Quantization.Bits)
		} else if aiv1alpha2.QuantizationFormat(cache.Spec.Quantization.Format) == aiv1alpha2.QuantizationFormatCompressedTensors {
			bits := int32(quantization.DefaultCompressedTensorsBits)
			if cache.Spec.Quantization.Bits != nil {
				bits = *cache.Spec.Quantization.Bits
			}
			groupSize := int32(quantization.DefaultCompressedTensorsGroupSize)
			if cache.Spec.Quantization.GroupSize != nil {
				groupSize = *cache.Spec.Quantization.GroupSize
			}
			requested = fmt.Sprintf("%s/%s", requested, quantization.CompressedTensorsType(int(bits), int(groupSize)))
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
	cache.Spec.Quantization = convertQuantizationSpecV2toV1(rec.Spec)
	if err := k8sClient.Patch(ctx(), cache, client.MergeFrom(original)); err != nil {
		return fmt.Errorf("failed to patch ModelCache with recommendation: %w", err)
	}

	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintf(out, "Applied recommendation to ModelCache %q\n", cache.Name)
	return nil
}

func runQuantizeValidate(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	format := normalizeQuantizationFormatInput(quantValFormat)
	if format == "" {
		return fmt.Errorf("quantization format is required")
	}
	if quantValBaseP <= 0 || quantValCandP <= 0 {
		return fmt.Errorf("baseline-perplexity and candidate-perplexity must be > 0")
	}

	baseAcceptance, baseWasPct, err := quantization.NormalizeAcceptanceRate(quantValBaseA)
	if err != nil {
		return fmt.Errorf("invalid baseline-acceptance: %w", err)
	}
	candidateAcceptance, candidateWasPct, err := quantization.NormalizeAcceptanceRate(quantValCandA)
	if err != nil {
		return fmt.Errorf("invalid candidate-acceptance: %w", err)
	}

	eval, err := quantization.EvaluateQuality(
		format,
		quantization.QualityMetrics{
			Perplexity:     quantValBaseP,
			AcceptanceRate: baseAcceptance,
		},
		quantization.QualityMetrics{
			Perplexity:     quantValCandP,
			AcceptanceRate: candidateAcceptance,
		},
	)
	if err != nil {
		return fmt.Errorf("quality evaluation failed: %w", err)
	}

	_, _ = fmt.Fprintf(out, "Format:       %s\n", eval.Format)
	_, _ = fmt.Fprintf(out, "Policy:       perplexity<=+%.2f%% acceptance-drop<=%.2fpp\n", eval.Policy.MaxPerplexityRegressionPct, eval.Policy.MaxAcceptanceDropPct)
	_, _ = fmt.Fprintf(out, "Perplexity:   baseline=%.4f candidate=%.4f delta=%.2f%%\n", eval.Baseline.Perplexity, eval.Candidate.Perplexity, eval.PerplexityDeltaPct)
	_, _ = fmt.Fprintf(out, "Acceptance:   baseline=%.2f%% candidate=%.2f%% drop=%.2fpp\n", eval.Baseline.AcceptanceRate*100, eval.Candidate.AcceptanceRate*100, eval.AcceptanceDropPct)
	if baseWasPct || candidateWasPct {
		_, _ = fmt.Fprintln(out, "Input note:   acceptance values >1 were interpreted as percentages.")
	}
	if eval.Pass {
		_, _ = fmt.Fprintln(out, "Result:       PASS")
		return nil
	}

	_, _ = fmt.Fprintln(out, "Result:       FAIL")
	for _, check := range eval.FailedChecks {
		_, _ = fmt.Fprintf(out, "Failure:      %s\n", check)
	}
	return fmt.Errorf("quantization quality gate failed")
}

func runQuantizeValidateArtifact(cmd *cobra.Command, _ []string) error {
	artifactPath := strings.TrimSpace(quantValArtifactPath)
	if artifactPath == "" {
		return fmt.Errorf("--artifact-path is required")
	}

	layout := strings.ToLower(strings.TrimSpace(quantValArtifactLayout))
	if layout == "" {
		layout = "auto"
	}
	if _, ok := quantizeArtifactLayoutAllowed[layout]; !ok {
		return fmt.Errorf("invalid --layout %q (allowed: auto, hf-native, vllm-gptq, compressed-tensors)", layout)
	}

	family := strings.ToLower(strings.TrimSpace(quantValArtifactFamily))
	if family == "" {
		family = "auto"
	}
	if family != "auto" && !quantizeArtifactFamilyPattern.MatchString(family) {
		return fmt.Errorf("invalid --family %q (use lowercase letters, numbers, '.', '_' or '-')", family)
	}

	scriptPath := strings.TrimSpace(quantValArtifactScript)
	if scriptPath == "" {
		return fmt.Errorf("--script is required")
	}

	validatorArgs := []string{
		scriptPath,
		"--artifact-path", artifactPath,
		"--layout", layout,
		"--family", family,
	}
	if quantValArtifactJSON {
		validatorArgs = append(validatorArgs, "--json")
	}
	if quantValArtifactRunGeneration {
		validatorArgs = append(validatorArgs, "--run-generation")
	}

	if err := quantizeRunLocalCommandFn(cmd, "python3", validatorArgs); err != nil {
		return fmt.Errorf("artifact validation command failed: %w", err)
	}

	return nil
}

func quantizationSpecSummary(spec *aiv1alpha2.QuantizationSpec) string {
	if spec == nil {
		return "-"
	}
	switch spec.Format {
	case aiv1alpha2.QuantizationFormatGGUF:
		ggufType := strings.TrimSpace(spec.GGUFType)
		if ggufType == "" {
			ggufType = quantization.DefaultGGUFType
		}
		return fmt.Sprintf("GGUF/%s", ggufType)
	case aiv1alpha2.QuantizationFormatAWQ, aiv1alpha2.QuantizationFormatGPTQ:
		bits := int32(quantization.DefaultAWQBits)
		if spec.Format == aiv1alpha2.QuantizationFormatGPTQ {
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
	case aiv1alpha2.QuantizationFormatEXL2:
		bits := int32(quantization.DefaultEXL2Bits)
		if spec.Bits != nil {
			bits = *spec.Bits
		}
		return fmt.Sprintf("EXL2/EXL2_B%d", bits)
	case aiv1alpha2.QuantizationFormatFP8:
		bits := int32(quantization.DefaultFP8Bits)
		if spec.Bits != nil {
			bits = *spec.Bits
		}
		return fmt.Sprintf("FP8/FP8_B%d", bits)
	case aiv1alpha2.QuantizationFormatCompressedTensors:
		bits := int32(quantization.DefaultCompressedTensorsBits)
		if spec.Bits != nil {
			bits = *spec.Bits
		}
		group := int32(quantization.DefaultCompressedTensorsGroupSize)
		if spec.GroupSize != nil {
			group = *spec.GroupSize
		}
		return fmt.Sprintf("%s/%s", spec.Format, quantization.CompressedTensorsType(int(bits), int(group)))
	default:
		return string(spec.Format)
	}
}

func normalizeQuantizationFormatInput(raw string) aiv1alpha2.QuantizationFormat {
	s := strings.TrimSpace(raw)
	s = strings.ReplaceAll(s, "-", "_")
	return aiv1alpha2.QuantizationFormat(strings.ToUpper(s))
}
