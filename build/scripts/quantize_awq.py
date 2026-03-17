#!/usr/bin/env python3
"""AWQ quantization via AutoAWQ.

All configuration is read from environment variables set by the controller:
  MODEL_DIR, OUT_DIR, BITS, GROUP_SIZE, MAX_SEQ_LEN, MAX_SAMPLES,
  N_PARALLEL_CALIB_SAMPLES (optional), FLEXINFER_TELEMETRY (optional)
"""
import json
import os
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


model_dir = os.environ["MODEL_DIR"]
out_dir = os.environ["OUT_DIR"]
bits = int(os.environ["BITS"])
group_size = int(os.environ["GROUP_SIZE"])
max_seq_len = int(os.environ.get("MAX_SEQ_LEN", "4096"))
max_samples = int(os.environ.get("MAX_SAMPLES", "256"))
n_parallel_str = os.environ.get("N_PARALLEL_CALIB_SAMPLES", "")

emit_progress(
    "start", phase="quantizing", model=model_dir, bits=bits, group_size=group_size
)

from awq import AutoAWQForCausalLM
from transformers import AutoTokenizer

model = AutoAWQForCausalLM.from_pretrained(model_dir, safetensors=True, device_map=None)
tokenizer = AutoTokenizer.from_pretrained(model_dir, trust_remote_code=True)

emit_progress("progress", phase="quantizing", percent=10.0, detail="model loaded")

quant_kwargs = dict(
    tokenizer=tokenizer,
    quant_config={
        "w_bit": bits,
        "q_group_size": group_size,
        "zero_point": True,
        "version": "GEMM",
    },
    max_calib_seq_len=max_seq_len,
    max_calib_samples=max_samples,
)
if n_parallel_str:
    quant_kwargs["n_parallel_calib_samples"] = int(n_parallel_str)

model.quantize(**quant_kwargs)

emit_progress("progress", phase="saving", percent=90.0, detail="saving quantized model")

model.save_quantized(out_dir)
tokenizer.save_pretrained(out_dir)

emit_progress("complete", phase="quantizing")
print("AWQ quantization complete")
