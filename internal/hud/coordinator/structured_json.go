package coordinator

import (
	"encoding/json"
	"fmt"

	"github.com/crb2nu/loom/pkg/strutil"
)

func decodeStructuredJSON[T any](raw string, dst *T) error {
	if err := strutil.DecodeEmbeddedJSON([]byte(raw), dst); err == nil {
		return nil
	}

	// Preserve the pre-fi-accel path as a fallback in case upstream responses
	// contain something the native extractor rejects.
	cleaned := stripCodeFence(raw)
	if err := json.Unmarshal([]byte(cleaned), dst); err != nil {
		return fmt.Errorf("parse structured JSON: %w", err)
	}
	return nil
}
