//go:build !darwin

// Package notify provides native notification support.
// This file is a stub for non-darwin platforms.
package notify

// Notify is a no-op on non-darwin platforms.
func Notify(title, subtitle, message string) error { return nil }

// NotifyWithSound is a no-op on non-darwin platforms.
func NotifyWithSound(title, subtitle, message, sound string) error { return nil }

// NotifyWorkflowApproval is a no-op on non-darwin platforms.
func NotifyWorkflowApproval(workflowName, stepName string) error { return nil }

// NotifyServerDown is a no-op on non-darwin platforms.
func NotifyServerDown(serverName string) error { return nil }

// NotifyHandoff is a no-op on non-darwin platforms.
func NotifyHandoff(sourceAgent, targetAgent, instructions string) error { return nil }
