package daemon

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// SchemaValidationMode controls how input schema validation failures are handled.
type SchemaValidationMode string

const (
	// SchemaValidateOff disables input schema validation (default).
	SchemaValidateOff SchemaValidationMode = "off"
	// SchemaValidateWarn logs a warning but allows the call to proceed.
	SchemaValidateWarn SchemaValidationMode = "warn"
	// SchemaValidateStrict rejects calls that fail input schema validation.
	SchemaValidateStrict SchemaValidationMode = "strict"
)

// SchemaValidationConfig controls input schema validation behavior.
type SchemaValidationConfig struct {
	// Mode controls behavior: "off", "warn", "strict". Default: "off".
	Mode SchemaValidationMode `yaml:"mode,omitempty"`
}

const stageValidate = "validate"

// validateInputSchema checks the tool call arguments against the tool's input schema.
// Returns nil to proceed, or an error response to short-circuit.
func (p *callPipeline) validateInputSchema() *mcp.Message {
	mode := p.daemon.fileCfg.SchemaValidation.Mode
	if mode == "" || mode == SchemaValidateOff {
		return nil
	}

	p.stage = stageValidate
	span := p.startStageSpan("daemon.pipeline.validate")
	defer span.End()

	// Find the tool in cache.
	tool := p.findToolInCache(p.serverName, p.toolName)
	if tool == nil {
		span.SetAttributes(attribute.String("validate.result", "skip_no_schema"))
		return nil // No schema available — skip validation.
	}

	schema := tool.InputSchema
	if schema.Type == "" && len(schema.Properties) == 0 {
		span.SetAttributes(attribute.String("validate.result", "skip_empty_schema"))
		return nil // Empty schema — nothing to validate.
	}

	// Parse the arguments.
	args := p.params.Arguments
	if len(args) == 0 {
		args = p.params.Params
	}
	var argMap map[string]any
	if len(args) > 0 {
		if err := json.Unmarshal(args, &argMap); err != nil {
			// Arguments aren't valid JSON — report as validation failure.
			return p.handleSchemaFailure(span, mode, "arguments are not valid JSON: "+err.Error())
		}
	}
	if argMap == nil {
		argMap = map[string]any{}
	}

	// Validate required fields.
	var violations []string
	for _, req := range schema.Required {
		if _, ok := argMap[req]; !ok {
			violations = append(violations, fmt.Sprintf("missing required field: %q", req))
		}
	}

	// Validate property types if schema has type info.
	for key, val := range argMap {
		propSchema, exists := schema.Properties[key]
		if !exists && len(schema.Properties) > 0 {
			violations = append(violations, fmt.Sprintf("unknown field: %q", key))
			continue
		}
		if exists {
			if typeViolation := checkPropertyType(key, val, propSchema); typeViolation != "" {
				violations = append(violations, typeViolation)
			}
		}
	}

	span.SetAttributes(
		attribute.Int("validate.violations", len(violations)),
		attribute.String("validate.mode", string(mode)),
	)

	if len(violations) == 0 {
		span.SetAttributes(attribute.String("validate.result", "pass"))
		return nil
	}

	detail := strings.Join(violations, "; ")
	return p.handleSchemaFailure(span, mode, detail)
}

// handleSchemaFailure processes a schema validation failure based on the configured mode.
func (p *callPipeline) handleSchemaFailure(span trace.Span, mode SchemaValidationMode, detail string) *mcp.Message {
	switch mode {
	case SchemaValidateStrict:
		err := fmt.Errorf("schema validation failed for %s/%s: %s", p.serverName, p.toolName, detail)
		span.SetAttributes(attribute.String("validate.result", "reject"))
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return p.invalidParamsError(err.Error())
	case SchemaValidateWarn:
		span.SetAttributes(attribute.String("validate.result", "warn"))
		slog.Warn("schema validation warning",
			"server", p.serverName,
			"tool", p.toolName,
			"violations", detail,
		)
		return nil // Allow the call to proceed.
	default:
		return nil
	}
}

// findToolInCache looks up a tool from the daemon's tool cache.
func (p *callPipeline) findToolInCache(serverName, toolName string) *mcp.Tool {
	if toolName == syntheticBulkToolName && p.daemon.serverEligibleForBulk(serverName) {
		tool := bulkSyntheticTool(serverName)
		return &tool
	}

	cache := p.daemon.toolCache
	if cache == nil {
		return nil
	}

	cache.mu.RLock()
	defer cache.mu.RUnlock()

	// Tools in cache use compound names: server__tool or just tool.
	compoundName := serverName + "__" + toolName
	visible := visibleTools(cache.tools)
	for i := range visible {
		if visible[i].Name == compoundName || visible[i].Name == toolName {
			return &visible[i]
		}
	}
	return nil
}

// checkPropertyType validates a single property value against its schema definition.
func checkPropertyType(key string, val any, propSchema any) string {
	schemaMap, ok := propSchema.(map[string]any)
	if !ok {
		return ""
	}

	expectedType, ok := schemaMap["type"].(string)
	if !ok {
		return ""
	}

	switch expectedType {
	case "string":
		if _, ok := val.(string); !ok {
			return fmt.Sprintf("field %q: expected string, got %T", key, val)
		}
	case "number", "integer":
		switch val.(type) {
		case float64, int, int64, json.Number:
			// OK
		default:
			return fmt.Sprintf("field %q: expected %s, got %T", key, expectedType, val)
		}
	case "boolean":
		if _, ok := val.(bool); !ok {
			return fmt.Sprintf("field %q: expected boolean, got %T", key, val)
		}
	case "array":
		if _, ok := val.([]any); !ok {
			return fmt.Sprintf("field %q: expected array, got %T", key, val)
		}
	case "object":
		if _, ok := val.(map[string]any); !ok {
			return fmt.Sprintf("field %q: expected object, got %T", key, val)
		}
	}
	return ""
}
