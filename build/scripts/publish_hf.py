#!/usr/bin/env python3
"""Publish model artifacts to HuggingFace Hub.

Environment variables:
    MODEL_DIR          - Path to model directory to publish
    HF_REPO            - HuggingFace repo ID (e.g. "myorg/qwen3-gptq-int4")
    HF_TOKEN           - HuggingFace API token (from secret)
    HF_COMMIT_MESSAGE  - Optional commit message (default: "Published by flexinfer")

Writes JSON metadata to /dev/termination-log on completion.
"""
import json
import os
import sys
import time


def emit_progress(event_type, **kwargs):
    msg = {
        "event": event_type,
        "ts": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
    }
    msg.update(kwargs)
    print(json.dumps(msg), flush=True)


def main():
    model_dir = os.environ.get("MODEL_DIR", "")
    hf_repo = os.environ.get("HF_REPO", "")
    hf_token = os.environ.get("HF_TOKEN", "")

    if not model_dir or not hf_repo:
        print("ERROR: MODEL_DIR and HF_REPO are required", file=sys.stderr)
        sys.exit(1)

    if not os.path.isdir(model_dir):
        print(f"ERROR: MODEL_DIR does not exist: {model_dir}", file=sys.stderr)
        sys.exit(1)

    if not hf_token:
        print("ERROR: HF_TOKEN is required for HuggingFace upload", file=sys.stderr)
        sys.exit(1)

    # Calculate total size for progress reporting.
    total_bytes = 0
    file_count = 0
    for root, _, files in os.walk(model_dir):
        for f in files:
            total_bytes += os.path.getsize(os.path.join(root, f))
            file_count += 1

    emit_progress(
        "start",
        phase="publishing",
        target="huggingface",
        total_bytes=total_bytes,
        file_count=file_count,
    )

    commit_message = os.environ.get("HF_COMMIT_MESSAGE", "Published by flexinfer")

    try:
        from huggingface_hub import HfApi

        api = HfApi(token=hf_token)

        emit_progress("progress", phase="uploading", percent=10)

        start_time = time.time()
        commit_info = api.upload_folder(
            folder_path=model_dir,
            repo_id=hf_repo,
            repo_type="model",
            commit_message=commit_message,
        )
        duration = time.time() - start_time

        commit_hash = getattr(commit_info, "oid", "") or ""

        emit_progress(
            "complete",
            phase="publishing",
            target="huggingface",
            duration_seconds=int(duration),
            commit=commit_hash,
        )

        metadata = {
            "target": "huggingface",
            "hfRepo": hf_repo,
            "hfCommit": commit_hash,
            "durationSeconds": int(duration),
            "totalBytes": total_bytes,
            "fileCount": file_count,
        }
        with open("/dev/termination-log", "w") as f:
            json.dump(metadata, f)

        print(f"Published to {hf_repo} (commit: {commit_hash}) in {duration:.0f}s")

    except Exception as e:
        emit_progress("error", phase="uploading", message=str(e)[:500])
        print(f"ERROR: HuggingFace upload failed: {e}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
