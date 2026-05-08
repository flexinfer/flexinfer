// env_file.go parses simple KEY=VALUE env files for runtime reload of
// fields like HUD_ADMIN_TOKEN that are otherwise frozen at process start
// (since launchd does not push EnvironmentVariables updates into a
// running process).
package daemon

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// parseEnvFile reads a KEY=VALUE env file. Lines beginning with '#' are
// comments. Surrounding double or single quotes around values are stripped.
// Empty values are kept (caller decides whether to skip).
//
// Format is the lowest-common-denominator launchd / systemd EnvironmentFile
// shape — the same format already shipped at ~/.config/loom/hud.env. We do
// NOT support shell expansion ($VAR), continuation, or `export` prefixes;
// the file is data, not a script.
func parseEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path) //nolint:gosec // path is operator-controlled config location
	if err != nil {
		return nil, fmt.Errorf("open env file: %w", err)
	}
	defer f.Close() //nolint:errcheck // read-only

	out := make(map[string]string)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024) // tolerate long token values
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Allow `export KEY=VALUE` for compatibility with shell-style env files.
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			return nil, fmt.Errorf("line %d: missing '=' in %q", lineNo, line)
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		val = stripMatchingQuotes(val)
		out[key] = val
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read env file: %w", err)
	}
	return out, nil
}

func stripMatchingQuotes(s string) string {
	if len(s) >= 2 {
		first, last := s[0], s[len(s)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
