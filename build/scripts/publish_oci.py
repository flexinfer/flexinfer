#!/usr/bin/env python3
"""Publish model artifacts to an OCI registry via oras push.

Environment variables:
    MODEL_DIR       - Path to model directory to publish
    OCI_REF         - OCI artifact reference (e.g. registry.harbor.lan/models/qwen3:gptq-int4)
    OCI_USERNAME    - Registry username (optional, from secret)
    OCI_PASSWORD    - Registry password (optional, from secret)

Writes JSON metadata to /dev/termination-log on completion.
"""
import json
import os
import subprocess
import sys
import time


def env_truthy(name: str) -> bool:
    value = os.environ.get(name, "").strip().lower()
    return value in {"1", "true", "yes", "on"}


def emit_progress(event_type, **kwargs):
    msg = {
        "event": event_type,
        "ts": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
    }
    msg.update(kwargs)
    print(json.dumps(msg), flush=True)


def main():
    model_dir = os.environ.get("MODEL_DIR", "")
    oci_ref = os.environ.get("OCI_REF", "")

    if not model_dir or not oci_ref:
        print("ERROR: MODEL_DIR and OCI_REF are required", file=sys.stderr)
        sys.exit(1)

    if not os.path.isdir(model_dir):
        print(f"ERROR: MODEL_DIR does not exist: {model_dir}", file=sys.stderr)
        sys.exit(1)

    registry = oci_ref.split("/")[0]
    plain_http = env_truthy("OCI_PLAIN_HTTP") or registry.endswith(".lan")

    # Calculate total size for progress reporting.
    total_bytes = 0
    file_count = 0
    for root, _, files in os.walk(model_dir, followlinks=True):
        for f in files:
            total_bytes += os.path.getsize(os.path.join(root, f))
            file_count += 1

    emit_progress(
        "start",
        phase="publishing",
        target="oci",
        total_bytes=total_bytes,
        file_count=file_count,
    )

    # Build oras push command.
    cmd = ["oras", "push"]
    if plain_http:
        cmd.append("--plain-http")
    # MODEL_DIR is absolute, so disable path validation for the source side.
    cmd.append("--disable-path-validation")
    cmd.append(oci_ref)

    # Login if credentials provided.
    username = os.environ.get("OCI_USERNAME", "")
    password = os.environ.get("OCI_PASSWORD", "")
    if username and password:
        login_cmd = ["oras", "login"]
        if plain_http:
            login_cmd.append("--plain-http")
        login_cmd.extend([registry, "-u", username, "-p", password])
        result = subprocess.run(login_cmd, capture_output=True, text=True)
        if result.returncode != 0:
            print(f"ERROR: oras login failed: {result.stderr}", file=sys.stderr)
            sys.exit(1)
        emit_progress("progress", phase="authenticated", percent=5)

    # Collect files to push (relative paths from model_dir).
    artifacts = []
    for root, _, files in os.walk(model_dir, followlinks=True):
        for f in files:
            full_path = os.path.join(root, f)
            rel_path = os.path.relpath(full_path, model_dir)
            artifacts.append(f"{os.path.realpath(full_path)}:{rel_path}")

    cmd.extend(artifacts)
    cmd.extend(["--artifact-type", "application/vnd.flexinfer.model.v1"])

    emit_progress(
        "progress",
        phase="pushing",
        percent=10,
        detail=f"{file_count} files, {total_bytes / (1024**3):.1f} GiB",
    )

    start_time = time.time()
    result = subprocess.run(cmd, capture_output=True, text=True, cwd=model_dir)
    duration = time.time() - start_time

    if result.returncode != 0:
        emit_progress("error", phase="pushing", message=result.stderr[:500])
        print(f"ERROR: oras push failed: {result.stderr}", file=sys.stderr)
        sys.exit(1)

    # Parse digest from oras output.
    digest = ""
    for line in result.stdout.splitlines():
        if "Digest:" in line:
            digest = line.split("Digest:")[-1].strip()
            break

    emit_progress(
        "complete",
        phase="publishing",
        target="oci",
        duration_seconds=int(duration),
        digest=digest,
    )

    # Write termination log.
    metadata = {
        "target": "oci",
        "ociRef": oci_ref,
        "ociDigest": digest,
        "durationSeconds": int(duration),
        "totalBytes": total_bytes,
        "fileCount": file_count,
    }
    with open("/dev/termination-log", "w") as f:
        json.dump(metadata, f)

    print(f"Published to {oci_ref} (digest: {digest}) in {duration:.0f}s")


if __name__ == "__main__":
    main()
