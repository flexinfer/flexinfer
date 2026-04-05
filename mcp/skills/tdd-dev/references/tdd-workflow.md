# TDD-First Workflow Reference

## The Red/Green/Refactor Cycle

### Red Phase
Write the minimum tests that specify the desired behavior.
- Focus on WHAT the code should do, not HOW
- Tests should be specific and deterministic
- Run tests: they MUST fail (if they pass, you're not testing new behavior)

### Green Phase
Write the minimum code to make all tests pass.
- Only code needed to pass the tests -- nothing more
- Don't optimize, don't refactor, don't add features
- Run tests: they MUST pass

### Refactor Phase
Improve code quality while keeping tests green.
- Remove duplication
- Improve naming
- Simplify logic
- Run tests after each change: they MUST stay green

## Why Tests First?

1. **Tests are the specification.** They define "done" unambiguously.
2. **Verify-red prevents false confidence.** Tests that pass before implementation aren't testing anything.
3. **Minimum implementation.** You only write code the tests demand. YAGNI is enforced.
4. **Refactoring safety.** Green tests give confidence that refactoring doesn't break behavior.

## Tips for Writing Good Failing Tests

- Test the public interface, not implementation details
- Use descriptive test names that read like specifications
- One assertion per test (guideline, not rule)
- Test edge cases: empty input, nil, overflow, timeout
- For Go: use table-driven tests for parameter variations

## Automated Gates

The `verify-red` and `verify-green` steps are automated tool gates:
- `verify-red`: runs test suite, expects non-zero exit -> tests should fail
- `verify-green`: runs test suite, expects zero exit -> tests should pass

If `verify-red` finds tests passing, the agent must add or fix assertions.
If `verify-green` finds tests failing, the agent must continue implementing.
