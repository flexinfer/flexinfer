# Test Health Injection

Inject test suite health into the agent's session context at startup.

## How It Works

1. SessionStart hook runs `test-health-snapshot.sh`
2. Script detects project language (Go/Python/TS/Rust)
3. Runs test suite with 30-second timeout
4. Emits structured JSON `systemMessage` with:
   - Total tests, passed, failed, skipped
   - Failed test names (up to 5)
   - Build status (OK/FAIL)
   - Runtime
   - Last commit hash

## Enabling

Add `sessionStart_testHealth` to the platform's `extras` list in the platform profile:

```yaml
hooks:
  extras:
    - sessionStart_testHealth
```

Then regenerate: `loom sync <platform> --regen`

## Timeout

Default timeout is 30 seconds. Override with:
```bash
TEST_HEALTH_TIMEOUT=60 bash test-health-snapshot.sh
```

## Output Format

```json
{"systemMessage":"Project health: 142 tests, 140 passed, 2 failed (TestFoo, TestBar). Build: OK. Runtime: 12s. Last commit: abc1234 fix: resolve auth issue"}
```
