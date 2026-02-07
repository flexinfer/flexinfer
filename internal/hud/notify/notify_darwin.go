//go:build darwin

// Package notify provides macOS native notification support via osascript.
package notify

import (
	"fmt"
	"os/exec"
	"strings"
)

// Notify sends a macOS notification with the given title, subtitle, and message.
func Notify(title, subtitle, message string) error {
	return sendNotification(title, subtitle, message, "")
}

// NotifyWithSound sends a macOS notification with a sound.
// Common sound names: "Glass", "Ping", "Pop", "Purr", "Sosumi", "Tink", "Hero".
func NotifyWithSound(title, subtitle, message, sound string) error {
	return sendNotification(title, subtitle, message, sound)
}

// NotifyWorkflowApproval sends a notification for a workflow approval request.
func NotifyWorkflowApproval(workflowName, stepName string) error {
	return NotifyWithSound(
		"Workflow Approval Required",
		workflowName,
		fmt.Sprintf("Step \"%s\" is waiting for approval", stepName),
		"Glass",
	)
}

// NotifyServerDown sends a notification when a server goes down.
func NotifyServerDown(serverName string) error {
	return NotifyWithSound(
		"Server Down",
		serverName,
		fmt.Sprintf("MCP server \"%s\" is unhealthy", serverName),
		"Sosumi",
	)
}

// NotifyHandoff sends a notification for an agent handoff.
func NotifyHandoff(sourceAgent, targetAgent, instructions string) error {
	msg := fmt.Sprintf("From %s: %s", sourceAgent, instructions)
	if len(msg) > 200 {
		msg = msg[:197] + "..."
	}
	return NotifyWithSound(
		"Agent Handoff",
		fmt.Sprintf("%s -> %s", sourceAgent, targetAgent),
		msg,
		"Ping",
	)
}

// sendNotification executes osascript to display a macOS notification.
func sendNotification(title, subtitle, message, sound string) error {
	// Escape double quotes in all user-provided strings.
	title = escapeAppleScript(title)
	subtitle = escapeAppleScript(subtitle)
	message = escapeAppleScript(message)
	sound = escapeAppleScript(sound)

	var script strings.Builder
	script.WriteString(fmt.Sprintf(`display notification "%s"`, message))
	script.WriteString(fmt.Sprintf(` with title "%s"`, title))

	if subtitle != "" {
		script.WriteString(fmt.Sprintf(` subtitle "%s"`, subtitle))
	}

	if sound != "" {
		script.WriteString(fmt.Sprintf(` sound name "%s"`, sound))
	}

	cmd := exec.Command("osascript", "-e", script.String())
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("osascript failed: %w (output: %s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// escapeAppleScript escapes characters that are special in AppleScript strings.
func escapeAppleScript(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}
