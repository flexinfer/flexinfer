"""KT-ASR (e): run a verbose_json body through ICC's real parser and aligner.

usage: python3 -I icc_check.py <icc_src_dir> <verbose_json> [speaker_change_s]

Uses integration_command_center.asr.whisper._parse_verbose_json (what
RealWhisperClient returns), the worker's word-key adaptation, and
synthesizers.asr_align.align with a two-speaker diarization split.
"""

import json
import sys

sys.path.insert(0, sys.argv[1])
from integration_command_center.asr import whisper as icc_whisper  # noqa: E402
from integration_command_center.synthesizers import asr_align  # noqa: E402


def words_for_align(words):
    # recordings_worker._words_for_align, verbatim logic
    return [
        {
            "text": w.get("word", ""),
            "start": w.get("start", 0.0),
            "end": w.get("end", w.get("start", 0.0)),
        }
        for w in words
    ]


def main():
    payload = json.load(open(sys.argv[2]))
    change = float(sys.argv[3]) if len(sys.argv) > 3 else None
    tr = icc_whisper._parse_verbose_json(payload, "whisper-large-v3-turbo-radeonvii")
    words = tr["words"]
    end = max((w["end"] for w in words), default=0.0)
    if change is None:
        change = end / 2
    diar = [
        {"start": 0.0, "end": change, "speaker_label": "SPEAKER_00"},
        {"start": change, "end": end + 1, "speaker_label": "SPEAKER_01"},
    ]
    merged = asr_align.align(words_for_align(words), diar)
    out = {
        "top_level_words": "words" in payload,
        "words": len(words),
        "monotonic": all(
            words[i]["start"] <= words[i + 1]["start"] for i in range(len(words) - 1)
        ),
        "bad_spans": sum(1 for w in words if w["end"] < w["start"]),
        "segments": len(tr["segments"]),
        "merged": merged if len(merged) <= 4 else len(merged),
        "first_words": [w["word"] for w in words[:12]],
    }
    print(json.dumps(out, indent=1))


if __name__ == "__main__":
    main()
