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
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

var migrateAll bool

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migrate v1alpha1 ModelDeployments to v1alpha2 Models",
	Long: `Tools for migrating from deprecated v1alpha1 ModelDeployment resources
to the v1alpha2 Model API.

Subcommands:
  list      - List all v1alpha1 ModelDeployments with migration status
  generate  - Generate v1alpha2 Model YAML from a ModelDeployment`,
}

var migrateListCmd = &cobra.Command{
	Use:   "list",
	Short: "List v1alpha1 ModelDeployments with migration status",
	RunE:  runMigrateList,
}

var migrateGenerateCmd = &cobra.Command{
	Use:   "generate [name]",
	Short: "Generate v1alpha2 Model YAML from a v1alpha1 ModelDeployment",
	Long: `Generates v1alpha2 Model YAML to stdout. Use --all to generate for all
ModelDeployments. Pipe to a file or kubectl apply.

Examples:
  flexinfer migrate generate my-model > my-model-v1alpha2.yaml
  flexinfer migrate generate --all > all-models-v1alpha2.yaml`,
	RunE: runMigrateGenerate,
}

func init() {
	migrateCmd.AddCommand(migrateListCmd)
	migrateCmd.AddCommand(migrateGenerateCmd)
	migrateGenerateCmd.Flags().BoolVar(&migrateAll, "all", false, "Generate YAML for all ModelDeployments")
}

func runMigrateList(cmd *cobra.Command, args []string) error {
	k8sClient, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to create k8s client: %w", err)
	}

	ns := getNamespace()

	var mds aiv1alpha1.ModelDeploymentList
	listOpts := []client.ListOption{}
	if ns != "" {
		listOpts = append(listOpts, client.InNamespace(ns))
	}
	if err := k8sClient.List(ctx(), &mds, listOpts...); err != nil {
		return fmt.Errorf("failed to list ModelDeployments: %w", err)
	}

	if len(mds.Items) == 0 {
		fmt.Println("No v1alpha1 ModelDeployments found.")
		return nil
	}

	// Check which already have a v1alpha2 Model counterpart
	var models aiv1alpha2.ModelList
	if err := k8sClient.List(ctx(), &models, listOpts...); err != nil {
		return fmt.Errorf("failed to list Models: %w", err)
	}
	existingModels := make(map[string]bool, len(models.Items))
	for _, m := range models.Items {
		existingModels[m.Name] = true
	}

	fmt.Printf("%-30s %-12s %-20s %-10s %-12s\n", "NAME", "NAMESPACE", "BACKEND/MODEL", "PHASE", "MIGRATED")
	fmt.Printf("%-30s %-12s %-20s %-10s %-12s\n", "----", "---------", "-------------", "-----", "--------")

	for _, md := range mds.Items {
		migrated := "No"
		if existingModels[md.Name] {
			migrated = "Yes"
		}

		model := md.Spec.Model
		if len(model) > 17 {
			model = model[:17] + "..."
		}
		label := fmt.Sprintf("%s/%s", md.Spec.Backend, model)
		if len(label) > 20 {
			label = label[:20]
		}

		phase := string(md.Status.Phase)
		if phase == "" {
			phase = "Unknown"
		}

		fmt.Printf("%-30s %-12s %-20s %-10s %-12s\n",
			md.Name, md.Namespace, label, phase, migrated)
	}

	fmt.Printf("\nTotal: %d ModelDeployments\n", len(mds.Items))
	return nil
}

func runMigrateGenerate(cmd *cobra.Command, args []string) error {
	if !migrateAll && len(args) == 0 {
		return fmt.Errorf("specify a ModelDeployment name or use --all")
	}

	k8sClient, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to create k8s client: %w", err)
	}

	ns := getNamespace()

	var deployments []aiv1alpha1.ModelDeployment

	if migrateAll {
		var mds aiv1alpha1.ModelDeploymentList
		listOpts := []client.ListOption{}
		if ns != "" {
			listOpts = append(listOpts, client.InNamespace(ns))
		}
		if err := k8sClient.List(ctx(), &mds, listOpts...); err != nil {
			return fmt.Errorf("failed to list ModelDeployments: %w", err)
		}
		deployments = mds.Items
	} else {
		for _, name := range args {
			md := &aiv1alpha1.ModelDeployment{}
			key := client.ObjectKey{Name: name, Namespace: ns}
			if ns == "" {
				key.Namespace = "flexinfer-system"
			}
			if err := k8sClient.Get(ctx(), key, md); err != nil {
				return fmt.Errorf("failed to get ModelDeployment %q: %w", name, err)
			}
			deployments = append(deployments, *md)
		}
	}

	if len(deployments) == 0 {
		fmt.Fprintln(os.Stderr, "No ModelDeployments found.")
		return nil
	}

	for i, md := range deployments {
		model, err := ConvertModelDeploymentToModel(&md)
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: failed to convert %s: %v\n", md.Name, err)
			continue
		}

		out, err := yaml.Marshal(model)
		if err != nil {
			return fmt.Errorf("failed to marshal YAML for %s: %w", md.Name, err)
		}

		if i > 0 {
			fmt.Println("---")
		}
		fmt.Print(string(out))
	}

	return nil
}

// ConvertModelDeploymentToModel converts a v1alpha1 ModelDeployment to a v1alpha2 Model.
// Exported for testing.
func ConvertModelDeploymentToModel(md *aiv1alpha1.ModelDeployment) (*aiv1alpha2.Model, error) {
	model := &aiv1alpha2.Model{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "ai.flexinfer/v1alpha2",
			Kind:       "Model",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      md.Name,
			Namespace: md.Namespace,
		},
	}

	model.Spec.Backend = md.Spec.Backend
	model.Spec.Source = InferSource(md.Spec.Backend, md.Spec.Model)

	if gpu := BuildGPUSpec(md); gpu != nil {
		model.Spec.GPU = gpu
	}

	if serverless := BuildServerlessSpec(md); serverless != nil {
		model.Spec.Serverless = serverless
	}

	if md.Spec.ModelCacheRef != nil && *md.Spec.ModelCacheRef != "" {
		model.Spec.Cache = &aiv1alpha2.CacheSpec{
			PVCName: *md.Spec.ModelCacheRef,
		}
	}

	if config := BuildConfigMap(md); config != nil {
		raw, err := json.Marshal(config)
		if err != nil {
			return nil, fmt.Errorf("marshaling config: %w", err)
		}
		model.Spec.Config = &apiextensionsv1.JSON{Raw: raw}
	}

	model.Spec.Resources = md.Spec.Resources
	model.Spec.NodeSelector = md.Spec.NodeSelector
	model.Spec.Tolerations = md.Spec.Tolerations
	model.Spec.ServiceLabels = md.Spec.ServiceLabels

	if md.Spec.LiteLLM != nil {
		model.Spec.LiteLLM = &aiv1alpha2.LiteLLMSpec{
			Enabled:         md.Spec.LiteLLM.Enabled,
			ServedModelName: md.Spec.LiteLLM.ServedModelName,
			Aliases:         md.Spec.LiteLLM.Aliases,
			CopilotAlias:    md.Spec.LiteLLM.CopilotAlias,
		}
	}

	return model, nil
}

// InferSource adds a backend-appropriate URI prefix to the model identifier.
// Exported for testing.
func InferSource(backend, model string) string {
	switch strings.ToLower(backend) {
	case "ollama":
		return "ollama://" + model
	case "vllm", "vllm-omni":
		if strings.Contains(model, "/") {
			return "HF://" + model
		}
		return "HF://" + model
	case "llamacpp", "llama.cpp":
		if strings.HasPrefix(model, "/") {
			return "file://" + model
		}
		if strings.Contains(model, "/") {
			return "HF://" + model
		}
		return model
	case "diffusers", "comfyui":
		if strings.Contains(model, "/") {
			return "HF://" + model
		}
		return model
	default:
		return model
	}
}

// BuildGPUSpec constructs the v1alpha2 GPUSpec from v1alpha1 fields.
// Exported for testing.
func BuildGPUSpec(md *aiv1alpha1.ModelDeployment) *aiv1alpha2.GPUSpec {
	gpu := &aiv1alpha2.GPUSpec{}
	hasFields := false

	if md.Spec.GPUGroupRef != nil && *md.Spec.GPUGroupRef != "" {
		gpu.Shared = *md.Spec.GPUGroupRef
		hasFields = true
	}

	if md.Spec.Priority != nil {
		gpu.Priority = md.Spec.Priority
		hasFields = true
	}

	if md.Spec.VRAMEstimateMB != nil {
		gpu.VRAMEstimateMB = md.Spec.VRAMEstimateMB
		hasFields = true
	}

	if !hasFields {
		return nil
	}
	return gpu
}

// BuildServerlessSpec constructs the v1alpha2 ServerlessSpec from v1alpha1 fields.
// Exported for testing.
func BuildServerlessSpec(md *aiv1alpha1.ModelDeployment) *aiv1alpha2.ServerlessSpec {
	s := &aiv1alpha2.ServerlessSpec{}
	hasFields := false

	if md.Spec.MinReplicas != nil {
		s.MinReplicas = md.Spec.MinReplicas
		hasFields = true
	}

	if md.Spec.IdleTimeoutSeconds != nil {
		d := metav1.Duration{Duration: time.Duration(*md.Spec.IdleTimeoutSeconds) * time.Second}
		s.IdleTimeout = &d
		hasFields = true
	}

	if md.Spec.ColdStartTimeoutSeconds != nil {
		d := metav1.Duration{Duration: time.Duration(*md.Spec.ColdStartTimeoutSeconds) * time.Second}
		s.ColdStartTimeout = &d
		hasFields = true
	}

	if !hasFields {
		return nil
	}
	return s
}

// BuildConfigMap converts backend-specific specs to a generic config map.
// Exported for testing.
func BuildConfigMap(md *aiv1alpha1.ModelDeployment) map[string]any {
	switch {
	case md.Spec.VLLM != nil:
		return vllmSpecToConfig(md.Spec.VLLM)
	case md.Spec.LlamaCpp != nil:
		return llamaCppSpecToConfig(md.Spec.LlamaCpp)
	case md.Spec.MLCLLM != nil:
		return mlcllmSpecToConfig(md.Spec.MLCLLM)
	case md.Spec.ComfyUI != nil:
		return comfyUISpecToConfig(md.Spec.ComfyUI)
	case md.Spec.VLLMOmni != nil:
		return vllmOmniSpecToConfig(md.Spec.VLLMOmni)
	default:
		return nil
	}
}

func vllmSpecToConfig(v *aiv1alpha1.VLLMSpec) map[string]any {
	cfg := map[string]any{}
	if v.TensorParallelSize != nil {
		cfg["tensorParallelSize"] = *v.TensorParallelSize
	}
	if v.Dtype != "" {
		cfg["dtype"] = v.Dtype
	}
	if v.Quantization != "" {
		cfg["quantization"] = v.Quantization
	}
	if v.MaxModelLen != nil {
		cfg["maxModelLen"] = *v.MaxModelLen
	}
	if v.GPUMemoryUtilization != nil {
		cfg["gpuMemoryUtilization"] = *v.GPUMemoryUtilization
	}
	if v.EnforceEager != nil {
		cfg["enforceEager"] = *v.EnforceEager
	}
	if v.MaxNumSeqs != nil {
		cfg["maxNumSeqs"] = *v.MaxNumSeqs
	}
	if v.SwapSpace != nil {
		cfg["swapSpace"] = *v.SwapSpace
	}
	if v.TrustRemoteCode != nil {
		cfg["trustRemoteCode"] = *v.TrustRemoteCode
	}
	if v.HIPVisibleDevices != "" {
		cfg["hipVisibleDevices"] = v.HIPVisibleDevices
	}
	if v.ROCRVisibleDevices != "" {
		cfg["rocrVisibleDevices"] = v.ROCRVisibleDevices
	}
	if v.GPUDeviceOrdinal != "" {
		cfg["gpuDeviceOrdinal"] = v.GPUDeviceOrdinal
	}
	if v.EnablePrefixCaching != nil {
		cfg["enablePrefixCaching"] = *v.EnablePrefixCaching
	}
	if v.KVCacheDtype != "" {
		cfg["kvCacheDtype"] = v.KVCacheDtype
	}
	if v.CalculateKVScales != nil {
		cfg["calculateKvScales"] = *v.CalculateKVScales
	}
	if v.AttentionBackend != "" {
		cfg["attentionBackend"] = v.AttentionBackend
	}
	if v.CPUOffloadGB != nil {
		cfg["cpuOffloadGB"] = *v.CPUOffloadGB
	}
	if v.EnableChunkedPrefill != nil {
		cfg["enableChunkedPrefill"] = *v.EnableChunkedPrefill
	}
	if v.BlockSize != nil {
		cfg["blockSize"] = *v.BlockSize
	}
	if v.RopeScaling != nil {
		rs := map[string]any{}
		if v.RopeScaling.Type != "" {
			rs["type"] = v.RopeScaling.Type
		}
		if v.RopeScaling.Factor != "" {
			rs["factor"] = v.RopeScaling.Factor
		}
		cfg["ropeScaling"] = rs
	}
	if len(cfg) == 0 {
		return nil
	}
	return cfg
}

func llamaCppSpecToConfig(l *aiv1alpha1.LlamaCppSpec) map[string]any {
	cfg := map[string]any{}
	if l.ContextSize != nil {
		cfg["contextSize"] = *l.ContextSize
	}
	if l.NGPULayers != nil {
		cfg["nGPULayers"] = *l.NGPULayers
	}
	if l.BatchSize != nil {
		cfg["batchSize"] = *l.BatchSize
	}
	if l.Threads != nil {
		cfg["threads"] = *l.Threads
	}
	if l.FlashAttention != nil {
		cfg["flashAttention"] = *l.FlashAttention
	}
	if l.MainGPU != nil {
		cfg["mainGPU"] = *l.MainGPU
	}
	if l.RopeFreqBase != "" {
		cfg["ropeFreqBase"] = l.RopeFreqBase
	}
	if l.RopeFreqScale != "" {
		cfg["ropeFreqScale"] = l.RopeFreqScale
	}
	if len(cfg) == 0 {
		return nil
	}
	return cfg
}

func mlcllmSpecToConfig(m *aiv1alpha1.MLCLLMSpec) map[string]any {
	cfg := map[string]any{}
	if m.Mode != "" {
		cfg["mode"] = m.Mode
	}
	if m.ModelLibPath != "" {
		cfg["modelLibPath"] = m.ModelLibPath
	}
	if m.GPUMemoryBytes != nil {
		cfg["gpuMemoryBytes"] = *m.GPUMemoryBytes
	}
	if m.JITPolicy != "" {
		cfg["jitPolicy"] = m.JITPolicy
	}
	if m.Overrides != nil {
		overrides := map[string]any{}
		if m.Overrides.PrefillChunkSize != nil {
			overrides["prefillChunkSize"] = *m.Overrides.PrefillChunkSize
		}
		if m.Overrides.MaxTotalSeqLength != nil {
			overrides["maxTotalSeqLength"] = *m.Overrides.MaxTotalSeqLength
		}
		if m.Overrides.MaxNumSequence != nil {
			overrides["maxNumSequence"] = *m.Overrides.MaxNumSequence
		}
		if m.Overrides.GPUMemoryUtilization != "" {
			overrides["gpuMemoryUtilization"] = m.Overrides.GPUMemoryUtilization
		}
		if m.Overrides.ContextWindowSize != nil {
			overrides["contextWindowSize"] = *m.Overrides.ContextWindowSize
		}
		if m.Overrides.Raw != "" {
			overrides["raw"] = m.Overrides.Raw
		}
		if len(overrides) > 0 {
			cfg["overrides"] = overrides
		}
	}
	if m.CompileOptions != nil {
		co := map[string]any{}
		if m.CompileOptions.UseCutlass != nil {
			co["useCutlass"] = *m.CompileOptions.UseCutlass
		}
		if m.CompileOptions.UseFlashInfer != nil {
			co["useFlashInfer"] = *m.CompileOptions.UseFlashInfer
		}
		if m.CompileOptions.UseCublasGemm != nil {
			co["useCublasGemm"] = *m.CompileOptions.UseCublasGemm
		}
		if m.CompileOptions.UseCudaGraph != nil {
			co["useCudaGraph"] = *m.CompileOptions.UseCudaGraph
		}
		if len(co) > 0 {
			cfg["compileOptions"] = co
		}
	}
	if len(cfg) == 0 {
		return nil
	}
	return cfg
}

func comfyUISpecToConfig(c *aiv1alpha1.ComfyUISpec) map[string]any {
	cfg := map[string]any{}
	if c.WorkflowsPath != "" {
		cfg["workflowsPath"] = c.WorkflowsPath
	}
	if c.ModelsPath != "" {
		cfg["modelsPath"] = c.ModelsPath
	}
	if c.CustomNodesPath != "" {
		cfg["customNodesPath"] = c.CustomNodesPath
	}
	if len(c.PreloadModels) > 0 {
		cfg["preloadModels"] = c.PreloadModels
	}
	if c.EnableCORS != nil {
		cfg["enableCORS"] = *c.EnableCORS
	}
	if len(c.ExtraArgs) > 0 {
		cfg["extraArgs"] = c.ExtraArgs
	}
	if len(cfg) == 0 {
		return nil
	}
	return cfg
}

func vllmOmniSpecToConfig(v *aiv1alpha1.VLLMOmniSpec) map[string]any {
	cfg := map[string]any{}
	if v.DiffusionModel != "" {
		cfg["diffusionModel"] = v.DiffusionModel
	}
	if v.CacheAcceleration != "" {
		cfg["cacheAcceleration"] = v.CacheAcceleration
	}
	if v.DefaultSize != "" {
		cfg["defaultSize"] = v.DefaultSize
	}
	if v.GPUMemoryUtilization != nil {
		cfg["gpuMemoryUtilization"] = *v.GPUMemoryUtilization
	}
	if v.MaxNumSeqs != nil {
		cfg["maxNumSeqs"] = *v.MaxNumSeqs
	}
	if len(cfg) == 0 {
		return nil
	}
	return cfg
}
