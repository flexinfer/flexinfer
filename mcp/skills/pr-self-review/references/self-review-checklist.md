# PR Self-Review Checklist

Before creating an MR/PR, review your own diff against this checklist.
Fix any issues found before shipping.

## Checklist

### 1. Diff Size
- [ ] Total changed lines < 500
- If > 500 lines: split into smaller PRs or document why a large PR is necessary

### 2. No Debug Artifacts
- [ ] No `console.log()` statements (unless part of production logging)
- [ ] No `fmt.Println()` debug statements (use structured logging)
- [ ] No `print()` / `pprint()` debug output
- [ ] No `debugger` or `breakpoint()` statements
- [ ] No `TODO` or `FIXME` comments added (unless tracked in an issue)

### 3. No Commented-Out Code
- [ ] No large blocks of commented-out code
- [ ] If code is removed, it's deleted, not commented

### 4. No Secrets or Credentials
- [ ] No hardcoded API keys, tokens, or passwords
- [ ] No `.env` file contents in the diff
- [ ] No private keys or certificates
- [ ] No internal URLs or IP addresses exposed

### 5. Test Coverage
- [ ] All new functions have tests
- [ ] All new branches/conditions have test coverage
- [ ] Existing tests still pass
- [ ] Bug fixes include a regression test

### 6. Scope Check
- [ ] All changes relate to the stated PR purpose
- [ ] No unrelated refactoring mixed in
- [ ] No formatting-only changes in unrelated files
- [ ] Import ordering changes are minimal and intentional

### 7. Commit Quality
- [ ] Commit message follows conventional format: `type(scope): description`
- [ ] Commit message explains WHY, not just WHAT
- [ ] Each commit is a logical unit (no "fix typo" fix-up commits)

### 8. Documentation
- [ ] Public API changes are documented
- [ ] Breaking changes are called out
- [ ] New configuration options are documented

## Self-Correction Protocol

If any check fails:
1. Fix the issue in the code
2. Re-stage the changes
3. Amend or create a new commit
4. Re-run the self-review
5. Only proceed to MR creation when ALL checks pass
