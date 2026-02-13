---
name: polyglot-release-bumper
description: "Prepare and ship releases across Go/Python/Node repos: detect project type, run the right test/lint/build commands, bump versions in pyproject.toml/package.json when applicable, and execute commit/tag/push with a standardized checklist. Use when asked to bump versions, cut a release, update changelogs, or publish."
---

# Polyglot Release Bumper

Standardize a safe release routine across repos that don't share one build system.

## Quick Start

- Detect project type(s) and suggested commands:
  - `python $CODEX_HOME/skills/polyglot-release-bumper/scripts/detect_project.py --root .`
- Bump versions (Python/Node only) with preview:
  - `python $CODEX_HOME/skills/polyglot-release-bumper/scripts/bump_version.py --root . --bump patch --dry-run`
- Apply the bump:
  - `python $CODEX_HOME/skills/polyglot-release-bumper/scripts/bump_version.py --root . --bump patch`

## What Gets Updated

- `pyproject.toml`: first `version = "X.Y.Z"` found under `[project]` or `[tool.poetry]`
- `package.json`: top-level `"version"`
- Go modules (`go.mod`): usually tag-only (no file bump); ship via `git tag vX.Y.Z`

## Core Workflow

1. Confirm clean working tree; pull latest.
2. Run tests/lint/build for the repo.
3. Bump version (if applicable).
4. Update changelog / release notes (repo conventions).
5. Commit, tag, push.
6. Verify CI pipeline.

## References / Templates

- Workflow notes: `references/workflow.md`
- Checklist: `assets/templates/release-checklist.md`

## Bundled Resources

- `scripts/detect_project.py`
- `scripts/bump_version.py`
- `references/workflow.md`
- `assets/templates/release-checklist.md`
