# Workspace Branding Maintenance Notes

## Source Script

Use `libs/banner-kit/scripts/workspace_branding_maintenance.sh` as the canonical implementation.

## Safety Defaults

- Run with `--dry-run` first.
- Prefer `--stash` when operating across many repos.

## Public vs Private

- `libs/*` repos are public by default.
- `services/*` repos are private by default unless allowlisted in `libs/banner-kit/scripts/workspace_public_services.txt`.
<<<<<<< Updated upstream

=======
>>>>>>> Stashed changes
