// Package quantization — finetune job builder.
// Finetuning runs LoRA/QLoRA or full finetuning via Unsloth after abliteration
// (if configured) and before quantization. The finetuned weights are saved
// in-place on the PVC so the quantization step sees the updated model.
package quantization

import (
	"fmt"
	"os"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

const (
	// DefaultFinetuneMemoryGB is the default memory limit for finetune jobs.
	DefaultFinetuneMemoryGB = 56

	// DefaultFinetuneDeadlineSeconds is the default 6-hour deadline.
	DefaultFinetuneDeadlineSeconds = 21600

	// DefaultFinetuneEpochs is the default number of training epochs.
	DefaultFinetuneEpochs = 3

	// DefaultFinetuneBatchSize is the default per-device training batch size.
	DefaultFinetuneBatchSize = 4

	// DefaultFinetuneLearningRate is the default learning rate.
	DefaultFinetuneLearningRate = "2e-4"

	// DefaultFinetuneMaxSeqLen is the default max sequence length for training.
	DefaultFinetuneMaxSeqLen = 2048

	// DefaultFinetuneLoRARank is the default LoRA rank.
	DefaultFinetuneLoRARank = 16

	// DefaultFinetuneLoRAAlpha is the default LoRA alpha.
	DefaultFinetuneLoRAAlpha = 32
)

// BuildFinetuneJob creates a Kubernetes Job that finetunes model weights on the PVC.
func BuildFinetuneJob(params JobParams, spec *aiv1alpha1.FinetuneSpec) (*batchv1.Job, error) {
	if spec == nil {
		return nil, fmt.Errorf("finetune spec is nil")
	}

	memoryGB := int32(DefaultFinetuneMemoryGB)
	if spec.MaxMemoryGB != nil && *spec.MaxMemoryGB > 0 {
		memoryGB = *spec.MaxMemoryGB
	}

	deadline := int64(DefaultFinetuneDeadlineSeconds)
	if spec.TimeoutSeconds != nil && *spec.TimeoutSeconds >= 300 {
		deadline = *spec.TimeoutSeconds
	}

	image := finetuneImage(params.GPUVendor, params.GPUArch)
	if params.ProfileQuantizerImage != "" {
		image = params.ProfileQuantizerImage
	}
	script := buildFinetuneScript(params.ModelPath, spec)

	backoffLimit := int32(2)
	pvcVol, pvcMount := modelPVCVolume(params.PVCName)
	wsVol, wsMount := workspaceVolume(fmt.Sprintf("%dGi", memoryGB*2))

	volumes := []corev1.Volume{pvcVol, wsVol}
	mounts := []corev1.VolumeMount{pvcMount, wsMount}

	// Mount dataset PVC if specified.
	if spec.Dataset.PVCName != nil && *spec.Dataset.PVCName != "" {
		dsVol := corev1.Volume{
			Name: "dataset",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: *spec.Dataset.PVCName,
					ReadOnly:  true,
				},
			},
		}
		dsMount := corev1.VolumeMount{
			Name:      "dataset",
			MountPath: "/datasets",
			ReadOnly:  true,
		}
		if spec.Dataset.PVCSubPath != nil && *spec.Dataset.PVCSubPath != "" {
			dsMount.SubPath = *spec.Dataset.PVCSubPath
		}
		volumes = append(volumes, dsVol)
		mounts = append(mounts, dsMount)
	}

	useGPU := spec.UseGPU == nil || *spec.UseGPU // default true
	var env []corev1.EnvVar
	resources := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(fmt.Sprintf("%d", DefaultGPUQuantizationCPU)),
			corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dGi", memoryGB)),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dGi", memoryGB)),
		},
	}

	if useGPU {
		gpuResourceName := "nvidia.com/gpu"
		if params.GPUVendor == "amd" {
			gpuResourceName = "amd.com/gpu"
			env = append(env, corev1.EnvVar{
				Name:  "PYTORCH_HIP_ALLOC_CONF",
				Value: "expandable_segments:True",
			})
		}
		gpuResource := corev1.ResourceName(gpuResourceName)
		resources.Requests[gpuResource] = resource.MustParse("1")
		resources.Limits[gpuResource] = resource.MustParse("1")
	}

	podSpec := corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyNever,
		Containers: []corev1.Container{
			{
				Name:            "finetuner",
				Image:           image,
				ImagePullPolicy: corev1.PullAlways,
				Command:         []string{"/bin/bash", "-c"},
				Args:            []string{script},
				Env:             env,
				VolumeMounts:    mounts,
				Resources:       resources,
			},
		},
		Volumes: volumes,
	}

	if len(params.NodeSelector) > 0 {
		podSpec.NodeSelector = params.NodeSelector
	}
	if len(params.Tolerations) > 0 {
		podSpec.Tolerations = params.Tolerations
	}

	jobMeta := metav1.ObjectMeta{
		Name:      fmt.Sprintf("%s-finetune", params.Name),
		Namespace: params.Namespace,
		Labels: map[string]string{
			"app.kubernetes.io/managed-by": "flexinfer",
			"flexinfer.ai/component":       "finetuner",
			"flexinfer.ai/cache":           params.Name,
		},
	}

	return &batchv1.Job{
		ObjectMeta: jobMeta,
		Spec: batchv1.JobSpec{
			ActiveDeadlineSeconds: &deadline,
			BackoffLimit:          &backoffLimit,
			Template: corev1.PodTemplateSpec{
				Spec: podSpec,
			},
		},
	}, nil
}

// finetuneImage returns the container image for finetune jobs.
func finetuneImage(gpuVendor, gpuArch string) string {
	if img := os.Getenv("FLEXINFER_FINETUNE_IMAGE"); img != "" {
		return img
	}
	// Fall back to the GPTQ quantizer image which has transformers + torch + accelerate.
	// Unsloth-specific images will be built separately per GPU arch.
	if gpuVendor == "amd" {
		return gptqQuantizerROCmImage(gpuArch)
	}
	return gptqQuantizerImage()
}

// buildFinetuneScript generates the shell+Python script that performs finetuning.
func buildFinetuneScript(modelPath string, spec *aiv1alpha1.FinetuneSpec) string {
	mode := "qlora"
	if spec.Mode != nil {
		mode = string(*spec.Mode)
	}

	epochs := int32(DefaultFinetuneEpochs)
	if spec.Epochs != nil && *spec.Epochs > 0 {
		epochs = *spec.Epochs
	}

	batchSize := int32(DefaultFinetuneBatchSize)
	if spec.BatchSize != nil && *spec.BatchSize > 0 {
		batchSize = *spec.BatchSize
	}

	lr := DefaultFinetuneLearningRate
	if spec.LearningRate != nil && *spec.LearningRate != "" {
		lr = *spec.LearningRate
	}

	maxSeqLen := int32(DefaultFinetuneMaxSeqLen)
	if spec.MaxSeqLen != nil && *spec.MaxSeqLen > 0 {
		maxSeqLen = *spec.MaxSeqLen
	}

	loraRank := int32(DefaultFinetuneLoRARank)
	loraAlpha := int32(DefaultFinetuneLoRAAlpha)
	loraDropout := "0.05"
	var targetModules string
	if spec.LoRA != nil {
		if spec.LoRA.Rank != nil && *spec.LoRA.Rank > 0 {
			loraRank = *spec.LoRA.Rank
		}
		if spec.LoRA.Alpha != nil {
			loraAlpha = *spec.LoRA.Alpha
		}
		if spec.LoRA.Dropout != nil && *spec.LoRA.Dropout != "" {
			loraDropout = *spec.LoRA.Dropout
		}
		if len(spec.LoRA.TargetModules) > 0 {
			targetModules = strings.Join(spec.LoRA.TargetModules, ",")
		}
	}

	mergeAdapter := "true"
	if spec.MergeAdapter != nil && !*spec.MergeAdapter {
		mergeAdapter = "false"
	}

	gradCheckpoint := "true"
	if spec.GradientCheckpointing != nil && !*spec.GradientCheckpointing {
		gradCheckpoint = "false"
	}

	// Dataset source: HF dataset ID or local PVC path.
	datasetSource := ""
	if spec.Dataset.HuggingFace != nil && *spec.Dataset.HuggingFace != "" {
		datasetSource = *spec.Dataset.HuggingFace
	}

	datasetSplit := "train"
	if spec.Dataset.Split != nil && *spec.Dataset.Split != "" {
		datasetSplit = *spec.Dataset.Split
	}

	maxSamples := "0" // 0 = all
	if spec.Dataset.MaxSamples != nil && *spec.Dataset.MaxSamples > 0 {
		maxSamples = fmt.Sprintf("%d", *spec.Dataset.MaxSamples)
	}

	// Dataset PVC path (mounted at /datasets).
	datasetPVCPath := ""
	if spec.Dataset.PVCName != nil && *spec.Dataset.PVCName != "" {
		datasetPVCPath = "/datasets"
	}

	return fmt.Sprintf(`set -euo pipefail

MODEL_DIR="/cache/%s"
MODE="%s"
EPOCHS=%d
BATCH_SIZE=%d
LEARNING_RATE="%s"
MAX_SEQ_LEN=%d
LORA_RANK=%d
LORA_ALPHA=%d
LORA_DROPOUT="%s"
TARGET_MODULES="%s"
MERGE_ADAPTER="%s"
GRAD_CHECKPOINT="%s"
DATASET_SOURCE="%s"
DATASET_SPLIT="%s"
MAX_SAMPLES=%s
DATASET_PVC_PATH="%s"

echo "=== FlexInfer Finetune ==="
echo "Model: ${MODEL_DIR}"
echo "Mode: ${MODE}"
echo "Epochs: ${EPOCHS}"
echo "Batch size: ${BATCH_SIZE}"
echo "LR: ${LEARNING_RATE}"
echo "Max seq len: ${MAX_SEQ_LEN}"
echo "LoRA rank: ${LORA_RANK}, alpha: ${LORA_ALPHA}"
echo "Merge adapter: ${MERGE_ADAPTER}"
echo "Gradient checkpointing: ${GRAD_CHECKPOINT}"
echo "Dataset: ${DATASET_SOURCE:-$DATASET_PVC_PATH}"
echo "Start: $(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ)"

START_TS=$(date +%%s)

# Install unsloth if not already present.
pip install --no-cache-dir --quiet "unsloth[cu124-ampere-torch250]" 2>/dev/null || \
pip install --no-cache-dir --quiet unsloth 2>/dev/null || \
echo "WARN: unsloth install failed, falling back to transformers SFTTrainer"

python3 << 'PYEOF'
import json, os, sys, time

model_dir = os.environ["MODEL_DIR"]
mode = os.environ["MODE"]
epochs = int(os.environ["EPOCHS"])
batch_size = int(os.environ["BATCH_SIZE"])
lr = float(os.environ["LEARNING_RATE"])
max_seq_len = int(os.environ["MAX_SEQ_LEN"])
lora_rank = int(os.environ["LORA_RANK"])
lora_alpha = int(os.environ["LORA_ALPHA"])
lora_dropout = float(os.environ["LORA_DROPOUT"])
target_modules_str = os.environ["TARGET_MODULES"]
merge_adapter = os.environ["MERGE_ADAPTER"] == "true"
grad_checkpoint = os.environ["GRAD_CHECKPOINT"] == "true"
dataset_source = os.environ["DATASET_SOURCE"]
dataset_split = os.environ["DATASET_SPLIT"]
max_samples = int(os.environ["MAX_SAMPLES"])
dataset_pvc_path = os.environ["DATASET_PVC_PATH"]

print(f"Loading model from {model_dir}...")
load_start = time.time()

# Try Unsloth first, fall back to transformers.
use_unsloth = False
try:
    from unsloth import FastLanguageModel
    model, tokenizer = FastLanguageModel.from_pretrained(
        model_name=model_dir,
        max_seq_length=max_seq_len,
        load_in_4bit=(mode == "qlora"),
    )
    use_unsloth = True
    print(f"Model loaded via Unsloth in {time.time() - load_start:.1f}s")
except Exception as e:
    print(f"Unsloth load failed ({e}), falling back to transformers")
    import torch
    from transformers import AutoModelForCausalLM, AutoTokenizer
    model = AutoModelForCausalLM.from_pretrained(
        model_dir,
        torch_dtype=torch.bfloat16,
        device_map="auto",
        trust_remote_code=True,
    )
    tokenizer = AutoTokenizer.from_pretrained(model_dir, trust_remote_code=True)
    print(f"Model loaded via transformers in {time.time() - load_start:.1f}s")

if tokenizer.pad_token is None:
    tokenizer.pad_token = tokenizer.eos_token

# Apply LoRA/QLoRA adapters (skip for full finetune).
if mode in ("lora", "qlora"):
    target_modules = target_modules_str.split(",") if target_modules_str else None
    if use_unsloth:
        model = FastLanguageModel.get_peft_model(
            model,
            r=lora_rank,
            lora_alpha=lora_alpha,
            lora_dropout=lora_dropout,
            target_modules=target_modules or [
                "q_proj", "k_proj", "v_proj", "o_proj",
                "gate_proj", "up_proj", "down_proj",
            ],
            use_gradient_checkpointing=("unsloth" if grad_checkpoint else False),
        )
    else:
        from peft import get_peft_model, LoraConfig, TaskType
        peft_config = LoraConfig(
            r=lora_rank,
            lora_alpha=lora_alpha,
            lora_dropout=lora_dropout,
            target_modules=target_modules or [
                "q_proj", "k_proj", "v_proj", "o_proj",
                "gate_proj", "up_proj", "down_proj",
            ],
            task_type=TaskType.CAUSAL_LM,
        )
        model = get_peft_model(model, peft_config)
    model.print_trainable_parameters()

# Load dataset.
from datasets import load_dataset, load_from_disk
if dataset_pvc_path and os.path.isdir(dataset_pvc_path):
    print(f"Loading dataset from PVC: {dataset_pvc_path}")
    try:
        dataset = load_from_disk(dataset_pvc_path)
        if isinstance(dataset, dict) and dataset_split in dataset:
            dataset = dataset[dataset_split]
    except Exception:
        dataset = load_dataset("json", data_dir=dataset_pvc_path, split=dataset_split)
elif dataset_source:
    print(f"Loading HF dataset: {dataset_source} (split={dataset_split})")
    dataset = load_dataset(dataset_source, split=dataset_split)
else:
    print("ERROR: No dataset source specified")
    sys.exit(1)

if max_samples > 0 and len(dataset) > max_samples:
    dataset = dataset.select(range(max_samples))
    print(f"Truncated dataset to {max_samples} samples")
print(f"Dataset size: {len(dataset)} samples")

# Configure trainer.
from transformers import TrainingArguments
from trl import SFTTrainer

output_dir = "/workspace/finetune-output"
training_args = TrainingArguments(
    output_dir=output_dir,
    num_train_epochs=epochs,
    per_device_train_batch_size=batch_size,
    learning_rate=lr,
    gradient_checkpointing=grad_checkpoint,
    logging_steps=10,
    save_strategy="epoch",
    bf16=True,
    optim="adamw_8bit" if mode == "qlora" else "adamw_torch",
    warmup_ratio=0.03,
    lr_scheduler_type="cosine",
    report_to="none",
)

# Detect text column for SFTTrainer.
text_col = None
for col in ["text", "instruction", "input", "content", "prompt"]:
    if col in dataset.column_names:
        text_col = col
        break

trainer_kwargs = dict(
    model=model,
    tokenizer=tokenizer,
    train_dataset=dataset,
    args=training_args,
    max_seq_length=max_seq_len,
)
if text_col:
    trainer_kwargs["dataset_text_field"] = text_col

trainer = SFTTrainer(**trainer_kwargs)

# Train.
print("Starting training...")
train_start = time.time()
result = trainer.train()
train_duration = time.time() - train_start
print(f"Training completed in {train_duration:.1f}s")

# Extract metrics.
train_loss = result.metrics.get("train_loss", 0.0)
samples_per_sec = result.metrics.get("train_samples_per_second", 0.0)
total_steps = result.metrics.get("train_steps", result.global_step if hasattr(result, "global_step") else 0)
print(f"Loss: {train_loss:.4f}, Samples/s: {samples_per_sec:.2f}, Steps: {total_steps}")

# Merge adapter and save.
if mode in ("lora", "qlora") and merge_adapter:
    print("Merging LoRA adapter into base model...")
    if use_unsloth:
        model.save_pretrained_merged(model_dir, tokenizer, save_method="merged_16bit")
    else:
        merged = model.merge_and_unload()
        merged.save_pretrained(model_dir)
        tokenizer.save_pretrained(model_dir)
    print("Merged model saved to", model_dir)
elif mode == "full":
    print("Saving finetuned model...")
    model.save_pretrained(model_dir)
    tokenizer.save_pretrained(model_dir)
    print("Model saved to", model_dir)
else:
    # Save adapter only (no merge).
    adapter_dir = os.path.join(model_dir, "adapter")
    model.save_pretrained(adapter_dir)
    tokenizer.save_pretrained(adapter_dir)
    print("Adapter saved to", adapter_dir)

# Write metadata to termination log.
meta = {
    "trainLoss": f"{train_loss:.6f}",
    "samplesPerSecond": f"{samples_per_sec:.2f}",
    "epochsCompleted": epochs,
    "totalSteps": int(total_steps),
}
meta_json = json.dumps(meta)
print(f"Metadata: {meta_json}")
with open("/dev/termination-log", "w") as f:
    f.write(meta_json)
with open(os.path.join(model_dir, ".finetune-status.json"), "w") as f:
    f.write(meta_json)

print("Finetune complete!")
PYEOF

END_TS=$(date +%%s)
DURATION=$((END_TS - START_TS))
echo "=== Finetune finished in ${DURATION}s ==="
`, modelPath, mode, epochs, batchSize, lr, maxSeqLen,
		loraRank, loraAlpha, loraDropout, targetModules,
		mergeAdapter, gradCheckpoint, datasetSource, datasetSplit,
		maxSamples, datasetPVCPath)
}
