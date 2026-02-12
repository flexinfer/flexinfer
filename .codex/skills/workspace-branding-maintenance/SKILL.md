---
name: workspace-branding-maintenance
description: "Standardize repo branding across a multi-repo workspace: ensure assets/banner.png + assets/icon.png and README banner references using libs/banner-kit, and keep the services public allowlist consistent. Use when repos are missing branding assets, banners drift, or GitLab metadata/visibility needs alignment."
---

# Workspace Branding Maintenance

Use `libs/banner-kit` as the canonical implementation for generating and fixing workspace branding and related GitLab metadata.

## Quick Start

- Scan for missing assets / README banner drift:
  - `python ${CODEX_HOME:-$HOME/.codex}/skills/workspace-branding-maintenance/scripts/scan_branding.py --root .`
- Run banner-kit maintenance in dry-run mode (default):
  - `bash ${CODEX_HOME:-$HOME/.codex}/skills/workspace-branding-maintenance/scripts/run_workspace_branding.sh . --stash`
- Apply changes (explicit opt-in):
  - `APPLY=1 bash ${CODEX_HOME:-$HOME/.codex}/skills/workspace-branding-maintenance/scripts/run_workspace_branding.sh . --stash`

## Core Workflow

1. Identify drift with `scan_branding.py` and/or banner-kit `--dry-run`.
2. Confirm allowlist behavior:
   - `libs/banner-kit/scripts/workspace_public_services.txt`
3. Apply changes only after previewing.

## Notes / Constraints

- The source script may commit/push and update GitLab project metadata; always start with `--dry-run`.
- `libs/*` repos are public by default; `services/*` repos are private by default unless allowlisted.

## Bundled Resources

- `scripts/scan_branding.py`
- `scripts/run_workspace_branding.sh`
- `references/notes.md`
