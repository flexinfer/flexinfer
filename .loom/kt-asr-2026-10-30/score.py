"""KT-ASR scoring: normalized WER of whisper.cpp and faster-whisper.

usage: python3 -I score.py <ami_words_dir> <results_dir>

results_dir holds <clip>.whispercpp.json and <clip>.fw.json (verbose_json) and
twospk.ref.txt. Ground truth for the AMI clips is rebuilt from the manual word
annotations (punctuation tokens dropped, all speakers merged by start time).
"""

import glob
import json
import os
import re
import sys
import xml.etree.ElementTree as ET

AMI = {
    # clip: (meeting, window start s, window end s, offset applied to the cut)
    "meeting60": ("EN2001a", 0.0, 3600.0),
    "meeting10": ("EN2002a", 300.0, 900.0),
}
FILLERS = {
    "uh",
    "um",
    "hmm",
    "mm",
    "mhm",
    "mmm",
    "ah",
    "eh",
    "er",
    "erm",
    "hm",
    "uhm",
    "huh",
    "uh-huh",
    "um-hum",
}
ONES = (
    "zero one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen sixteen "
    "seventeen eighteen nineteen".split()
)
TENS = "_ _ twenty thirty forty fifty sixty seventy eighty ninety".split()
SPELL = {
    "colour": "color",
    "behaviour": "behavior",
    "favourite": "favorite",
    "organise": "organize",
    "realise": "realize",
    "recognise": "recognize",
    "centre": "center",
    "okay": "ok",
    "alright": "all right",
    "programme": "program",
    "analyse": "analyze",
    "labelled": "labeled",
    "modelling": "modeling",
    "vii": "seven",
}


def num_words(n):
    if n < 20:
        return ONES[n]
    if n < 100:
        return TENS[n // 10] + ("" if n % 10 == 0 else " " + ONES[n % 10])
    if n < 1000:
        return (
            ONES[n // 100]
            + " hundred"
            + ("" if n % 100 == 0 else " " + num_words(n % 100))
        )
    if n < 1_000_000:
        return (
            num_words(n // 1000)
            + " thousand"
            + ("" if n % 1000 == 0 else " " + num_words(n % 1000))
        )
    return str(n)


def normalize(text):
    t = text.lower()
    t = t.replace("%", " percent ")
    t = re.sub(r"(\d+)(st|nd|rd|th)\b", r"\1", t)
    t = re.sub(r"(\d),(\d)", r"\1\2", t)
    t = re.sub(r"\d+", lambda m: " " + num_words(int(m.group())) + " ", t)
    t = t.replace("-", " ").replace("'", "").replace("’", "")
    t = re.sub(r"[^a-z ]+", " ", t)
    t = t.replace("isation", "ization")
    words = [SPELL.get(w, w) for w in t.split() if w not in FILLERS]
    return " ".join(words).replace("all right", "all right").split()


def ami_reference(words_dir, meeting, start, end):
    toks = []
    for path in glob.glob(os.path.join(words_dir, f"{meeting}.*.words.xml")):
        for el in ET.parse(path).getroot():
            if el.tag != "w" or el.get("punc") == "true" or el.text is None:
                continue
            st = el.get("starttime")
            if st is None:
                continue
            st = float(st)
            if start <= st < end:
                toks.append((st, el.text))
    toks.sort()
    return " ".join(w for _, w in toks)


def wer(ref, hyp):
    """Word edit distance (S+D+I) / len(ref), row DP."""
    prev = list(range(len(hyp) + 1))
    for i, r in enumerate(ref, 1):
        cur = [i] + [0] * len(hyp)
        for j, h in enumerate(hyp, 1):
            cur[j] = min(prev[j] + 1, cur[j - 1] + 1, prev[j - 1] + (r != h))
        prev = cur
    return prev[-1] / max(1, len(ref))


def text_of(path):
    with open(path) as f:
        return json.load(f).get("text", "")


def main():
    words_dir, res = sys.argv[1], sys.argv[2]
    rows = {}
    for clip in ("twospk", "meeting10", "meeting60"):
        cpp_p, fw_p = f"{res}/{clip}.whispercpp.json", f"{res}/{clip}.fw.json"
        if not (os.path.exists(cpp_p) and os.path.exists(fw_p)):
            rows[clip] = "missing"
            continue
        if clip == "twospk":
            gt = open(f"{res}/twospk.ref.txt").read()
        else:
            m, s, e = AMI[clip]
            gt = ami_reference(words_dir, m, s, e)
        g, c, f = normalize(gt), normalize(text_of(cpp_p)), normalize(text_of(fw_p))
        rows[clip] = {
            "ref_words": len(g),
            "cpp_words": len(c),
            "fw_words": len(f),
            "wer_cpp_vs_truth": round(100 * wer(g, c), 2),
            "wer_fw_vs_truth": round(100 * wer(g, f), 2),
            "wer_cpp_vs_fw": round(100 * wer(f, c), 2),
        }
        print(clip, json.dumps(rows[clip]), flush=True)
    with open(f"{res}/wer.json", "w") as fh:
        json.dump(rows, fh, indent=1)


if __name__ == "__main__":
    main()
