#!/usr/bin/env python3
from __future__ import annotations

import argparse
import datetime as dt
import os
import platform
import shutil
import subprocess
from pathlib import Path


def _parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Create a workspace snapshot Markdown file for planning/research/spec work."
    )
    parser.add_argument(
        "--root",
        default=".",
        help="Workspace/repo root to scan (default: .).",
    )
    parser.add_argument(
        "--out",
        default=".loom/00-workspace-snapshot.md",
        help="Output Markdown path, relative to --root (default: .loom/00-workspace-snapshot.md).",
    )
    parser.add_argument(
        "--max-files",
        type=int,
        default=200,
        help="Maximum number of tracked files to list (default: 200).",
    )
    parser.add_argument(
        "--max-agents-lines",
        type=int,
        default=120,
        help="Maximum lines to include per AGENTS.md (default: 120).",
    )
    return parser.parse_args()


def _run(cmd: list[str], *, cwd: Path) -> tuple[int, str, str]:
    p = subprocess.run(cmd, cwd=str(cwd), text=True, capture_output=True)
    return p.returncode, p.stdout.strip(), p.stderr.strip()


def _git_available() -> bool:
    return shutil.which("git") is not None


def _rg_available() -> bool:
    return shutil.which("rg") is not None


def _detect_repo_root(root: Path) -> Path | None:
    if not _git_available():
        return None
    code, out, _err = _run(["git", "rev-parse", "--show-toplevel"], cwd=root)
    if code != 0 or not out:
        return None
    return Path(out).resolve()


def _rel(path: Path, base: Path) -> str:
    try:
        return str(path.resolve().relative_to(base.resolve()))
    except Exception:
        return str(path)


def _find_agents_files(root: Path) -> list[Path]:
    exclude_dirs = {
        ".git",
        "node_modules",
        "dist",
        "build",
        "target",
        "coverage",
        ".next",
        ".venv",
        "venv",
        "__pycache__",
    }
    found: list[Path] = []
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if d not in exclude_dirs and not d.startswith(".terraform")]
        if "AGENTS.md" in filenames:
            found.append(Path(dirpath) / "AGENTS.md")
    return sorted(found)


def _read_head(path: Path, max_lines: int) -> str:
    lines: list[str] = []
    try:
        with path.open("r", encoding="utf-8", errors="replace") as f:
            for i, line in enumerate(f):
                if i >= max_lines:
                    lines.append("…")
                    break
                lines.append(line.rstrip("\n"))
    except FileNotFoundError:
        return ""
    return "\n".join(lines).rstrip()


def _list_key_files(root: Path) -> list[str]:
    candidates = [
        "README.md",
        "README",
        "AGENTS.md",
        "package.json",
        "pnpm-lock.yaml",
        "yarn.lock",
        "package-lock.json",
        "tsconfig.json",
        "pyproject.toml",
        "requirements.txt",
        "Pipfile",
        "poetry.lock",
        "uv.lock",
        "go.mod",
        "Cargo.toml",
        "Gemfile",
        "composer.json",
        "pom.xml",
        "build.gradle",
        "Makefile",
        "docker-compose.yml",
        "Dockerfile",
    ]
    existing: list[str] = []
    for name in candidates:
        path = root / name
        if path.exists():
            existing.append(name)
    return existing


def _top_level_listing(root: Path) -> tuple[list[str], list[str]]:
    if not root.is_dir():
        return [], []
    entries = sorted(root.iterdir(), key=lambda p: (p.is_file(), p.name.lower()))
    dirs = [p.name + "/" for p in entries if p.is_dir()]
    files = [p.name for p in entries if p.is_file()]
    return dirs, files


def _tracked_files_listing(root: Path, max_files: int) -> list[str]:
    if _git_available():
        code, out, _err = _run(["git", "ls-files"], cwd=root)
        if code == 0 and out:
            lines = out.splitlines()
            return lines[:max_files] + (["…"] if len(lines) > max_files else [])

    if _rg_available():
        code, out, _err = _run(["rg", "--files"], cwd=root)
        if code == 0 and out:
            lines = out.splitlines()
            return lines[:max_files] + (["…"] if len(lines) > max_files else [])

    files: list[str] = []
    exclude_dirs = {".git", "node_modules", "dist", "build", "target", ".venv", "venv", "__pycache__"}
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if d not in exclude_dirs]
        for filename in filenames:
            files.append(str(Path(dirpath).joinpath(filename).relative_to(root)))
            if len(files) >= max_files:
                return files + ["…"]
    return files


def main() -> int:
    args = _parse_args()
    root = Path(args.root).resolve()
    out_path = (root / args.out).resolve()
    out_path.parent.mkdir(parents=True, exist_ok=True)

    repo_root = _detect_repo_root(root)
    now = dt.datetime.now(dt.timezone.utc).astimezone().isoformat(timespec="seconds")

    lines: list[str] = []
    lines.append("# Workspace Snapshot")
    lines.append("")
    lines.append(f"- Generated: {now}")
    lines.append(f"- Root: `{root}`")
    if repo_root:
        lines.append(f"- Git toplevel: `{repo_root}`")
    lines.append(f"- Platform: `{platform.platform()}`")
    lines.append(f"- Python: `{platform.python_version()}`")
    lines.append("")

    lines.append("## Git")
    if not _git_available():
        lines.append("- `git` not found on PATH")
    else:
        code, out, err = _run(["git", "status", "--porcelain=v1", "-b"], cwd=root)
        if code == 0:
            lines.append("```")
            lines.append(out if out else "(clean)")
            lines.append("```")
        else:
            lines.append(f"- Not a git repo (or git error): `{err or 'unknown error'}`")

        code, out, _err = _run(["git", "remote", "-v"], cwd=root)
        if code == 0 and out:
            lines.append("")
            lines.append("### Remotes")
            lines.append("```")
            lines.append(out)
            lines.append("```")

        code, out, _err = _run(["git", "log", "-1", "--oneline"], cwd=root)
        if code == 0 and out:
            lines.append("")
            lines.append("### HEAD")
            lines.append("```")
            lines.append(out)
            lines.append("```")

    lines.append("")
    lines.append("## Top-Level Layout")
    dirs, files = _top_level_listing(root)
    if dirs:
        lines.append("")
        lines.append("### Directories")
        for d in dirs[:80]:
            lines.append(f"- `{d}`")
        if len(dirs) > 80:
            lines.append("- `…`")
    if files:
        lines.append("")
        lines.append("### Files")
        for f in files[:80]:
            lines.append(f"- `{f}`")
        if len(files) > 80:
            lines.append("- `…`")

    key_files = _list_key_files(root)
    if key_files:
        lines.append("")
        lines.append("## Key Files Detected")
        for name in key_files:
            lines.append(f"- `{name}`")

    lines.append("")
    lines.append("## Tracked / Indexed Files (sample)")
    for f in _tracked_files_listing(root, args.max_files):
        lines.append(f"- `{f}`")

    agents_files = _find_agents_files(root)
    lines.append("")
    lines.append("## AGENTS.md Files")
    if not agents_files:
        lines.append("- None found")
    else:
        for path in agents_files:
            rel = _rel(path, root)
            lines.append(f"- `{rel}`")

        lines.append("")
        lines.append("### AGENTS.md Contents (head)")
        for path in agents_files:
            rel = _rel(path, root)
            head = _read_head(path, args.max_agents_lines)
            lines.append("")
            lines.append(f"#### `{rel}`")
            lines.append("```")
            lines.append(head if head else "(empty/unreadable)")
            lines.append("```")

    lines.append("")
    lines.append("## Notes")
    lines.append("- Add MCP inventory via the plan-loom-core workflow (see `.loom/00-mcp-inventory.md`).")
    lines.append("")

    out_path.write_text("\n".join(lines), encoding="utf-8")
    print(f"Wrote: {out_path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
<<<<<<< Updated upstream

=======
>>>>>>> Stashed changes
