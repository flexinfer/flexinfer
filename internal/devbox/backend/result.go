package backend

import "strings"

// ExecResult holds the structured output of a command execution.
type ExecResult struct {
	ExitCode    int    `json:"exit_code"`
	StdoutLines int    `json:"stdout_lines"`
	StderrLines int    `json:"stderr_lines"`
	StdoutTail  string `json:"stdout_tail"`
	StderrTail  string `json:"stderr_tail,omitempty"`
	DurationMs  int64  `json:"duration_ms"`
	Truncated   bool   `json:"truncated"`
	OOMKilled   bool   `json:"oom_killed,omitempty"`
}

// TruncateOutput keeps only the last maxLines lines from output.
// Returns the truncated string, total line count, and whether truncation occurred.
func TruncateOutput(output string, maxLines int) (string, int, bool) {
	if output == "" {
		return "", 0, false
	}

	lines := strings.Split(output, "\n")
	total := len(lines)

	// Remove trailing empty line from Split
	if total > 0 && lines[total-1] == "" {
		lines = lines[:total-1]
		total = len(lines)
	}

	if maxLines <= 0 || total <= maxLines {
		return strings.Join(lines, "\n"), total, false
	}

	tail := lines[total-maxLines:]
	return strings.Join(tail, "\n"), total, true
}
