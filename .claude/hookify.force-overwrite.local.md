---
name: macos-interactive-commands
enabled: true
event: bash
pattern: ^(?!yes\s*\|).*\b(rm|cp|mv)\s+.*\s+\S+
action: block
---

**BLOCKED: Interactive prompt risk on macOS!**

Commands `rm`, `cp`, and `mv` can prompt for confirmation, blocking non-interactive execution.

**Problem:** On macOS (including Apple Silicon M1-M4), the `-f` flag does NOT reliably suppress prompts because:
- Shell aliases like `rm='rm -i'` expand before your flags
- zsh's `rm_star_wait` prompts even with `-f` present
- BSD coreutils behave differently than GNU coreutils

**Solution - Use `yes |` prefix:**
```bash
yes | rm -rf old_directory/
yes | cp -r source/ dest/
yes | mv old_name new_name
```

**Why this works:** `yes` continuously outputs "y" to stdin, automatically confirming any prompts, regardless of shell configuration or platform.
