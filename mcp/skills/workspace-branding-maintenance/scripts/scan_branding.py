#!/usr/bin/env python3
from __future__ import annotations

import argparse
import os
from pathlib import Path


def _parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(
        description="Scan a workspace for repo branding drift (assets/banner.png, assets/icon.png, README banner reference)."
    )
    p.add_argument("--root", default=".", help="Workspace root (default: .).")
    p.add_argument("--max-repos", type=int, default=200, help="Max repos (default: 200).")
    return p.parse_args()


def _find_repos(root: Path, max_repos: int) -> list[Path]:
    repos: list[Path] = []
    for dirpath, dirnames, _filenames in os.walk(root):
        if ".git" in dirnames:
            repos.append(Path(dirpath))
            dirnames[:] = []
            if len(repos) >= max_repos:
                break
            continue
        for skip in ("node_modules", "dist", "build", "target", ".venv", "venv", "__pycache__"):
            if skip in dirnames:
                dirnames.remove(skip)
    return sorted(repos)


def _readme_has_banner(readme: Path) -> bool:
    try:
        head = readme.read_text(encoding="utf-8", errors="replace").splitlines()[:40]
    except FileNotFoundError:
        return False
    blob = "\n".join(head)
    return "assets/banner.png" in blob


def main() -> int:
    args = _parse_args()
    root = Path(args.root).resolve()
    repos = _find_repos(root, args.max_repos)

    missing_banner: list[Path] = []
    missing_icon: list[Path] = []
    missing_readme_banner: list[Path] = []

    for repo in repos:
        if not (repo / "assets" / "banner.png").exists():
            missing_banner.append(repo)
        if not (repo / "assets" / "icon.png").exists():
            missing_icon.append(repo)
        readme = repo / "README.md"
        if readme.exists() and not _readme_has_banner(readme):
            missing_readme_banner.append(repo)

    print(f"Workspace root: {root}")
    print(f"Repos scanned: {len(repos)}")
    print("")

    def _emit(title: str, items: list[Path]) -> None:
        print(title)
        if not items:
            print("  (none)")
            print("")
            return
        for p in items[:80]:
            print(f"  - {p.relative_to(root)}")
        if len(items) > 80:
            print("  - …")
        print("")

    _emit("Missing assets/banner.png:", missing_banner)
    _emit("Missing assets/icon.png:", missing_icon)
    _emit("README.md missing banner reference:", missing_readme_banner)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
