#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import re
from dataclasses import dataclass
from pathlib import Path


@dataclass(frozen=True)
class SemVer:
    major: int
    minor: int
    patch: int

    @classmethod
    def parse(cls, s: str) -> "SemVer":
        m = re.match(r"^(\d+)\.(\d+)\.(\d+)$", s.strip())
        if not m:
            raise ValueError(f"Not a semver X.Y.Z: {s}")
        return cls(int(m.group(1)), int(m.group(2)), int(m.group(3)))

    def bump(self, kind: str) -> "SemVer":
        if kind == "patch":
            return SemVer(self.major, self.minor, self.patch + 1)
        if kind == "minor":
            return SemVer(self.major, self.minor + 1, 0)
        if kind == "major":
            return SemVer(self.major + 1, 0, 0)
        raise ValueError(f"Unknown bump kind: {kind}")

    def __str__(self) -> str:
        return f"{self.major}.{self.minor}.{self.patch}"


def _parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(
        description="Bump versions for common project files (pyproject.toml, package.json)."
    )
    p.add_argument("--root", default=".", help="Repo root (default: .).")
    g = p.add_mutually_exclusive_group(required=True)
    g.add_argument("--bump", choices=["patch", "minor", "major"], help="SemVer bump kind")
    g.add_argument("--set", help="Set version explicitly (X.Y.Z)")
    p.add_argument("--dry-run", action="store_true", help="Do not write files")
    return p.parse_args()


def _bump_or_set(current: str, args: argparse.Namespace) -> str:
    if args.set:
        SemVer.parse(args.set)
        return args.set
    ver = SemVer.parse(current)
    return str(ver.bump(args.bump))


def _update_pyproject(path: Path, args: argparse.Namespace) -> tuple[bool, str | None, str | None]:
    text = path.read_text(encoding="utf-8", errors="replace").splitlines(keepends=True)
    section: str | None = None
    old: str | None = None
    new: str | None = None
    changed = False

    for i, line in enumerate(text):
        m = re.match(r"^\s*\[(.+?)\]\s*$", line)
        if m:
            section = m.group(1).strip()
            continue

        if section not in ("project", "tool.poetry"):
            continue

        m = re.match(r"^(\s*)version\s*=\s*\"([0-9]+\.[0-9]+\.[0-9]+)\"(\s*(#.*)?)\s*$", line)
        if not m:
            continue

        old = m.group(2)
        new = _bump_or_set(old, args)
        text[i] = f'{m.group(1)}version = "{new}"{m.group(3) or ""}\n'
        changed = True
        break

    if changed and not args.dry_run:
        path.write_text("".join(text), encoding="utf-8")
    return changed, old, new


def _update_package_json(path: Path, args: argparse.Namespace) -> tuple[bool, str | None, str | None]:
    data = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(data, dict) or "version" not in data:
        return False, None, None
    old = str(data["version"])
    new = _bump_or_set(old, args)
    if new == old:
        return False, old, new
    data["version"] = new
    if not args.dry_run:
        path.write_text(json.dumps(data, indent=2) + "\n", encoding="utf-8")
    return True, old, new


def main() -> int:
    args = _parse_args()
    root = Path(args.root).resolve()

    updates: list[str] = []
    pyproject = root / "pyproject.toml"
    if pyproject.exists():
        changed, old, new = _update_pyproject(pyproject, args)
        if changed:
            updates.append(f"- {pyproject}: {old} -> {new}")

    pkg = root / "package.json"
    if pkg.exists():
        changed, old, new = _update_package_json(pkg, args)
        if changed:
            updates.append(f"- {pkg}: {old} -> {new}")

    if not updates:
        print("No updates applied (no supported version fields found).")
        return 0

    if args.dry_run:
        print("Dry run:")
    else:
        print("Updated:")
    for u in updates:
        print(u)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
<<<<<<< Updated upstream

=======
>>>>>>> Stashed changes
