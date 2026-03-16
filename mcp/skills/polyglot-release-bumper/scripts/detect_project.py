#!/usr/bin/env python3
from __future__ import annotations

import argparse
from pathlib import Path


def _parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(description="Detect project type (go/python/node) and print suggested release commands.")
    p.add_argument("--root", default=".", help="Repo root (default: .).")
    return p.parse_args()


def main() -> int:
    root = Path(_parse_args().root).resolve()

    is_go = (root / "go.mod").exists()
    is_py = (root / "pyproject.toml").exists() or (root / "setup.cfg").exists()
    is_node = (root / "package.json").exists()

    kinds = []
    if is_go:
        kinds.append("go")
    if is_py:
        kinds.append("python")
    if is_node:
        kinds.append("node")

    print(f"Root: {root}")
    print(f"Detected: {', '.join(kinds) if kinds else 'unknown'}")
    print("")

    if is_go:
        print("Go suggestions:")
        print("- Run tests: `go test ./...`")
        print("- Lint (if configured): `golangci-lint run`")
        print("- Tag: `git tag vX.Y.Z && git push origin vX.Y.Z`")
        print("")

    if is_py:
        print("Python suggestions:")
        print("- Run tests: `pytest -q`")
        print("- Lint/format (repo dependent): `ruff check .` / `ruff format .` / `black -q .`")
        print("")

    if is_node:
        print("Node suggestions:")
        print("- Install: `npm ci` (or `pnpm i`) ")
        print("- Test: `npm test`")
        print("- Build: `npm run build`")
        print("")

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
<<<<<<< Updated upstream

=======
>>>>>>> Stashed changes
