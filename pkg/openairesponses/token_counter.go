package openairesponses

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"

	"gitlab.flexinfer.ai/libs/fi-accel/go/fiaccel"
)

const tokenCountCacheSize = 1024
const exactTokenThresholdBytes = 256

var globalTokenCounter = newTokenCounter()

type tokenCounter struct {
	tokenizers sync.Map
	cacheMu    sync.Mutex
	cacheOrder []string
	cache      map[string]int
}

func newTokenCounter() *tokenCounter {
	return &tokenCounter{
		cache: make(map[string]int, tokenCountCacheSize),
	}
}

func normalizeTokenizerModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return "gpt-4"
	}
	return model
}

func (c *tokenCounter) tokenizer(model string) fiaccel.Tokenizer {
	model = normalizeTokenizerModel(model)
	if tok, ok := c.tokenizers.Load(model); ok {
		if tokenizer, ok := tok.(fiaccel.Tokenizer); ok {
			return tokenizer
		}
	}
	tok, err := fiaccel.NewTokenizer(model)
	if err != nil {
		return nil
	}
	actual, _ := c.tokenizers.LoadOrStore(model, tok)
	if tokenizer, ok := actual.(fiaccel.Tokenizer); ok {
		return tokenizer
	}
	return tok
}

func (c *tokenCounter) countBytes(model string, payload []byte) int {
	if len(payload) == 0 {
		return 0
	}
	if len(payload) < exactTokenThresholdBytes {
		return fallbackCharsToTokens(len(payload))
	}
	key := tokenCacheKey(model, payload)

	c.cacheMu.Lock()
	if tokens, ok := c.cache[key]; ok {
		c.cacheMu.Unlock()
		return tokens
	}
	c.cacheMu.Unlock()

	tokens := fallbackCharsToTokens(len(payload))
	if tok := c.tokenizer(model); tok != nil {
		if n, err := tok.Count(string(payload)); err == nil && n >= 0 {
			tokens = n
		}
	}

	c.cacheMu.Lock()
	if _, exists := c.cache[key]; !exists {
		if len(c.cacheOrder) >= tokenCountCacheSize {
			evict := c.cacheOrder[0]
			c.cacheOrder = c.cacheOrder[1:]
			delete(c.cache, evict)
		}
		c.cacheOrder = append(c.cacheOrder, key)
		c.cache[key] = tokens
	}
	c.cacheMu.Unlock()
	return tokens
}

func estimateTextTokens(model, text string) int {
	return globalTokenCounter.countBytes(model, []byte(text))
}

func estimateJSONTokens(model string, v any) int {
	if v == nil {
		return 0
	}
	data, err := json.Marshal(v)
	if err != nil {
		return 100
	}
	return globalTokenCounter.countBytes(model, data)
}

func estimateInputTokens(model string, input any) int {
	if input == nil {
		return 0
	}

	switch v := input.(type) {
	case string:
		return estimateTextTokens(model, v)
	case []byte:
		return globalTokenCounter.countBytes(model, v)
	case json.RawMessage:
		return globalTokenCounter.countBytes(model, []byte(v))
	case []ToolResult:
		total := 0
		for _, r := range v {
			total += estimateToolResultTokens(model, r)
		}
		return total
	default:
		return estimateJSONTokens(model, v)
	}
}

func estimateToolTokens(model string, t ToolDefinition) int {
	tokens := estimateTextTokens(model, t.Name+" "+t.Description)
	if t.InputSchema != nil {
		data, err := json.Marshal(t.InputSchema)
		if err == nil {
			tokens += globalTokenCounter.countBytes(model, data)
		}
	}
	return tokens + 10
}

func estimateToolResultTokens(model string, r ToolResult) int {
	tokens := 20 + estimateTextTokens(model, r.CallID)
	if r.IsError {
		return tokens + estimateTextTokens(model, r.ErrorText)
	}

	if r.Output == nil {
		return tokens
	}
	switch v := r.Output.(type) {
	case string:
		return tokens + estimateTextTokens(model, v)
	case []byte:
		return tokens + globalTokenCounter.countBytes(model, v)
	case json.RawMessage:
		return tokens + globalTokenCounter.countBytes(model, []byte(v))
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return tokens + 100
		}
		return tokens + globalTokenCounter.countBytes(model, data)
	}
}

func tokenCacheKey(model string, payload []byte) string {
	sum := sha256.Sum256(payload)
	return normalizeTokenizerModel(model) + ":" + hex.EncodeToString(sum[:])
}

func fallbackCharsToTokens(chars int) int {
	if chars <= 0 {
		return 0
	}
	return (chars + 3) / 4
}
