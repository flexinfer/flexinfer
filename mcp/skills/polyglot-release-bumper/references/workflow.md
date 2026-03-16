# Polyglot Release Workflow

## Goal

Standardize a safe, repeatable release routine across Go/Python/Node repos without assuming a single build system.

## Steps (Suggested)

1. Ensure clean working tree and up-to-date main branch.
2. Run lint/tests/build for the repo’s language(s).
3. Bump version where applicable:
   - Python: `pyproject.toml` (`[project]` or `[tool.poetry]`)
   - Node: `package.json`
   - Go: usually tag-only (module version = git tag)
4. Update changelog / release notes (repo conventions).
5. Commit, tag, push.
6. Verify CI pipeline.
<<<<<<< Updated upstream

=======
>>>>>>> Stashed changes
