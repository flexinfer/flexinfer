#!/usr/bin/env python3
"""GPTQ quantization via GPTQModel.

All configuration is read from environment variables set by the controller:
  MODEL_DIR, BITS, GROUP_SIZE, MAX_MEMORY_GB, MAX_SEQ_LEN, MAX_SAMPLES,
  SYM, DESC_ACT, GPU_MEMORY_FRACTION, DYNAMIC_EXCLUSION, DATASET,
  FLEXINFER_TELEMETRY (optional, "true" enables JSON progress lines)
"""
import json
import os
import sys
import time


# ── Telemetry helper ──────────────────────────────────────────────────
def emit_progress(event_type, **kwargs):
    """Emit a structured JSON telemetry line to stdout."""
    if os.environ.get("FLEXINFER_TELEMETRY") != "true":
        return
    msg = {
        "event": event_type,
        "ts": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
    }
    msg.update(kwargs)
    print(json.dumps(msg), flush=True)


# ── Read config from environment ──────────────────────────────────────
model_dir = os.environ["MODEL_DIR"]
out_dir = os.environ["OUT_DIR"]
bits = int(os.environ["BITS"])
group_size = int(os.environ["GROUP_SIZE"])
max_memory_gb = int(os.environ["MAX_MEMORY_GB"])
max_seq_len = int(os.environ.get("MAX_SEQ_LEN", "4096"))
max_samples = int(os.environ.get("MAX_SAMPLES", "256"))
sym = os.environ.get("SYM", "True") == "True"
desc_act = os.environ.get("DESC_ACT", "False") == "True"
gpu_memory_fraction = float(os.environ.get("GPU_MEMORY_FRACTION", "0.80"))
dynamic_exclusion = os.environ.get("DYNAMIC_EXCLUSION", "auto")
dataset_name = os.environ.get("DATASET", "mit-han-lab/pile-val-backup")

emit_progress(
    "start", phase="quantizing", model=model_dir, bits=bits, group_size=group_size
)

# ── VLM config extraction ─────────────────────────────────────────────
# Models like Qwen3.5 have a composite VLM config wrapping text_config.
# Extract text_config to top level so transformers loads text-only model.
cfg_path = os.path.join(model_dir, "config.json")
with open(cfg_path) as f:
    cfg = json.load(f)
composite_text_model = "text_config" in cfg and "model_type" in cfg.get("text_config", {})
if composite_text_model:
    text_cfg = cfg["text_config"]
    for key in ["bos_token_id", "eos_token_id", "pad_token_id"]:
        if key in cfg and key not in text_cfg:
            text_cfg[key] = cfg[key]
    with open(cfg_path, "w") as f:
        json.dump(text_cfg, f, indent=2)
    print(f"Extracted text_config: model_type={text_cfg.get('model_type')}")

# ── Dynamic exclusion ──────────────────────────────────────────────────
if dynamic_exclusion == "none":
    dynamic_config = None
    print("Dynamic exclusion disabled (mode=none) -- all modules will be quantized")
else:
    with open(cfg_path) as f:
        cfg_recheck = json.load(f)
    dynamic_config = None
    if "layer_types" in cfg_recheck:
        layer_types = cfg_recheck["layer_types"]
        unique_types = set(layer_types)
        if len(unique_types) > 1:
            print(
                f"Hybrid architecture detected: {dict((t, layer_types.count(t)) for t in unique_types)}"
            )
            dynamic_config = {
                "-:.*attn.*": {},
                "-:.*shared_expert.*": {},
                "-:.*visual.*": {},
                "-:.*mtp.*": {},
            }
            print(f"Dynamic exclusion: {list(dynamic_config.keys())}")

# ── Memory management ──────────────────────────────────────────────────
import torch
from datasets import load_dataset
from gptqmodel import GPTQModel, QuantizeConfig
from transformers import AutoTokenizer

total_vram = torch.cuda.get_device_properties(0).total_memory
try:
    torch.cuda.set_per_process_memory_fraction(gpu_memory_fraction)
except RuntimeError:
    pass
print(
    f"Memory: GPU fraction={gpu_memory_fraction} ({int(total_vram * gpu_memory_fraction / (1024**3))}GiB of {total_vram // (1024**3)}GiB), container={max_memory_gb}Gi"
)

# ── Tokenizer + model ──────────────────────────────────────────────────
tokenizer = AutoTokenizer.from_pretrained(model_dir, trust_remote_code=True)
qcfg_kwargs = dict(bits=bits, group_size=group_size, sym=sym, desc_act=desc_act)
if dynamic_config is not None:
    qcfg_kwargs["dynamic"] = dynamic_config
# GPTQModel's offload-to-disk path uses a meta-device "turtle model" load. On
# composite Qwen3.5 text configs that path currently trips a transformers
# meta-tensor materialization bug, so force direct load for stability.
if composite_text_model:
    qcfg_kwargs["offload_to_disk"] = False
    print("Disabled GPTQ offload_to_disk for composite text_config model")
quantize_config = QuantizeConfig(**qcfg_kwargs)
model = GPTQModel.load(
    model_dir,
    quantize_config=quantize_config,
    trust_remote_code=True,
)

emit_progress("progress", phase="quantizing", percent=5.0, detail="model loaded")

# ── Calibration dataset ────────────────────────────────────────────────
dataset = load_dataset(dataset_name, split="validation")
examples = []
for sample in dataset.select(range(min(max_samples, len(dataset)))):
    tok = tokenizer(
        sample["text"], return_tensors="pt", max_length=max_seq_len, truncation=True
    )
    examples.append({"input_ids": tok.input_ids, "attention_mask": tok.attention_mask})

emit_progress(
    "progress", phase="quantizing", percent=10.0, detail="calibration data ready"
)

# ── Quantize ───────────────────────────────────────────────────────────
model.quantize(examples)

emit_progress("progress", phase="saving", percent=90.0, detail="saving quantized model")

# ── Save ───────────────────────────────────────────────────────────────
model.save(out_dir)
tokenizer.save_pretrained(out_dir)

emit_progress("complete", phase="quantizing")
print("Quantization complete")
