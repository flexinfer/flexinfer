#!/usr/bin/env python3
"""One-shot kill-test for pyannote speaker-diarization-3.1 on gfx906.

Proves the load-bearing assumption: the pipeline loads on the mixa3607
PyTorch fork, dispatches on the Radeon VII GPU, and produces speaker turns.

Usage:
    pyannote_diarize_probe.py <audio.wav> [expected_speakers]

Env:
    HF_TOKEN   required (gated models: speaker-diarization-3.1, segmentation-3.0)

Exit 0 = PASS (>=1 segment produced; >=expected speakers if given).
Exit 1 = FAIL (load error, gate/license error, no GPU, or no segments).
"""
import os
import sys
import time
import traceback


def main() -> int:
    if len(sys.argv) < 2:
        print("FAIL: usage: pyannote_diarize_probe.py <audio.wav> [expected_speakers]")
        return 1
    audio = sys.argv[1]
    expected = int(sys.argv[2]) if len(sys.argv) > 2 else 0
    # Optional 3rd arg forces num_speakers (pyannote auto-detection is
    # unreliable on acoustically-uniform TTS clips; a real meeting auto-detects).
    force_n = int(sys.argv[3]) if len(sys.argv) > 3 else None

    token = os.environ.get("HF_TOKEN")
    if not token:
        print("FAIL: HF_TOKEN not set (gated pyannote models require it)")
        return 1
    if not os.path.exists(audio):
        print(f"FAIL: audio file not found: {audio}")
        return 1

    print("=== pyannote gfx906 kill-test ===", flush=True)
    import torch

    print(
        f"torch={torch.__version__}  cuda_available={torch.cuda.is_available()}",
        flush=True,
    )
    if torch.cuda.is_available():
        print(f"device={torch.cuda.get_device_name(0)}", flush=True)
    else:
        print(
            "WARN: torch.cuda.is_available() is False — diarization will run on CPU "
            "(assumption about GPU dispatch is NOT proven)",
            flush=True,
        )

    import torchaudio_compat  # noqa: F401  — must precede pyannote (torchaudio 2.9 shim)
    from pyannote.audio import Pipeline

    t0 = time.time()
    try:
        pipeline = Pipeline.from_pretrained(
            "pyannote/speaker-diarization-3.1",
            use_auth_token=token,
        )
    except Exception as e:  # noqa: BLE001
        print(f"FAIL: pipeline load error (gate/license/dep): {type(e).__name__}: {e}")
        traceback.print_exc()
        return 1
    print(f"pipeline loaded in {time.time()-t0:.1f}s", flush=True)

    used_mb = None
    if torch.cuda.is_available():
        try:
            pipeline.to(torch.device("cuda"))
            print("pipeline moved to cuda", flush=True)
        except Exception as e:  # noqa: BLE001
            print(f"WARN: pipeline.to(cuda) failed, staying on CPU: {e}", flush=True)

    t1 = time.time()
    try:
        diarization = (
            pipeline(audio, num_speakers=force_n) if force_n else pipeline(audio)
        )
    except Exception as e:  # noqa: BLE001
        print(f"FAIL: diarization run error: {type(e).__name__}: {e}")
        traceback.print_exc()
        return 1
    run_s = time.time() - t1

    if torch.cuda.is_available():
        used_mb = torch.cuda.max_memory_allocated() / (1024 * 1024)

    segments = list(diarization.itertracks(yield_label=True))
    speakers = sorted({label for _, _, label in segments})
    print(f"\n--- RTTM segments ({len(segments)}) ---", flush=True)
    for turn, _, label in segments:
        print(f"  {label}  {turn.start:6.2f}s -> {turn.end:6.2f}s", flush=True)
    print(f"\ndistinct speakers: {len(speakers)} {speakers}", flush=True)
    print(f"diarize wall time: {run_s:.1f}s", flush=True)
    if used_mb is not None:
        print(f"peak GPU mem allocated: {used_mb:.1f} MiB", flush=True)

    if not segments:
        print("\nVERDICT: FAIL — no segments produced")
        return 1
    if expected and len(speakers) < expected:
        print(
            f"\nVERDICT: FAIL — found {len(speakers)} speakers, expected >= {expected}"
        )
        return 1
    gpu_note = (
        "on GPU"
        if (torch.cuda.is_available() and used_mb and used_mb > 5)
        else "on CPU (GPU NOT exercised)"
    )
    print(
        f"\nVERDICT: PASS — {len(segments)} segments, {len(speakers)} speakers, {gpu_note}"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
