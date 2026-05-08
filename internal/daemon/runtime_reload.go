// runtime_reload.go ties the env-file parser and cache-eviction sweep
// into the daemon lifecycle. Both are best-effort: a missing env file
// or an unwritable cache directory must never abort startup or reload.
package daemon

import (
	"context"
	"errors"
	"os"
	"time"
)

// reloadableEnvKeys is the allowlist of keys runtime_reload propagates
// from the env file into the running process and downstream subsystems.
// The list is intentionally narrow — env files are operator-controlled
// but we don't want a typo'd key to silently shadow some other setting.
var reloadableEnvKeys = []string{
	"HUD_ADMIN_TOKEN",
	"LOOM_HUD_ADMIN_TOKEN",
}

// reloadEnvFile reads d.cfg.EnvFilePath (when set) and pushes the
// allowlisted keys into both the process environment (so any code path
// that reads via os.Getenv picks them up on the next access) and into
// the embedded HUD app (so the active requireAdminToken middleware
// switches over without a restart).
//
// Returns nil when EnvFilePath is unset or the file is missing; the
// daemon treats env-file reload as opt-in.
func (d *Daemon) reloadEnvFile() error {
	if d.cfg.EnvFilePath == "" {
		return nil
	}
	parsed, err := parseEnvFile(d.cfg.EnvFilePath)
	if err != nil {
		// Missing-file is a no-op; let the caller decide on other errors.
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, key := range reloadableEnvKeys {
		val, ok := parsed[key]
		if !ok {
			continue
		}
		if setErr := os.Setenv(key, val); setErr != nil {
			d.logger.Warn("env reload: setenv failed", "key", key, "error", setErr)
			continue
		}
		// HUD admin token gets pushed into the running HUD's atomic
		// override — bypass the launchd plist staleness window.
		if (key == "HUD_ADMIN_TOKEN" || key == "LOOM_HUD_ADMIN_TOKEN") && d.hudApp != nil {
			d.hudApp.SetAdminToken(val)
		}
	}
	return nil
}

// cacheEvictionMaxAgeOrDefault returns the configured staleness
// threshold or 14 days when zero. Negative means disabled and the
// caller should skip the sweep.
func (d *Daemon) cacheEvictionMaxAgeOrDefault() time.Duration {
	if d.cfg.CacheEvictionMaxAge < 0 {
		return -1
	}
	if d.cfg.CacheEvictionMaxAge == 0 {
		return 14 * 24 * time.Hour
	}
	return d.cfg.CacheEvictionMaxAge
}

// cacheEvictionIntervalOrDefault returns the configured interval or 1
// hour when zero. Negative disables the periodic loop (startup sweep
// still runs).
func (d *Daemon) cacheEvictionIntervalOrDefault() time.Duration {
	if d.cfg.CacheEvictionInterval < 0 {
		return -1
	}
	if d.cfg.CacheEvictionInterval == 0 {
		return 1 * time.Hour
	}
	return d.cfg.CacheEvictionInterval
}

// runCacheEviction performs a single eviction sweep and logs the
// outcome. Errors are reported at warn level (the daemon must keep
// running even if the cache dir is unwritable).
func (d *Daemon) runCacheEviction() {
	maxAge := d.cacheEvictionMaxAgeOrDefault()
	if maxAge < 0 {
		return
	}
	dir := d.cfg.CacheDir
	if dir == "" {
		dir = defaultLoomCacheDir()
	}
	removed, considered, err := EvictAgentCache(dir, maxAge, time.Now())
	if err != nil {
		d.logger.Warn("cache eviction encountered errors", "dir", dir, "error", err, "removed", removed, "considered", considered)
		return
	}
	if removed > 0 || considered > 0 {
		d.logger.Info("cache eviction swept", "dir", dir, "removed", removed, "considered", considered, "max_age", maxAge.String())
	}
}

// cacheEvictionLoop runs a startup sweep then a periodic sweep on
// cacheEvictionIntervalOrDefault(). Exits cleanly when ctx is cancelled
// or the daemon enters shutdown.
func (d *Daemon) cacheEvictionLoop(ctx context.Context) {
	d.runCacheEviction()

	interval := d.cacheEvictionIntervalOrDefault()
	if interval < 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-d.done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.runCacheEviction()
		}
	}
}
