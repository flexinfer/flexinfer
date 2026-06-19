#!/usr/bin/env python3
"""Finetune model weights via LoRA/QLoRA or full finetuning.

Environment variables:
  MODEL_DIR, MODE, EPOCHS, BATCH_SIZE, LEARNING_RATE, MAX_SEQ_LEN,
  LORA_RANK, LORA_ALPHA, LORA_DROPOUT, TARGET_MODULES, MERGE_ADAPTER,
  GRAD_CHECKPOINT, DATASET_SOURCE, DATASET_SPLIT, MAX_SAMPLES,
  DATASET_PVC_PATH, FLEXINFER_TELEMETRY (optional)
"""
import json
import os
import sys
import time


# ── Telemetry helper ──────────────────────────────────────────────────
def emit_progress(event_type, **kwargs):
    if os.environ.get("FLEXINFER_TELEMETRY") != "true":
        return
    msg = {
        "event": event_type,
        "ts": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
    }
    msg.update(kwargs)
    print(json.dumps(msg), flush=True)


# ── Config ────────────────────────────────────────────────────────────
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

emit_progress("start", phase="finetuning", model=model_dir, mode=mode, epochs=epochs)

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

    model_kwargs = dict(device_map="auto", trust_remote_code=True)
    if mode == "qlora":
        # True 4-bit NF4 QLoRA (Unsloth absent). Without this the fallback loaded
        # bf16 weights and only the optimizer was 8-bit — i.e. not actually QLoRA.
        from transformers import BitsAndBytesConfig

        model_kwargs["quantization_config"] = BitsAndBytesConfig(
            load_in_4bit=True,
            bnb_4bit_quant_type="nf4",
            bnb_4bit_compute_dtype=torch.bfloat16,
            bnb_4bit_use_double_quant=True,
        )
        print("Loading base in 4-bit NF4 (QLoRA) via bitsandbytes")
    else:
        model_kwargs["torch_dtype"] = torch.bfloat16

    model = AutoModelForCausalLM.from_pretrained(model_dir, **model_kwargs)
    tokenizer = AutoTokenizer.from_pretrained(model_dir, trust_remote_code=True)
    print(f"Model loaded via transformers in {time.time() - load_start:.1f}s")

if tokenizer.pad_token is None:
    tokenizer.pad_token = tokenizer.eos_token

emit_progress("progress", phase="finetuning", percent=10.0, detail="model loaded")

# ── Apply LoRA/QLoRA adapters ─────────────────────────────────────────
if mode in ("lora", "qlora"):
    target_modules = target_modules_str.split(",") if target_modules_str else None
    if use_unsloth:
        model = FastLanguageModel.get_peft_model(
            model,
            r=lora_rank,
            lora_alpha=lora_alpha,
            lora_dropout=lora_dropout,
            target_modules=target_modules
            or [
                "q_proj",
                "k_proj",
                "v_proj",
                "o_proj",
                "gate_proj",
                "up_proj",
                "down_proj",
            ],
            use_gradient_checkpointing=("unsloth" if grad_checkpoint else False),
        )
    else:
        from peft import get_peft_model, LoraConfig, TaskType

        if mode == "qlora":
            # Required for stable 4-bit training: casts norms/embeddings to fp32,
            # enables input-grad hooks, and wires gradient checkpointing.
            from peft import prepare_model_for_kbit_training

            model = prepare_model_for_kbit_training(
                model, use_gradient_checkpointing=grad_checkpoint
            )

        peft_config = LoraConfig(
            r=lora_rank,
            lora_alpha=lora_alpha,
            lora_dropout=lora_dropout,
            target_modules=target_modules
            or [
                "q_proj",
                "k_proj",
                "v_proj",
                "o_proj",
                "gate_proj",
                "up_proj",
                "down_proj",
            ],
            task_type=TaskType.CAUSAL_LM,
        )
        model = get_peft_model(model, peft_config)
    model.print_trainable_parameters()

# ── Load dataset ──────────────────────────────────────────────────────
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

emit_progress("progress", phase="finetuning", percent=15.0, detail="dataset loaded")

# ── Configure trainer ─────────────────────────────────────────────────
from transformers import TrainingArguments, TrainerCallback
from trl import SFTTrainer


class TelemetryCallback(TrainerCallback):
    """Emit JSON progress lines during training."""

    def on_log(self, args, state, control, logs=None, **kwargs):
        if state.max_steps > 0:
            pct = 15.0 + (state.global_step / state.max_steps) * 75.0
            emit_progress(
                "progress",
                phase="training",
                percent=round(pct, 1),
                step=state.global_step,
                total_steps=state.max_steps,
                loss=logs.get("loss") if logs else None,
            )


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
    callbacks=[TelemetryCallback()],
)
if text_col:
    trainer_kwargs["dataset_text_field"] = text_col

trainer = SFTTrainer(**trainer_kwargs)

# ── Train ─────────────────────────────────────────────────────────────
print("Starting training...")
train_start = time.time()
result = trainer.train()
train_duration = time.time() - train_start
print(f"Training completed in {train_duration:.1f}s")

train_loss = result.metrics.get("train_loss", 0.0)
samples_per_sec = result.metrics.get("train_samples_per_second", 0.0)
total_steps = result.metrics.get(
    "train_steps", result.global_step if hasattr(result, "global_step") else 0
)
print(f"Loss: {train_loss:.4f}, Samples/s: {samples_per_sec:.2f}, Steps: {total_steps}")

# ── Merge adapter and save ────────────────────────────────────────────
emit_progress("progress", phase="saving", percent=92.0, detail="saving finetuned model")

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
    adapter_dir = os.path.join(model_dir, "adapter")
    model.save_pretrained(adapter_dir)
    tokenizer.save_pretrained(adapter_dir)
    print("Adapter saved to", adapter_dir)

# ── Write metadata ────────────────────────────────────────────────────
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

emit_progress(
    "complete", phase="finetuning", train_loss=train_loss, total_steps=int(total_steps)
)
print("Finetune complete!")
