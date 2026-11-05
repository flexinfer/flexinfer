package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

// Top-level JSON field edits that leave every other byte of a request body
// untouched.
//
// The proxy rewrites three top-level fields on the way to a lane: "model"
// (alias to served name), "max_tokens" (context-window clamp) and "priority"
// (QoS class on a vLLM priority lane). Re-encoding the body through
// map[string]any for those edits sorts every nested object's keys and
// HTML-escapes strings. That is harmless for a chat history, but not for a
// structured-output request: xgrammar emits object fields in the order a
// response_format.json_schema (or a tool's parameters schema) declares them,
// so a sorted schema changes what the lane generates. Splicing the one value
// keeps response_format, structured_outputs, tools and messages byte-exact on
// every forward, reroute and replay.

// topLevelField locates key in the top-level object of body and returns the
// byte span [start, end) of its value. found is false when body is an object
// without the key; ok is false when body is not exactly one JSON object.
// A repeated key resolves to its last occurrence, as encoding/json and
// Python's json module (vLLM) both read it.
func topLevelField(body []byte, key string) (start, end int, found, ok bool) {
	dec := json.NewDecoder(bytes.NewReader(body))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return 0, 0, false, false
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return 0, 0, false, false
		}
		name, isString := keyTok.(string)
		if !isString {
			return 0, 0, false, false
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return 0, 0, false, false
		}
		if name == key {
			end = int(dec.InputOffset())
			start = end - len(raw)
			found = true
		}
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return 0, 0, false, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return 0, 0, false, false
	}
	return start, end, found, true
}

// topLevelValue returns the raw JSON value of a top-level key.
func topLevelValue(body []byte, key string) (json.RawMessage, bool) {
	start, end, found, ok := topLevelField(body, key)
	if !ok || !found {
		return nil, false
	}
	return json.RawMessage(body[start:end]), true
}

// setTopLevelField returns body with the top-level key set to value, a raw
// JSON value. An existing value is replaced in place; a missing key is
// appended before the closing brace. Every other byte is preserved. ok is
// false, and body is returned unchanged, when body is not one JSON object.
func setTopLevelField(body []byte, key string, value []byte) ([]byte, bool) {
	start, end, found, ok := topLevelField(body, key)
	if !ok {
		return body, false
	}
	if found {
		out := make([]byte, 0, len(body)-(end-start)+len(value))
		out = append(out, body[:start]...)
		out = append(out, value...)
		return append(out, body[end:]...), true
	}
	closing := bytes.LastIndexByte(body, '}')
	head := bytes.TrimRight(body[:closing], " \t\r\n")
	quotedKey, _ := json.Marshal(key)
	out := make([]byte, 0, len(body)+len(quotedKey)+len(value)+2)
	out = append(out, head...)
	if head[len(head)-1] != '{' {
		out = append(out, ',')
	}
	out = append(out, quotedKey...)
	out = append(out, ':')
	out = append(out, value...)
	return append(out, body[len(head):]...), true
}

// Schema-constraint kinds, the value of the usage log's `constraint` field and
// the `constraint` label of flexinfer_proxy_schema_constrained_requests_total.
// vLLM enforces each with its structured-output backend (xgrammar or guidance;
// named and required tool_choice compile the tool schemas the same way).
const (
	constraintStructuredOutputsPrefix = "structured_outputs."
	constraintResponseFormatPrefix    = "response_format."
	constraintTextFormatPrefix        = "text.format."
	constraintToolChoiceRequired      = "tool_choice.required"
	constraintToolChoiceFunction      = "tool_choice.function"
)

// structuredOutputsKinds are the vLLM structured_outputs members that
// constrain decoding, in the order the tag reports the first one present.
var structuredOutputsKinds = []string{"json", "regex", "choice", "grammar", "structural_tag"}

// requestConstraint reports how a chat, completions or responses request body
// constrains its output, or "" when it does not. The proxy only tags the
// request; it never edits these fields.
//
//   - structured_outputs.{json,regex,choice,grammar,structural_tag}: vLLM's own field
//   - response_format.{json_schema,json_object,structural_tag}: chat and completions
//   - text.format.{json_schema,json_object}: the Responses API
//   - tool_choice.required, tool_choice.function: forced tool calls
func requestConstraint(path string, body []byte) string {
	if len(body) == 0 || !isConstrainablePath(path) {
		return ""
	}
	var probe struct {
		StructuredOutputs map[string]json.RawMessage `json:"structured_outputs"`
		ResponseFormat    *struct {
			Type string `json:"type"`
		} `json:"response_format"`
		Text *struct {
			Format *struct {
				Type string `json:"type"`
			} `json:"format"`
		} `json:"text"`
		ToolChoice json.RawMessage `json:"tool_choice"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return ""
	}
	for _, kind := range structuredOutputsKinds {
		if raw, ok := probe.StructuredOutputs[kind]; ok && !isJSONNull(raw) {
			return constraintStructuredOutputsPrefix + kind
		}
	}
	if rf := probe.ResponseFormat; rf != nil {
		switch rf.Type {
		case "json_schema", "json_object", "structural_tag":
			return constraintResponseFormatPrefix + rf.Type
		}
	}
	if probe.Text != nil && probe.Text.Format != nil {
		switch probe.Text.Format.Type {
		case "json_schema", "json_object":
			return constraintTextFormatPrefix + probe.Text.Format.Type
		}
	}
	if len(probe.ToolChoice) > 0 {
		var choice string
		if json.Unmarshal(probe.ToolChoice, &choice) == nil {
			if choice == "required" {
				return constraintToolChoiceRequired
			}
			return ""
		}
		var named struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(probe.ToolChoice, &named) == nil && named.Type == "function" {
			return constraintToolChoiceFunction
		}
	}
	return ""
}

// isConstrainablePath reports the OpenAI paths whose bodies may carry an
// output constraint: chat completions, completions and responses.
func isConstrainablePath(path string) bool {
	return isCompletionPath(path) || strings.HasSuffix(path, "/v1/responses")
}

func isJSONNull(raw json.RawMessage) bool {
	return string(bytes.TrimSpace(raw)) == "null"
}
