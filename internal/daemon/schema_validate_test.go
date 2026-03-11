package daemon

import (
	"encoding/json"
	"testing"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testMsg creates a minimal MCP message for pipeline tests.
func testMsg() *mcp.Message {
	return &mcp.Message{JSONRPC: mcp.JSONRPCVersion, ID: json.RawMessage(`1`)}
}

func TestCheckPropertyType(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		val     any
		schema  any
		wantErr bool
	}{
		{name: "string_valid", key: "path", val: "foo", schema: map[string]any{"type": "string"}},
		{name: "string_invalid", key: "path", val: 42.0, schema: map[string]any{"type": "string"}, wantErr: true},
		{name: "number_valid", key: "count", val: 42.0, schema: map[string]any{"type": "number"}},
		{name: "number_invalid", key: "count", val: "abc", schema: map[string]any{"type": "number"}, wantErr: true},
		{name: "integer_valid", key: "n", val: 5.0, schema: map[string]any{"type": "integer"}},
		{name: "boolean_valid", key: "flag", val: true, schema: map[string]any{"type": "boolean"}},
		{name: "boolean_invalid", key: "flag", val: "yes", schema: map[string]any{"type": "boolean"}, wantErr: true},
		{name: "array_valid", key: "items", val: []any{"a"}, schema: map[string]any{"type": "array"}},
		{name: "object_valid", key: "opts", val: map[string]any{"k": "v"}, schema: map[string]any{"type": "object"}},
		{name: "no_type_schema", key: "x", val: "any", schema: map[string]any{}},
		{name: "non_map_schema", key: "x", val: "any", schema: "invalid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := checkPropertyType(tt.key, tt.val, tt.schema)
			if tt.wantErr {
				assert.NotEmpty(t, result, "expected type violation")
			} else {
				assert.Empty(t, result, "unexpected type violation: %s", result)
			}
		})
	}
}

func TestValidateInputSchema_Off(t *testing.T) {
	d := &Daemon{
		fileCfg:   FileConfig{SchemaValidation: SchemaValidationConfig{Mode: SchemaValidateOff}},
		toolCache: &ToolCache{},
	}
	p := &callPipeline{
		daemon:     d,
		msg:        testMsg(),
		serverName: "test",
		toolName:   "echo",
		params:     callParams{Arguments: json.RawMessage(`{"msg":"hello"}`)},
	}
	resp := p.validateInputSchema()
	assert.Nil(t, resp, "off mode should skip validation")
}

func TestValidateInputSchema_Strict_Pass(t *testing.T) {
	d := &Daemon{
		fileCfg: FileConfig{SchemaValidation: SchemaValidationConfig{Mode: SchemaValidateStrict}},
		toolCache: &ToolCache{
			tools: []mcp.Tool{
				{
					Name: "test__echo",
					InputSchema: mcp.InputSchema{
						Type: "object",
						Properties: map[string]any{
							"msg": map[string]any{"type": "string"},
						},
						Required: []string{"msg"},
					},
				},
			},
		},
	}
	p := &callPipeline{
		daemon:     d,
		msg:        testMsg(),
		serverName: "test",
		toolName:   "echo",
		params:     callParams{Arguments: json.RawMessage(`{"msg":"hello"}`)},
	}
	resp := p.validateInputSchema()
	assert.Nil(t, resp, "valid args should pass")
}

func TestValidateInputSchema_Strict_MissingRequired(t *testing.T) {
	d := &Daemon{
		fileCfg: FileConfig{SchemaValidation: SchemaValidationConfig{Mode: SchemaValidateStrict}},
		toolCache: &ToolCache{
			tools: []mcp.Tool{
				{
					Name: "test__echo",
					InputSchema: mcp.InputSchema{
						Type:     "object",
						Required: []string{"msg"},
					},
				},
			},
		},
	}
	p := &callPipeline{
		daemon:     d,
		msg:        testMsg(),
		serverName: "test",
		toolName:   "echo",
		params:     callParams{Arguments: json.RawMessage(`{}`)},
	}
	resp := p.validateInputSchema()
	require.NotNil(t, resp, "missing required field should fail in strict mode")
}

func TestValidateInputSchema_Warn_MissingRequired(t *testing.T) {
	d := &Daemon{
		fileCfg: FileConfig{SchemaValidation: SchemaValidationConfig{Mode: SchemaValidateWarn}},
		toolCache: &ToolCache{
			tools: []mcp.Tool{
				{
					Name: "test__echo",
					InputSchema: mcp.InputSchema{
						Type:     "object",
						Required: []string{"msg"},
					},
				},
			},
		},
	}
	p := &callPipeline{
		daemon:     d,
		msg:        testMsg(),
		serverName: "test",
		toolName:   "echo",
		params:     callParams{Arguments: json.RawMessage(`{}`)},
	}
	resp := p.validateInputSchema()
	assert.Nil(t, resp, "warn mode should allow call to proceed")
}

func TestValidateInputSchema_NoSchema(t *testing.T) {
	d := &Daemon{
		fileCfg: FileConfig{SchemaValidation: SchemaValidationConfig{Mode: SchemaValidateStrict}},
		toolCache: &ToolCache{
			tools: []mcp.Tool{}, // No matching tool
		},
	}
	p := &callPipeline{
		daemon:     d,
		msg:        testMsg(),
		serverName: "test",
		toolName:   "echo",
		params:     callParams{Arguments: json.RawMessage(`{"any":"thing"}`)},
	}
	resp := p.validateInputSchema()
	assert.Nil(t, resp, "missing schema should skip validation")
}

func TestValidateInputSchema_WrongType(t *testing.T) {
	d := &Daemon{
		fileCfg: FileConfig{SchemaValidation: SchemaValidationConfig{Mode: SchemaValidateStrict}},
		toolCache: &ToolCache{
			tools: []mcp.Tool{
				{
					Name: "test__read",
					InputSchema: mcp.InputSchema{
						Type: "object",
						Properties: map[string]any{
							"path":  map[string]any{"type": "string"},
							"lines": map[string]any{"type": "integer"},
						},
						Required: []string{"path"},
					},
				},
			},
		},
	}
	p := &callPipeline{
		daemon:     d,
		msg:        testMsg(),
		serverName: "test",
		toolName:   "read",
		params:     callParams{Arguments: json.RawMessage(`{"path":123}`)},
	}
	resp := p.validateInputSchema()
	require.NotNil(t, resp, "wrong type should fail in strict mode")
}
