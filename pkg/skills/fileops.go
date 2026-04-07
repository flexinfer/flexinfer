// fileops.go contains low-level file I/O helpers for skill generation.
package skills

import (
	"os"
	"path/filepath"
)

// writeFileAtomic writes data to path via a same-directory tempfile and
// os.Rename, making the write atomic from the perspective of concurrent
// readers (e.g. codex's skill file watcher).
//
// Without this, os.WriteFile's O_TRUNC + streaming write pattern exposes a
// window where a watcher can read the file as empty or partially written,
// which triggers false "missing YAML frontmatter delimited by ---" errors
// from codex (openai/codex#11495).
func writeFileAtomic(path string, data []byte, mode os.FileMode) (retErr error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".loom-skill-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if retErr != nil {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
