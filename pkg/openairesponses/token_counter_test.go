package openairesponses

import (
	"encoding/json"
	"testing"
)

func TestEstimateInputTokens_ByteAndRawMessageParity(t *testing.T) {
	t.Parallel()

	payload := []byte(`{"message":"hello","count":3}`)
	model := "gpt-4o"

	bytesTokens := estimateInputTokens(model, payload)
	rawTokens := estimateInputTokens(model, json.RawMessage(payload))
	stringTokens := estimateInputTokens(model, string(payload))

	if bytesTokens <= 0 {
		t.Fatalf("bytes tokens = %d, want > 0", bytesTokens)
	}
	if rawTokens != bytesTokens {
		t.Fatalf("raw tokens = %d, want %d", rawTokens, bytesTokens)
	}
	if stringTokens != bytesTokens {
		t.Fatalf("string tokens = %d, want %d", stringTokens, bytesTokens)
	}
}
