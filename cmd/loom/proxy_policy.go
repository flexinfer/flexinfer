package main

import (
	"encoding/json"
	"regexp"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

const proxyFluxFirstPolicyMessage = "GitOps policy: kubectl edit and kubectl set env are blocked in loom proxy. Edit the manifest in Git, commit and push the change, then run flux reconcile."

var proxyUnsafeKubectlCommandPattern = regexp.MustCompile(`(?i)\bkubectl\b(?:\s+\S+)*\s+(?:edit\b|set\s+env\b)`)

// proxyFluxPolicyResponse inspects a tool call request and blocks imperative
// kubectl edit/set env flows before the daemon sees them.
func proxyFluxPolicyResponse(msg *mcp.Message) (*mcp.Message, bool) {
	if msg == nil {
		return nil, false
	}

	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments,omitempty"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return nil, false
	}

	if !proxyContainsUnsafeKubectlCommand(params.Name) && !proxyContainsUnsafeKubectlCommandJSON(params.Arguments) {
		return nil, false
	}

	return mcp.NewErrorResponse(msg.ID, mcp.InvalidParams, proxyFluxFirstPolicyMessage), true
}

func proxyContainsUnsafeKubectlCommandJSON(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}

	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return proxyContainsUnsafeKubectlCommand(string(raw))
	}
	return proxyContainsUnsafeKubectlCommand(decoded)
}

func proxyContainsUnsafeKubectlCommand(v any) bool {
	switch typed := v.(type) {
	case string:
		return proxyUnsafeKubectlCommandPattern.MatchString(strings.Join(strings.Fields(typed), " "))
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			switch child := item.(type) {
			case string:
				parts = append(parts, child)
			default:
				if proxyContainsUnsafeKubectlCommand(child) {
					return true
				}
			}
		}
		if proxyUnsafeKubectlCommandPattern.MatchString(strings.Join(parts, " ")) {
			return true
		}
		for _, item := range typed {
			if proxyContainsUnsafeKubectlCommand(item) {
				return true
			}
		}
		return false
	case map[string]any:
		for _, item := range typed {
			if proxyContainsUnsafeKubectlCommand(item) {
				return true
			}
		}
		return false
	default:
		return false
	}
}
