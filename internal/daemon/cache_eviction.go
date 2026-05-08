// cache_eviction.go evicts stale per-session marker files from
// ~/.cache/loom/. The hook bash bootstrap writes one
// `agent-id-<workspace-hash>-<session-hash>` file per Claude Code session
// to remember the agent ID across hook invocations, plus
// `parent-session-<agent-id>` files used to thread Subagent sessions.
// These files were never automatically pruned, so over a multi-month
// span the directory grew to several hundred files; pre-cleanup pass
// today found 269 entries with 134 older than 7 days.
package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// evictableCachePrefixes are the filename prefixes the eviction sweep
// considers. Any file under the cache dir whose basename starts with one
// of these and whose mtime is older than maxAge is removed.
var evictableCachePrefixes = []string{
	"agent-id-",
	"parent-session-",
}

// EvictAgentCache removes hook-bootstrap marker files older than maxAge
// from cacheDir. Returns the count of files removed plus the count
// considered (so the caller can log churn). Errors removing individual
// files are accumulated but never abort the sweep.
func EvictAgentCache(cacheDir string, maxAge time.Duration, now time.Time) (removed int, considered int, err error) {
	entries, readErr := os.ReadDir(cacheDir)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("read cache dir: %w", readErr)
	}

	var firstErr error
	cutoff := now.Add(-maxAge)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !hasEvictablePrefix(e.Name()) {
			continue
		}
		considered++
		info, infoErr := e.Info()
		if infoErr != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("stat %s: %w", e.Name(), infoErr)
			}
			continue
		}
		if !info.ModTime().Before(cutoff) {
			continue
		}
		path := filepath.Join(cacheDir, e.Name())
		if rmErr := os.Remove(path); rmErr != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("remove %s: %w", e.Name(), rmErr)
			}
			continue
		}
		removed++
	}
	return removed, considered, firstErr
}

func hasEvictablePrefix(name string) bool {
	for _, p := range evictableCachePrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// defaultLoomCacheDir returns the conventional path
// ~/.cache/loom/. Falls back to TMPDIR if HOME is unset (e.g. in some
// CI environments).
func defaultLoomCacheDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".cache", "loom")
	}
	if tmp := os.Getenv("TMPDIR"); tmp != "" {
		return filepath.Join(tmp, "loom-cache")
	}
	return "/tmp/loom-cache"
}
