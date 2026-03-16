#!/usr/bin/env python3
from __future__ import annotations

import argparse
import shutil
from pathlib import Path


def _parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Initialize a .loom context pack folder using plan-loom-core templates."
    )
    parser.add_argument(
        "--root",
        default=".",
        help="Workspace/repo root where the context folder is created (default: .).",
    )
    parser.add_argument(
        "--dir",
        default=".loom",
        help="Context folder name/path relative to --root (default: .loom).",
    )
    parser.add_argument(
        "--force",
        action="store_true",
        help="Overwrite existing files in the context folder.",
    )
    return parser.parse_args()


def _skill_root() -> Path:
    return Path(__file__).resolve().parents[1]


def main() -> int:
    args = _parse_args()
    skill_root = _skill_root()
    templates_dir = skill_root / "assets" / "templates"
    if not templates_dir.is_dir():
        raise SystemExit(f"Missing templates directory: {templates_dir}")

    root = Path(args.root).resolve()
    out_dir = (root / args.dir).resolve()
    out_dir.mkdir(parents=True, exist_ok=True)

    created: list[Path] = []
    skipped: list[Path] = []
    overwritten: list[Path] = []

    for template_path in sorted(templates_dir.glob("*.md")):
        out_path = out_dir / template_path.name
        if out_path.exists() and not args.force:
            skipped.append(out_path)
            continue

        if out_path.exists() and args.force:
            overwritten.append(out_path)

        shutil.copy2(template_path, out_path)
        created.append(out_path)

    print(f"Initialized Loom context folder: {out_dir}")
    if created:
        print("Created:")
        for path in created:
            print(f"  - {path}")
    if overwritten:
        print("Overwrote:")
        for path in overwritten:
            print(f"  - {path}")
    if skipped:
        print("Skipped (exists, use --force to overwrite):")
        for path in skipped:
            print(f"  - {path}")

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
<<<<<<< Updated upstream

=======
>>>>>>> Stashed changes
