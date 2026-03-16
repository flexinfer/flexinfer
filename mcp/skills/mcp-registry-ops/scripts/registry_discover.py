#!/usr/bin/env python3
from __future__ import annotations

import argparse
import os
from pathlib import Path


def _parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(
        description="Find MCP registry.yaml files in a workspace (common: mcp/context/registry.yaml)."
    )
    p.add_argument("--root", default=".", help="Workspace root to scan (default: .).")
    p.add_argument(
        "--max-results", type=int, default=50, help="Max results to print (default: 50)."
    )
    return p.parse_args()


def main() -> int:
    args = _parse_args()
    root = Path(args.root).resolve()

    candidates: list[Path] = []
    for dirpath, dirnames, filenames in os.walk(root):
        if ".git" in dirnames:
            dirnames.remove(".git")
        for skip in ("node_modules", "dist", "build", "target", ".venv", "venv"):
            if skip in dirnames:
                dirnames.remove(skip)

        if "registry.yaml" in filenames:
            path = Path(dirpath) / "registry.yaml"
            if path.as_posix().endswith("/mcp/context/registry.yaml"):
                candidates.insert(0, path)
            else:
                candidates.append(path)

    seen: set[Path] = set()
    unique: list[Path] = []
    for p in candidates:
        rp = p.resolve()
        if rp in seen:
            continue
        seen.add(rp)
        unique.append(rp)

    for p in unique[: args.max_results]:
        print(p)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
