#!/usr/bin/env python3
"""Add Swift files to LoomCompanion.xcodeproj/project.pbxproj.

Usage:
    python3 scripts/add_to_xcodeproj.py NewFile.swift --group GROUP_ID --sources SOURCES_ID

    # Multiple files at once:
    python3 scripts/add_to_xcodeproj.py File1.swift File2.swift --group GROUP_ID --sources SOURCES_ID

    # Dry run (show what would change):
    python3 scripts/add_to_xcodeproj.py NewFile.swift --group GROUP_ID --sources SOURCES_ID --dry-run

Common group/sources IDs (see docs/xcode-project-management.md for full list):

    LoomCompanionKit targets:
        Models:     --group A844B9E2031F24E4BAE6BAC3 --sources F26E08CF7C6C423A2D91DA7D
        Networking: --group DE723BFA102C44EB318ACE38 --sources F26E08CF7C6C423A2D91DA7D
        Services:   --group 2C5B5E8E4ED8B1A15914E3D1 --sources F26E08CF7C6C423A2D91DA7D
        ViewModels: --group B39CFD1AD6E2DA65E866D2C5 --sources F26E08CF7C6C423A2D91DA7D

    LoomCompanion (app) targets:
        Components:   --group FF004545B6ED4262B66A78D8 --sources 03D7A70606FB4EFE9B648B98
        DesignSystem: --group 7C0185E86B5B88DAC95F5BD1 --sources 03D7A70606FB4EFE9B648B98
        Dashboard:    --group E799C002B5D57AB6690F2D63 --sources 03D7A70606FB4EFE9B648B98
        Ops:          --group 94E138E964ADC74E7EBF3410 --sources 03D7A70606FB4EFE9B648B98
        SessionDetail:--group A4929F479D971135B1566B70 --sources 03D7A70606FB4EFE9B648B98
        Sessions:     --group 9D4BCF9F36EB467A7AC99AF4 --sources 03D7A70606FB4EFE9B648B98
        Shared:       --group 9E3AB81F7F45D768CE409B3E --sources 03D7A70606FB4EFE9B648B98

    LoomCompanionWidget targets:
        Widget:  --group 1DDDBF31AFD42FED125F68FB --sources 0810009D742E099841700DCC
"""

import argparse
import hashlib
import sys
from collections import defaultdict
from pathlib import Path


def gen_id(seed: str) -> str:
    """Generate a deterministic 24-char uppercase hex ID."""
    return hashlib.md5(seed.encode()).hexdigest()[:24].upper()


def find_pbxproj() -> Path:
    """Find the project.pbxproj relative to this script."""
    script_dir = Path(__file__).resolve().parent
    pbxproj = script_dir.parent / "LoomCompanion.xcodeproj" / "project.pbxproj"
    if not pbxproj.exists():
        print(f"Error: {pbxproj} not found", file=sys.stderr)
        sys.exit(1)
    return pbxproj


def add_files(
    filenames: list[str], group_id: str, sources_id: str, dry_run: bool = False
):
    pbxproj = find_pbxproj()

    # Generate IDs
    entries = []
    for fname in filenames:
        fr = gen_id(f"fileref_{fname}_2026")
        bf = gen_id(f"buildfile_{fname}_2026")
        entries.append((fname, fr, bf))

    # Read current file
    lines = pbxproj.read_text().splitlines(keepends=True)
    content = "".join(lines)

    # Check for existing files
    for fname, fr, bf in entries:
        if fname in content and f"path = {fname}" in content:
            print(
                f"Warning: {fname} already exists in pbxproj, skipping", file=sys.stderr
            )
            entries = [(f, r, b) for f, r, b in entries if f != fname]

    if not entries:
        print("No files to add.")
        return

    # Verify no ID collisions
    for fname, fr, bf in entries:
        if fr in content:
            print(f"Error: fileRef ID {fr} collision for {fname}", file=sys.stderr)
            sys.exit(1)
        if bf in content:
            print(f"Error: buildFile ID {bf} collision for {fname}", file=sys.stderr)
            sys.exit(1)

    # Build insertion maps
    group_items = [(fr, fname) for fname, fr, bf in entries]
    sources_items = [(bf, fname) for fname, fr, bf in entries]

    # Process line by line with state tracking
    new_lines = []
    inside_group = False
    inside_children = False
    inside_sources = False
    inside_files = False

    for line in lines:
        stripped = line.strip()

        # Detect target group definition
        if not inside_group and not inside_sources:
            if stripped.startswith(f"{group_id} /*") and stripped.endswith("= {"):
                inside_group = True

        # Detect target sources phase definition
        if not inside_sources and not inside_group:
            if stripped.startswith(f"{sources_id} /*") and stripped.endswith("= {"):
                inside_sources = True

        # Track children array
        if inside_group:
            if "children = (" in stripped:
                inside_children = True
            if inside_children and stripped == ");":
                for fr, fname in group_items:
                    new_lines.append(f"\t\t\t\t{fr} /* {fname} */,\n")
                inside_children = False
                inside_group = False

        # Track files array
        if inside_sources:
            if "files = (" in stripped:
                inside_files = True
            if inside_files and stripped == ");":
                for bf, fname in sources_items:
                    new_lines.append(f"\t\t\t\t{bf} /* {fname} in Sources */,\n")
                inside_files = False
                inside_sources = False

        # Insert PBXBuildFile entries
        if stripped == "/* End PBXBuildFile section */":
            for fname, fr, bf in entries:
                new_lines.append(
                    f"\t\t{bf} /* {fname} in Sources */ = "
                    f"{{isa = PBXBuildFile; fileRef = {fr} /* {fname} */; }};\n"
                )

        # Insert PBXFileReference entries
        if stripped == "/* End PBXFileReference section */":
            for fname, fr, bf in entries:
                new_lines.append(
                    f"\t\t{fr} /* {fname} */ = "
                    f"{{isa = PBXFileReference; lastKnownFileType = sourcecode.swift; "
                    f'path = {fname}; sourceTree = "<group>"; }};\n'
                )

        new_lines.append(line)

    if dry_run:
        print("DRY RUN — would add:")
        for fname, fr, bf in entries:
            print(f"  {fname}")
            print(f"    fileRef:   {fr}")
            print(f"    buildFile: {bf}")
            print(f"    group:     {group_id}")
            print(f"    sources:   {sources_id}")
        return

    pbxproj.write_text("".join(new_lines))
    print(f"Added {len(entries)} file(s) to {pbxproj.name}:")
    for fname, fr, bf in entries:
        print(f"  {fname} (fileRef={fr}, buildFile={bf})")


def main():
    parser = argparse.ArgumentParser(
        description="Add Swift files to LoomCompanion.xcodeproj",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=__doc__,
    )
    parser.add_argument("files", nargs="+", help="Swift filenames to add")
    parser.add_argument("--group", required=True, help="PBXGroup ID (24-char hex)")
    parser.add_argument(
        "--sources", required=True, help="PBXSourcesBuildPhase ID (24-char hex)"
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Show what would change without modifying",
    )

    args = parser.parse_args()
    add_files(args.files, args.group, args.sources, args.dry_run)


if __name__ == "__main__":
    main()
