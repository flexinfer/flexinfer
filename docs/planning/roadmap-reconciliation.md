# Roadmap Reconciliation

Use this checklist after planning docs, roadmap checkboxes, or tracking issues
change. The goal is to leave the repo in a state where the next maintainer or
agent can choose work from committed docs instead of chat history.

## Required Sources

Check these surfaces together:

| Surface | Purpose |
|---------|---------|
| `ROADMAP.md` | High-level project state and long-term feature/debt checklist |
| `docs/planning/next-roadmap.md` | Near-term execution queue and acceptance criteria |
| `docs/planning/spec-driven-delivery.md` | Spec-driven delivery lane status and contracts |
| `.loom/00-index.md` | Current context-pack goal, open slices, and handoff links |
| GitLab issues | Open/closed state, completion notes, and links back to merged MRs |

## Reconciliation Checklist

- [ ] Confirm every completed roadmap slice is marked complete in both
      `ROADMAP.md` and `docs/planning/next-roadmap.md`.
- [ ] Confirm the relevant planning doc records what changed, not only the MR or
      commit message.
- [ ] Confirm `.loom/00-index.md` points to the current plan, matrix, or handoff
      artifact that a future agent should read first.
- [ ] Close completed GitLab issues only after the merged MR is on `master`.
- [ ] Add a closing issue note with the MR, touched planning docs, and validation
      commands.
- [ ] Leave still-open issues visible as the remaining backlog, with the next
      action explicit in planning docs.

## Useful Commands

```bash
git diff --check
rg "SD-[1-5]|Issue #5[5-9]|#5[5-9]" ROADMAP.md docs/planning/next-roadmap.md docs/planning/spec-driven-delivery.md .loom/00-index.md
glab issue list --opened --per-page 80 --order updated_at --sort desc
```

For a specific issue:

```bash
glab issue view <id>
glab issue note <id> --message "<completion note>"
glab issue close <id>
```

## Completion Note Template

```markdown
Completed in MR !<id>.

Updated:
- `ROADMAP.md`
- `docs/planning/next-roadmap.md`
- `<planning doc>`
- `.loom/00-index.md` when context-pack state changed

Validation:
- `git diff --check`
- `<targeted command>`
```

## Backout

This workflow is docs-only. If a reconciliation update is wrong, revert the docs
commit or send a follow-up MR that restores the prior issue and roadmap state.
