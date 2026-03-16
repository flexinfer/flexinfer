#!/usr/bin/env python3
from __future__ import annotations

import argparse
import re
from datetime import date
from pathlib import Path


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(
        description="Scaffold a new Loom skill entry in skills-registry.yaml and create source directories."
    )
    p.add_argument(
        "--root", default=".", help="Workspace root (default: current directory)."
    )
    p.add_argument(
        "--registry", default="", help="Path to skills-registry.yaml (optional)."
    )
    p.add_argument("--name", required=True, help="Skill name (kebab-case preferred).")
    p.add_argument(
        "--description",
        default="",
        help="Short skill description. If omitted, one is generated from the skill name.",
    )
    p.add_argument(
        "--categories",
        default="workflow,automation",
        help="Comma-separated categories (default: workflow,automation).",
    )
    p.add_argument(
        "--insert-after",
        default="",
        help="Insert after an existing skill name instead of appending at end.",
    )
    p.add_argument(
        "--no-source-dirs",
        action="store_true",
        help="Do not create mcp/skills/<name>/scripts|references|assets/templates.",
    )
    p.add_argument(
        "--force", action="store_true", help="Allow updating an existing skill entry."
    )
    p.add_argument(
        "--dry-run",
        action="store_true",
        default=True,
        help="Preview changes without writing (default).",
    )
    p.add_argument(
        "--apply",
        action="store_true",
        help="Actually write changes (overrides --dry-run).",
    )
    return p.parse_args()


def normalize_name(raw: str) -> str:
    v = raw.strip().lower().replace("_", "-").replace(" ", "-")
    v = re.sub(r"[^a-z0-9-]+", "-", v)
    v = re.sub(r"-{2,}", "-", v)
    return v.strip("-")


def discover_registry(root: Path, explicit: str) -> Path:
    if explicit:
        p = Path(explicit).expanduser().resolve()
        if not p.exists():
            raise SystemExit(f"Registry not found: {p}")
        return p

    candidates = [
        root / "mcp" / "context" / "skills-registry.yaml",
        root / "services" / "loom-core" / "mcp" / "context" / "skills-registry.yaml",
        root / "platform" / "gitops" / "mcp" / "context" / "skills-registry.yaml",
    ]
    for p in candidates:
        if p.exists():
            return p.resolve()

    raise SystemExit(
        "Could not find skills-registry.yaml. Pass --registry explicitly or run from workspace root."
    )


def indent_block(text: str, spaces: int) -> str:
    prefix = " " * spaces
    lines = text.strip("\n").splitlines()
    if not lines:
        return prefix
    return "\n".join(prefix + line if line else prefix for line in lines)


def to_title(name: str) -> str:
    return " ".join(part.capitalize() for part in name.split("-") if part)


def build_instructions(name: str) -> str:
    title = to_title(name)
    return f"""# {title}

Purpose-built Loom skill scaffold. Edit this instruction body to match your domain/workflow.

## Trigger

Use this skill when the user asks for this workflow by name or asks for tasks in this domain.

## Core Workflow

1. Confirm scope and expected outputs.
2. Gather required context and constraints.
3. Execute the workflow using deterministic scripts where possible.
4. Validate outputs and summarize follow-ups.

## Bundled Resources

- Add reusable scripts in `scripts/`
- Add deep reference docs in `references/`
- Add output templates in `assets/templates/`
"""


def build_entry(name: str, description: str, categories: list[str]) -> str:
    if not description:
        description = f"Scaffolded Loom skill for {name.replace('-', ' ')} workflows."
    instructions = build_instructions(name)
    cats = ", ".join(categories)
    return (
        f"  - name: {name}\n"
        f"    categories: [{cats}]\n"
        "    common:\n"
        "      description: |\n"
        f"{indent_block(description, 8)}\n"
        "      instructions: |\n"
        f"{indent_block(instructions, 8)}\n"
        "      scripts: []\n"
        "      references: []\n"
        "      assets: []\n"
        "      always_allow: []\n"
        "    targets:\n"
        "      codex:\n"
        "        enabled: true\n"
        "        type: skill\n"
        "      claude:\n"
        "        enabled: true\n"
        "        type: command\n"
        "      kilocode:\n"
        "        enabled: true\n"
        "        type: rule\n"
        "      gemini:\n"
        "        enabled: true\n"
        "        type: skill\n"
    )


def update_updated_date(content: str) -> str:
    today = date.today().isoformat()
    return re.sub(
        r"^updated:\s+\d{4}-\d{2}-\d{2}\s*$",
        f"updated: {today}",
        content,
        count=1,
        flags=re.M,
    )


def find_insert_offset(content: str, insert_after: str) -> int:
    if not insert_after:
        return len(content)

    pat = re.compile(rf"^  - name:\s+{re.escape(insert_after)}\s*$", re.M)
    match = pat.search(content)
    if not match:
        raise SystemExit(f"insert-after skill not found: {insert_after}")

    next_skill = re.compile(r"^  - name:\s+", re.M).search(content, match.end())
    if next_skill:
        return next_skill.start()
    return len(content)


def main() -> int:
    args = parse_args()
    if args.apply:
        args.dry_run = False
    root = Path(args.root).expanduser().resolve()
    registry_path = discover_registry(root, args.registry)
    skills_root = registry_path.parent.parent / "skills"

    name = normalize_name(args.name)
    if not name:
        raise SystemExit(
            "Invalid --name after normalization; use letters, digits, and hyphens."
        )

    categories = [
        normalize_name(c) for c in args.categories.split(",") if normalize_name(c)
    ]
    if not categories:
        categories = ["workflow", "automation"]

    content = registry_path.read_text(encoding="utf-8")
    exists = (
        re.search(rf"^  - name:\s+{re.escape(name)}\s*$", content, flags=re.M)
        is not None
    )
    if exists and not args.force:
        raise SystemExit(
            f"Skill already exists in registry: {name} (use --force to proceed)"
        )

    entry = build_entry(name, args.description.strip(), categories)
    updated = update_updated_date(content)

    if exists and args.force:
        updated = re.sub(
            rf"^  - name:\s+{re.escape(name)}\s*$.*?(?=^  - name:\s+|\Z)",
            entry.rstrip() + "\n\n",
            updated,
            flags=re.M | re.S,
        )
    else:
        offset = find_insert_offset(updated, args.insert_after)
        if offset == len(updated):
            if not updated.endswith("\n"):
                updated += "\n"
            updated += "\n" + entry
        else:
            updated = updated[:offset] + entry + "\n" + updated[offset:]

    source_paths = [
        skills_root / name / "scripts",
        skills_root / name / "references",
        skills_root / name / "assets" / "templates",
    ]

    print(f"Registry: {registry_path}")
    print(f"Skill name: {name}")
    print(f"Categories: {', '.join(categories)}")
    print(f"Create source dirs: {not args.no_source_dirs}")

    if args.dry_run:
        print(
            "\n[dry-run] Registry would be updated and the following directories ensured:"
        )
        for p in source_paths:
            print(f"  - {p}")
        return 0

    registry_path.write_text(updated, encoding="utf-8")
    print(f"Updated: {registry_path}")

    if not args.no_source_dirs:
        for p in source_paths:
            p.mkdir(parents=True, exist_ok=True)
            print(f"Ensured: {p}")

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
