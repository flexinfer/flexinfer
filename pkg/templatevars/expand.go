// Package templatevars provides shared template variable expansion for MCP configs.
// It resolves ${env:VAR}, ${env:VAR:-default}, ${keychain:VAR}, and ${secret:VAR}
// patterns using the registry's env alias fallbacks and the secrets manager.
//
// This logic was extracted from internal/daemon/daemon.go to allow reuse during
// config generation (for platforms like Codex that lack runtime resolvers).
package templatevars

import (
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/crb2nu/loom/pkg/registry"
	"github.com/crb2nu/loom/pkg/secrets"
)

// Expander resolves template variable patterns in strings.
type Expander struct {
	registry   *registry.Registry
	secretsMgr *secrets.Manager
	lazyInit   sync.Once
	lazy       bool // if true, defer secrets.DefaultManager() to first use
}

// Option configures an Expander.
type Option func(*Expander)

// WithRegistry provides a registry for env alias fallback resolution.
func WithRegistry(reg *registry.Registry) Option {
	return func(e *Expander) {
		e.registry = reg
	}
}

// WithSecretsManager provides an explicit secrets manager.
func WithSecretsManager(mgr *secrets.Manager) Option {
	return func(e *Expander) {
		e.secretsMgr = mgr
	}
}

// WithLazySecrets defers secrets.DefaultManager() initialization until first use.
func WithLazySecrets() Option {
	return func(e *Expander) {
		e.lazy = true
	}
}

// New creates an Expander with the given options.
// It does NOT resolve ${repo} or ${HOME} — those are handled by generator.ResolveTokens.
func New(opts ...Option) *Expander {
	e := &Expander{}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// resolveEnv resolves an environment variable with registry alias fallbacks.
func (e *Expander) resolveEnv(name string) string {
	if e.registry != nil {
		return e.registry.GetEnvWithFallback(name)
	}
	return os.Getenv(name)
}

// resolveSecret resolves a secret using the secrets manager.
func (e *Expander) resolveSecret(key string) string {
	mgr := e.getSecretsManager()
	if mgr == nil {
		return ""
	}
	val := mgr.GetValue(key)
	if val == "" {
		slog.Debug("secret not found", "key", key)
	} else {
		slog.Debug("secret resolved", "key", key, "length", len(val))
	}
	return val
}

// getSecretsManager returns the secrets manager, lazily initializing if configured.
func (e *Expander) getSecretsManager() *secrets.Manager {
	if e.secretsMgr != nil {
		return e.secretsMgr
	}
	if !e.lazy {
		return nil
	}
	e.lazyInit.Do(func() {
		mgr, err := secrets.DefaultManager()
		if err != nil {
			slog.Debug("failed to initialize secrets manager", "error", err)
			return
		}
		e.secretsMgr = mgr
	})
	return e.secretsMgr
}

// Expand resolves ${env:VAR}, ${env:VAR:-default}, ${keychain:VAR}, and
// ${secret:VAR} patterns in s. It does NOT touch ${repo} or ${HOME}.
func (e *Expander) Expand(s string) string {
	// Expand ${env:VAR} and ${env:VAR:-default} patterns
	s = e.expandEnv(s)

	// Expand ${keychain:VAR} patterns
	s = e.expandKeychain(s)

	// Expand ${secret:VAR} patterns
	s = e.expandSecret(s)

	return s
}

func (e *Expander) expandEnv(s string) string {
	for {
		start := strings.Index(s, "${env:")
		if start == -1 {
			break
		}
		end := strings.Index(s[start:], "}")
		if end == -1 {
			break
		}
		end += start
		varExpr := s[start+len("${env:") : end]

		var varName, defaultVal string
		if idx := strings.Index(varExpr, ":-"); idx != -1 {
			varName = varExpr[:idx]
			defaultVal = varExpr[idx+2:]
		} else {
			varName = varExpr
		}

		value := e.resolveEnv(varName)
		if value == "" {
			value = defaultVal
		}
		s = s[:start] + value + s[end+1:]
	}
	return s
}

func (e *Expander) expandKeychain(s string) string {
	for {
		start := strings.Index(s, "${keychain:")
		if start == -1 {
			break
		}
		end := strings.Index(s[start:], "}")
		if end == -1 {
			break
		}
		end += start
		varName := s[start+len("${keychain:") : end]

		// Try secrets manager first, fall back to env
		value := e.resolveSecret(varName)
		if value == "" {
			value = e.resolveEnv(varName)
		}
		s = s[:start] + value + s[end+1:]
	}
	return s
}

func (e *Expander) expandSecret(s string) string {
	for {
		start := strings.Index(s, "${secret:")
		if start == -1 {
			break
		}
		end := strings.Index(s[start:], "}")
		if end == -1 {
			break
		}
		end += start
		varName := s[start+len("${secret:") : end]

		value := e.resolveSecret(varName)
		s = s[:start] + value + s[end+1:]
	}
	return s
}

// ExpandMap applies Expand to all values in a map, returning a new map.
func (e *Expander) ExpandMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = e.Expand(v)
	}
	return out
}
