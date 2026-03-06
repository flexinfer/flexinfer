package bridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
)

// UnmarshalToolResult decodes a daemon tools/call response into target.
// It supports both raw JSON payloads and standard MCP CallToolResult envelopes
// whose text payload may be JSON or TOON.
func UnmarshalToolResult(raw json.RawMessage, target any) error {
	if target == nil || raw == nil {
		return nil
	}

	var envelope mcp.CallToolResult
	if err := json.Unmarshal(raw, &envelope); err == nil && (len(envelope.Content) > 0 || envelope.IsError) {
		if envelope.IsError {
			return toolEnvelopeError(envelope)
		}
		text, err := firstToolText(envelope)
		if err != nil {
			return err
		}
		return unmarshalToolText(text, target)
	}

	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("unmarshal tool result: %w", err)
	}
	return nil
}

// ParseToolResultMap decodes a daemon tools/call response into a generic map.
func ParseToolResultMap(raw json.RawMessage) (map[string]any, error) {
	if raw == nil {
		return map[string]any{}, nil
	}

	var result map[string]any
	if err := UnmarshalToolResult(raw, &result); err != nil {
		return nil, err
	}
	if result == nil {
		return map[string]any{}, nil
	}
	return result, nil
}

func firstToolText(envelope mcp.CallToolResult) (string, error) {
	for _, content := range envelope.Content {
		if content.Type == "text" && strings.TrimSpace(content.Text) != "" {
			return content.Text, nil
		}
	}
	return "", fmt.Errorf("tool result did not contain text content")
}

func toolEnvelopeError(envelope mcp.CallToolResult) error {
	for _, content := range envelope.Content {
		if content.Type == "text" && strings.TrimSpace(content.Text) != "" {
			return errors.New(strings.TrimSpace(content.Text))
		}
	}
	return errors.New("tool returned error")
}

func unmarshalToolText(text string, target any) error {
	if err := json.Unmarshal([]byte(text), target); err == nil {
		return nil
	}

	jsonBytes, err := mcp.DecodeTOONToJSON(text)
	if err != nil {
		return fmt.Errorf("decode tool text: %w", err)
	}
	if err := json.Unmarshal(jsonBytes, target); err != nil {
		return fmt.Errorf("unmarshal decoded tool text: %w", err)
	}
	return nil
}
