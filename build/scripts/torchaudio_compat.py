"""torchaudio 2.9 back-compat shim for pyannote on the gfx906 fork.

IMPORT THIS BEFORE pyannote.audio. Unlike sitecustomize (which runs too early
in interpreter startup, before torch is importable), this module is imported
explicitly at script runtime when torch/torchaudio are fully ready.

The mixa3607/pytorch-gfx906 fork ships torchaudio 2.9, which removed symbols
pyannote.audio 3.x references at *import* time:
  - torchaudio.set_audio_backend / get_audio_backend / list_audio_backends
    (deprecated 2.1, removed 2.2)
  - torchaudio.AudioMetaData (top-level alias removed ~2.8; pyannote
    core/io.py uses it as a module-level return annotation)

It also force-defaults torch.load(weights_only=False): the fork's torch 2.9
flipped that default to True (PyTorch 2.6+), which refuses to unpickle the
pyannote checkpoints (they contain torch.torch_version.TorchVersion globals).
These are the trusted official pyannote weights, so weights_only=False is safe.
"""

import torch as _torch

# Force-default torch.load(weights_only=False) for pyannote/lightning, which
# call torch.load() without the arg. The fork's torch 2.9 defaults it to True
# and rejects the (trusted) pyannote checkpoint globals.
_orig_torch_load = _torch.load


def _patched_torch_load(*args, **kwargs):
    # Force False, not setdefault: lightning_fabric passes weights_only=True
    # explicitly, so a default would be a no-op. These are trusted pyannote
    # checkpoints, so disabling the safe-unpickler is acceptable here.
    kwargs["weights_only"] = False
    return _orig_torch_load(*args, **kwargs)


_torch.load = _patched_torch_load

# pyannote's check_version parses torch/torchaudio __version__ as strict SemVer.
# The fork's strings ("2.9.0a0+git0fabc3b") are not valid SemVer and raise in
# semver.VersionInfo.parse. Sanitize to the X.Y.Z prefix — it's only a soft
# version-mismatch warning check, never used for dispatch.
import re as _re

_torch.__version__ = (_re.match(r"\d+\.\d+\.\d+", _torch.__version__) or [None])[
    0
] or _torch.__version__

import torchaudio as _ta

_ta.__version__ = (_re.match(r"\d+\.\d+\.\d+", _ta.__version__) or [None])[
    0
] or _ta.__version__

# Return sensible values, not None: pyannote io.py does
# `"soundfile" in torchaudio.list_audio_backends()`, so the list must be
# iterable and contain the installed soundfile backend.
if not hasattr(_ta, "set_audio_backend"):
    _ta.set_audio_backend = lambda *a, **k: None
if not hasattr(_ta, "get_audio_backend"):
    _ta.get_audio_backend = lambda *a, **k: "soundfile"
if not hasattr(_ta, "list_audio_backends"):
    _ta.list_audio_backends = lambda *a, **k: ["soundfile"]

if not hasattr(_ta, "AudioMetaData"):
    _amd = None
    for _path in (
        "torio._backend.common",
        "torchaudio.backend.common",
        "torchaudio._backend.common",
    ):
        try:
            _mod = __import__(_path, fromlist=["AudioMetaData"])
            _amd = getattr(_mod, "AudioMetaData", None)
            if _amd is not None:
                break
        except Exception:
            pass
    # A stub satisfies pyannote's type annotation if no real class is found.
    _ta.AudioMetaData = _amd if _amd is not None else type("AudioMetaData", (), {})

# torchaudio 2.9 routes load()/info() through torchcodec by default, which is
# not installed (and has no ROCm-fork wheel). Redirect both to soundfile, which
# IS installed and handles the WAV/FLAC inputs pyannote uses. Signatures mirror
# torchaudio's: load -> (waveform[channels, frames] float32, sample_rate).
import soundfile as _sf
import torch as _t


def _sf_load(filepath, frame_offset=0, num_frames=-1, *args, **kwargs):
    start = frame_offset or 0
    frames = num_frames if (num_frames not in (None, -1) and num_frames > 0) else -1
    data, sr = _sf.read(
        filepath, dtype="float32", always_2d=True, start=start, frames=frames
    )
    # soundfile returns (frames, channels); torchaudio wants (channels, frames).
    return _t.from_numpy(data.T.copy()), sr


def _sf_info(filepath, *args, **kwargs):
    si = _sf.info(filepath)
    meta = _ta.AudioMetaData() if isinstance(_ta.AudioMetaData, type) else object()
    meta.sample_rate = si.samplerate
    meta.num_frames = si.frames
    meta.num_channels = si.channels
    meta.bits_per_sample = 0
    meta.encoding = si.subtype or "PCM_S"
    return meta


_ta.load = _sf_load
_ta.info = _sf_info
