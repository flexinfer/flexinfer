#!/usr/bin/env python3
from __future__ import annotations

import argparse
import re
from dataclasses import dataclass
from pathlib import Path


@dataclass(frozen=True)
class _Block:
    start: int
    end: int
    indent: int


def _parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(
        description="Edit registry.yaml allowlists (common/targets.*.always_allow) without a YAML dependency."
    )
    p.add_argument("--registry", required=True, help="Path to registry.yaml")
    p.add_argument("--server", required=True, help="Server name to edit (e.g., gitlab)")
    p.add_argument(
        "--scope",
        default="common",
        help="Allowlist scope: common or target:<name> (e.g., target:codex)",
    )
    action = p.add_mutually_exclusive_group(required=True)
    action.add_argument("--add", nargs="+", help="Tool names to add to always_allow")
    action.add_argument("--remove", nargs="+", help="Tool names to remove from always_allow")
    action.add_argument("--set", nargs="+", help="Replace always_allow with these tool names")
    p.add_argument("--sort", action="store_true", help="Sort tool names (lexicographic)")
    p.add_argument("--dry-run", action="store_true", help="Print changes without writing")
    return p.parse_args()


def _indent(line: str) -> int:
    return len(line) - len(line.lstrip(" "))


def _find_block(lines: list[str], start: int, end: int, key: str) -> _Block | None:
    pattern = re.compile(rf"^(\s*){re.escape(key)}:\s*(#.*)?$")
    for i in range(start, end):
        m = pattern.match(lines[i])
        if not m:
            continue
        indent = len(m.group(1))
        j = i + 1
        while j < end:
            if lines[j].strip() == "":
                j += 1
                continue
            if _indent(lines[j]) <= indent:
                break
            j += 1
        return _Block(start=i, end=j, indent=indent)
    return None


def _find_servers_list_indent(lines: list[str]) -> tuple[int, int] | None:
    for i, line in enumerate(lines):
        if re.match(r"^\s*servers:\s*$", line):
            for j in range(i + 1, len(lines)):
                m = re.match(r"^(\s*)-\s+name:\s*([A-Za-z0-9_.-]+)\s*$", lines[j])
                if m:
                    return i, len(m.group(1))
            return None
    return None


def _find_server_slice(lines: list[str], server: str) -> tuple[int, int, int] | None:
    servers_meta = _find_servers_list_indent(lines)
    if not servers_meta:
        return None
    servers_key_idx, item_indent = servers_meta

    starts: list[int] = []
    for i in range(servers_key_idx + 1, len(lines)):
        m = re.match(r"^(\s*)-\s+name:\s*([A-Za-z0-9_.-]+)\s*$", lines[i])
        if not m:
            continue
        if len(m.group(1)) != item_indent:
            continue
        starts.append(i)

    for idx, start in enumerate(starts):
        name = re.match(r"^\s*-\s+name:\s*([A-Za-z0-9_.-]+)\s*$", lines[start]).group(1)  # type: ignore[union-attr]
        if name != server:
            continue
        end = starts[idx + 1] if idx + 1 < len(starts) else len(lines)
        return start, end, item_indent
    return None


def _find_target_block(lines: list[str], server_start: int, server_end: int, target: str) -> _Block | None:
    targets_block = _find_block(lines, server_start, server_end, "targets")
    if not targets_block:
        return None
    target_key_pat = re.compile(rf"^(\s*){re.escape(target)}:\s*(#.*)?$")
    for i in range(targets_block.start + 1, targets_block.end):
        m = target_key_pat.match(lines[i])
        if not m:
            continue
        indent = len(m.group(1))
        j = i + 1
        while j < targets_block.end:
            if lines[j].strip() == "":
                j += 1
                continue
            if _indent(lines[j]) <= indent:
                break
            j += 1
        return _Block(start=i, end=j, indent=indent)
    return None


def _parse_tool_item(line: str) -> str | None:
    m = re.match(r"^\s*-\s*(.+?)\s*$", line)
    if not m:
        return None
    val = m.group(1).strip()
    if (val.startswith('"') and val.endswith('"')) or (val.startswith("'") and val.endswith("'")):
        val = val[1:-1]
    return val


def _rewrite_allowlist(
    lines: list[str],
    parent_block: _Block,
    *,
    always_key: str,
    desired: list[str],
) -> tuple[list[str], bool]:
    allow_block = _find_block(lines, parent_block.start, parent_block.end, always_key)
    changed = False

    if allow_block:
        original_line = lines[allow_block.start].rstrip("\n")
        m = re.match(r"^(\s*)always_allow:\s*(.*?)\s*(#.*)?$", original_line)
        comment = m.group(3) if m else None

        inline_value = (m.group(2) if m else "").strip()
        current: list[str] = []
        if inline_value:
            if inline_value == "[]":
                current = []
            elif inline_value.startswith("[") and inline_value.endswith("]"):
                inner = inline_value[1:-1].strip()
                if inner:
                    parts = [p.strip() for p in inner.split(",")]
                    for p in parts:
                        if (p.startswith('"') and p.endswith('"')) or (p.startswith("'") and p.endswith("'")):
                            p = p[1:-1]
                        if p:
                            current.append(p)
            else:
                current = []
        else:
            for i in range(allow_block.start + 1, allow_block.end):
                val = _parse_tool_item(lines[i])
                if val is not None:
                    current.append(val)

        if current != desired:
            changed = True

        if desired:
            header = " " * allow_block.indent + "always_allow:"
            if comment:
                header += " " + comment.lstrip()
            new_block = [header]
            item_indent = allow_block.indent + 2
            for t in desired:
                new_block.append(" " * item_indent + f"- {t}")
            new_block = [s + "\n" for s in new_block]
        else:
            header = " " * allow_block.indent + "always_allow: []"
            if comment:
                header += " " + comment.lstrip()
            new_block = [header + "\n"]

        if changed:
            return lines[: allow_block.start] + new_block + lines[allow_block.end :], True
        return lines, False

    insert_at = parent_block.end
    allow_indent = parent_block.indent + 2
    item_indent = allow_indent + 2

    if not desired:
        new_lines = [" " * allow_indent + "always_allow: []\n"]
    else:
        new_lines = [" " * allow_indent + "always_allow:\n"]
        for t in desired:
            new_lines.append(" " * item_indent + f"- {t}\n")

    return lines[:insert_at] + new_lines + lines[insert_at:], True


def main() -> int:
    args = _parse_args()
    registry_path = Path(args.registry).expanduser().resolve()
    lines = registry_path.read_text(encoding="utf-8", errors="replace").splitlines(keepends=True)

    server_slice = _find_server_slice(lines, args.server)
    if not server_slice:
        raise SystemExit(f"Server not found in registry: {args.server}")
    server_start, server_end, _item_indent = server_slice

    scope = args.scope.strip()
    if scope == "common":
        common_block = _find_block(lines, server_start, server_end, "common")
        if not common_block:
            raise SystemExit("Missing 'common' block for server")
        parent = common_block
    elif scope.startswith("target:"):
        target = scope.split(":", 1)[1].strip()
        if not target:
            raise SystemExit("Invalid --scope target:<name>")
        target_block = _find_target_block(lines, server_start, server_end, target)
        if not target_block:
            raise SystemExit(f"Target block not found: {target}")
        parent = target_block
    else:
        raise SystemExit("Invalid --scope (expected 'common' or 'target:<name>')")

    # Read existing allowlist (best-effort) to compute desired result.
    allow_block = _find_block(lines, parent.start, parent.end, "always_allow")
    current: list[str] = []
    if allow_block:
        header = lines[allow_block.start].rstrip("\n")
        m = re.match(r"^\s*always_allow:\s*(.*?)\s*(#.*)?$", header)
        inline_value = (m.group(1) if m else "").strip()
        if inline_value:
            if inline_value == "[]":
                current = []
            elif inline_value.startswith("[") and inline_value.endswith("]"):
                inner = inline_value[1:-1].strip()
                if inner:
                    for p in [x.strip() for x in inner.split(",")]:
                        if (p.startswith('"') and p.endswith('"')) or (p.startswith("'") and p.endswith("'")):
                            p = p[1:-1]
                        if p:
                            current.append(p)
        else:
            for i in range(allow_block.start + 1, allow_block.end):
                v = _parse_tool_item(lines[i])
                if v is not None:
                    current.append(v)

    desired: list[str]
    if args.set is not None:
        desired = list(dict.fromkeys(args.set))
    elif args.add is not None:
        desired = list(dict.fromkeys(current + args.add))
    elif args.remove is not None:
        to_remove = set(args.remove)
        desired = [t for t in current if t not in to_remove]
    else:
        raise SystemExit("No action provided")

    if args.sort:
        desired = sorted(set(desired))

    new_lines, changed = _rewrite_allowlist(lines, parent, always_key="always_allow", desired=desired)
    if not changed:
        print("No changes.")
        return 0

    if args.dry_run:
        print(f"Would update: {registry_path}")
        print(f"- server: {args.server}")
        print(f"- scope: {args.scope}")
        print(f"- always_allow: {desired}")
        return 0

    registry_path.write_text("".join(new_lines), encoding="utf-8")
    print(f"Updated: {registry_path}")
    print(f"- server: {args.server}")
    print(f"- scope: {args.scope}")
    print(f"- always_allow: {desired}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
