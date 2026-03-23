package strutil

import "encoding/json"

// ExtractEmbeddedJSON extracts the first valid JSON object or array from text
// that may contain prose or markdown code fences.
func ExtractEmbeddedJSON(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, ErrNoEmbeddedJSON
	}

	trimmed := trimSpaceBytes(data)
	if len(trimmed) == 0 {
		return nil, ErrNoEmbeddedJSON
	}

	// Fast path: entire input is valid JSON.
	var value any
	if err := json.Unmarshal(trimmed, &value); err == nil {
		return json.Marshal(value)
	}

	// Scan for the first '{' or '[' and try to extract a valid JSON value.
	for i, b := range trimmed {
		if b != '{' && b != '[' {
			continue
		}
		if end, ok := findJSONEnd(trimmed, i); ok {
			candidate := trimmed[i:end]
			if err := json.Unmarshal(candidate, &value); err == nil {
				return json.Marshal(value)
			}
		}
	}

	return nil, ErrNoEmbeddedJSON
}

// DecodeEmbeddedJSON extracts the first valid JSON object or array from text
// and unmarshals it into dst.
func DecodeEmbeddedJSON(data []byte, dst any) error {
	raw, err := ExtractEmbeddedJSON(data)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}

// ErrNoEmbeddedJSON is returned when no valid JSON object or array is found.
var ErrNoEmbeddedJSON = &embeddedJSONError{msg: "no valid embedded JSON object or array found"}

type embeddedJSONError struct{ msg string }

func (e *embeddedJSONError) Error() string { return e.msg }

func trimSpaceBytes(b []byte) []byte {
	start, end := 0, len(b)
	for start < end && (b[start] == ' ' || b[start] == '\t' || b[start] == '\n' || b[start] == '\r') {
		start++
	}
	for end > start && (b[end-1] == ' ' || b[end-1] == '\t' || b[end-1] == '\n' || b[end-1] == '\r') {
		end--
	}
	return b[start:end]
}

func findJSONEnd(data []byte, start int) (int, bool) {
	var stack []byte
	switch data[start] {
	case '{':
		stack = append(stack, '}')
	case '[':
		stack = append(stack, ']')
	default:
		return 0, false
	}

	inString := false
	escape := false
	for i := start + 1; i < len(data); i++ {
		b := data[i]
		if inString {
			if escape {
				escape = false
				continue
			}
			if b == '\\' {
				escape = true
				continue
			}
			if b == '"' {
				inString = false
			}
			continue
		}
		switch b {
		case '"':
			inString = true
		case '{':
			stack = append(stack, '}')
		case '[':
			stack = append(stack, ']')
		case '}', ']':
			if len(stack) == 0 || stack[len(stack)-1] != b {
				return 0, false
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				return i + 1, true
			}
		}
	}
	return 0, false
}
