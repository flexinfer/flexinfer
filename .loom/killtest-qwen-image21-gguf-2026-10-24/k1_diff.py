"""K1: diff tensor names/shapes between two header dumps from k1_headers.py.

Block indices are collapsed to N so per-layer naming shows once.
usage: k1_diff.py <a.json> <b.json>
"""

import json
import re
import sys
from collections import Counter


def load(path):
    with open(path) as f:
        return json.load(f)["tensors"]


def pattern(name):
    return re.sub(r"\.(\d+)\.", ".N.", name)


def main():
    a_path, b_path = sys.argv[1:3]
    a, b = load(a_path), load(b_path)
    print(f"A={a_path} ({len(a)})  B={b_path} ({len(b)})")
    only_a = sorted(set(a) - set(b))
    only_b = sorted(set(b) - set(a))
    both = sorted(set(a) & set(b))
    shape_diff = [n for n in both if a[n]["shape"] != b[n]["shape"]]
    print(
        f"common names: {len(both)}  only A: {len(only_a)}  only B: {len(only_b)}  "
        f"shape mismatches among common: {len(shape_diff)}"
    )
    for label, names, src in (("only A", only_a, a), ("only B", only_b, b)):
        pats = Counter(pattern(n) for n in names)
        for p, c in sorted(pats.items()):
            ex = next(n for n in names if pattern(n) == p)
            print(
                f"  {label}: {p}  x{c}  e.g. {ex} {src[ex]['shape']} {src[ex]['dtype']}"
            )
    for n in shape_diff[:20]:
        print(f"  shape: {n} A={a[n]['shape']} B={b[n]['shape']}")
    types_a = Counter(v["dtype"] for v in a.values())
    types_b = Counter(v["dtype"] for v in b.values())
    print(f"  dtypes A: {dict(types_a)}")
    print(f"  dtypes B: {dict(types_b)}")


if __name__ == "__main__":
    main()
